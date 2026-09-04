package rating_test

import (
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestBaselineGeneratedRoomProperties(t *testing.T) {
	t.Parallel()

	for _, roomSize := range []int{5, 6, 7} {
		roomSize := roomSize
		t.Run(fmt.Sprintf("%d players", roomSize), func(t *testing.T) {
			t.Parallel()
			model := newBaseline(t)
			// A fixed non-cryptographic seed makes generated property cases replayable.
			random := rand.New(rand.NewPCG(uint64(roomSize), 0x5eed)) //nolint:gosec

			for scenario := range 128 {
				result, estimates, processedAt := orderedResult(t, model, roomSize)
				result.EventID = fmt.Sprintf("event-%d", scenario)
				result.RoomID = fmt.Sprintf("room-%d", scenario)
				places := random.Perm(roomSize)
				for index, participant := range result.Participants {
					result.Participants[index].Place = places[index] + 1
					estimate := estimates[participant.PlayerID]
					estimate.Mean = 5 + random.Float64()*40
					estimate.Uncertainty = 1 + random.Float64()*24
					if index%2 == 0 {
						deviation := random.Float64() * 10
						estimate.PerformanceDeviation = &deviation
					}
					estimates[participant.PlayerID] = estimate
				}

				prediction, err := model.Predict(predictionRequest(result, estimates))
				if err != nil {
					t.Fatalf("scenario %d Predict() error = %v", scenario, err)
				}
				assertCoherentDistribution(t, prediction)

				original := cloneEstimates(estimates)
				updates, err := model.Update(result, estimates, processedAt)
				if err != nil {
					t.Fatalf("scenario %d Update() error = %v", scenario, err)
				}
				if !reflect.DeepEqual(estimates, original) {
					t.Fatalf("scenario %d mutated pre-game estimates", scenario)
				}
				if len(updates) != roomSize {
					t.Fatalf("scenario %d returned %d updates, want %d", scenario, len(updates), roomSize)
				}
				for _, update := range updates {
					if err := update.After.Validate(); err != nil {
						t.Errorf("scenario %d player %q produced invalid rating: %v", scenario, update.PlayerID, err)
					}
					if update.After.Games != update.Before.Games+1 {
						t.Errorf("scenario %d player %q games = %d, want %d", scenario, update.PlayerID, update.After.Games, update.Before.Games+1)
					}
					if update.After.Uncertainty > update.Before.Uncertainty || update.After.Uncertainty < 1 {
						t.Errorf("scenario %d player %q uncertainty moved outside [%f, %f]: %f", scenario, update.PlayerID, 1.0, update.Before.Uncertainty, update.After.Uncertainty)
					}
				}
			}
		})
	}
}

func TestBaselineSimulationIsReproducibleAndLearnsOrdering(t *testing.T) {
	t.Parallel()

	first := runRatingSimulation(t, 0x51a7, 0xcafe, 256)
	second := runRatingSimulation(t, 0x51a7, 0xcafe, 256)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("fixed-seed rating simulations produced different results")
	}

	for index := 1; index < len(first.players); index++ {
		stronger := first.finalEstimates[first.players[index-1].id]
		weaker := first.finalEstimates[first.players[index].id]
		if stronger.Mean <= weaker.Mean {
			t.Errorf("learned means do not follow latent ordering: %s=%f <= %s=%f", first.players[index-1].id, stronger.Mean, first.players[index].id, weaker.Mean)
		}
	}
	if first.report.Rooms != 256 || first.report.Players != 256*rating.MaxRoomSize {
		t.Fatalf("unexpected calibration sample size: %+v", first.report)
	}
	if !finiteMetric(first.report.MulticlassBrierScore) || !finiteMetric(first.report.MeanLogLoss) || !finiteMetric(first.report.ExpectedCalibrationError) {
		t.Fatalf("simulation produced non-finite calibration metrics: %+v", first.report)
	}
}

type simulatedPlayer struct {
	id          string
	latentSkill float64
}

type simulationSummary struct {
	players        []simulatedPlayer
	finalEstimates map[string]rating.Estimate
	report         rating.CalibrationReport
}

func runRatingSimulation(t *testing.T, seed1, seed2 uint64, games int) simulationSummary {
	t.Helper()
	model := newBaseline(t)
	// A fixed non-cryptographic seed is part of the simulation replay contract.
	random := rand.New(rand.NewPCG(seed1, seed2)) //nolint:gosec
	players := []simulatedPlayer{
		{id: "player-1", latentSkill: 34},
		{id: "player-2", latentSkill: 31},
		{id: "player-3", latentSkill: 28},
		{id: "player-4", latentSkill: 25},
		{id: "player-5", latentSkill: 22},
		{id: "player-6", latentSkill: 19},
		{id: "player-7", latentSkill: 16},
	}
	start := time.Date(2026, time.September, 4, 2, 0, 0, 0, time.UTC)
	estimates := make(map[string]rating.Estimate, len(players))
	for _, player := range players {
		estimate, err := model.InitialEstimate(start.Add(-time.Minute))
		if err != nil {
			t.Fatalf("InitialEstimate() error = %v", err)
		}
		estimates[player.id] = estimate
	}

	observations := make([]rating.CalibrationObservation, 0, games)
	for game := range games {
		generatedAt := start.Add(time.Duration(game) * time.Minute)
		result := simulatedResult(players, random, game, generatedAt.Add(30*time.Second))
		prediction, err := model.Predict(rating.PredictionRequest{
			RoomID:      result.RoomID,
			ModeID:      result.ModeID,
			GeneratedAt: generatedAt,
			Estimates:   estimates,
		})
		if err != nil {
			t.Fatalf("game %d Predict() error = %v", game, err)
		}
		observations = append(observations, rating.CalibrationObservation{Prediction: prediction, Result: result})

		updates, err := model.Update(result, estimates, result.AvailableAt.Add(time.Second))
		if err != nil {
			t.Fatalf("game %d Update() error = %v", game, err)
		}
		for _, update := range updates {
			estimates[update.PlayerID] = update.After
		}
	}

	report, err := rating.EvaluateCalibration(observations, 10)
	if err != nil {
		t.Fatalf("EvaluateCalibration() error = %v", err)
	}
	return simulationSummary{players: players, finalEstimates: estimates, report: report}
}

func simulatedResult(players []simulatedPlayer, random *rand.Rand, game int, finishedAt time.Time) rating.MatchResult {
	type performance struct {
		playerID string
		value    float64
	}
	performances := make([]performance, len(players))
	for index, player := range players {
		performances[index] = performance{playerID: player.id, value: player.latentSkill + random.NormFloat64()*4}
	}
	slices.SortFunc(performances, func(left, right performance) int {
		return -compareFloat(left.value, right.value)
	})
	places := make(map[string]int, len(players))
	for index, item := range performances {
		places[item.playerID] = index + 1
	}

	participants := make([]rating.ParticipantResult, len(players))
	for index, player := range players {
		participants[index] = rating.ParticipantResult{PlayerID: player.id, Place: places[player.id]}
	}
	availableAt := finishedAt.Add(time.Second)
	return rating.MatchResult{
		EventID:             fmt.Sprintf("event-%d", game),
		RoomID:              fmt.Sprintf("room-%d", game),
		ModeID:              "classic",
		DeckID:              fmt.Sprintf("deck-%d", game),
		ScoringRulesVersion: "rules-v1",
		FinishedAt:          finishedAt,
		AvailableAt:         availableAt,
		Participants:        participants,
	}
}

func compareFloat(left, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func finiteMetric(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
