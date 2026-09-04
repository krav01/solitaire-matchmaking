package rating_test

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestBaselinePredictEqualPlayersHaveUniformPlacementProbabilities(t *testing.T) {
	t.Parallel()

	for _, roomSize := range []int{5, 6, 7} {
		roomSize := roomSize
		t.Run(fmt.Sprintf("%d players", roomSize), func(t *testing.T) {
			t.Parallel()
			model := newBaseline(t)
			result, estimates, _ := orderedResult(t, model, roomSize)
			prediction, err := model.Predict(predictionRequest(result, estimates))
			if err != nil {
				t.Fatalf("Predict() error = %v", err)
			}
			assertCoherentDistribution(t, prediction)

			wantProbability := 1 / float64(roomSize)
			wantExpectedPlace := float64(roomSize+1) / 2
			placeTotals := make([]float64, roomSize)
			for _, participant := range prediction.Participants {
				if !closeTo(participant.FirstPlaceProbability, wantProbability) {
					t.Errorf("first-place probability = %f, want %f", participant.FirstPlaceProbability, wantProbability)
				}
				if !closeTo(participant.ExpectedPlace, wantExpectedPlace) {
					t.Errorf("expected place = %f, want %f", participant.ExpectedPlace, wantExpectedPlace)
				}
				for placeIndex, probability := range participant.PlaceProbabilities {
					if !closeTo(probability, wantProbability) {
						t.Errorf("place probability = %f, want %f", probability, wantProbability)
					}
					placeTotals[placeIndex] += probability
				}
			}
			for placeIndex, total := range placeTotals {
				if !closeTo(total, 1) {
					t.Errorf("probabilities for place %d total %f, want 1", placeIndex+1, total)
				}
			}
		})
	}
}

func TestBaselinePredictOrdersPlayersBySkill(t *testing.T) {
	t.Parallel()
	model := newBaseline(t)
	result, estimates, _ := orderedResult(t, model, 5)
	means := []float64{40, 32, 25, 18, 10}
	for index, participant := range result.Participants {
		estimate := estimates[participant.PlayerID]
		estimate.Mean = means[index]
		estimate.Uncertainty = 2
		estimates[participant.PlayerID] = estimate
	}

	prediction, err := model.Predict(predictionRequest(result, estimates))
	if err != nil {
		t.Fatalf("Predict() error = %v", err)
	}
	assertCoherentDistribution(t, prediction)
	for index := 1; index < len(prediction.Participants); index++ {
		stronger := prediction.Participants[index-1]
		weaker := prediction.Participants[index]
		if stronger.FirstPlaceProbability <= weaker.FirstPlaceProbability {
			t.Errorf("first-place probability does not follow skill: %f <= %f", stronger.FirstPlaceProbability, weaker.FirstPlaceProbability)
		}
		if stronger.ExpectedPlace >= weaker.ExpectedPlace {
			t.Errorf("expected place does not follow skill: %f >= %f", stronger.ExpectedPlace, weaker.ExpectedPlace)
		}
	}
}

func TestBaselinePredictUncertaintyFlattensProbabilities(t *testing.T) {
	t.Parallel()
	model := newBaseline(t)
	result, estimates, _ := orderedResult(t, model, 5)
	for index, participant := range result.Participants {
		estimate := estimates[participant.PlayerID]
		estimate.Mean = 35 - float64(index)*5
		estimate.Uncertainty = 1
		estimates[participant.PlayerID] = estimate
	}

	lowUncertainty, err := model.Predict(predictionRequest(result, estimates))
	if err != nil {
		t.Fatalf("low-uncertainty Predict() error = %v", err)
	}
	for playerID, estimate := range estimates {
		estimate.Uncertainty = 20
		estimates[playerID] = estimate
	}
	highUncertainty, err := model.Predict(predictionRequest(result, estimates))
	if err != nil {
		t.Fatalf("high-uncertainty Predict() error = %v", err)
	}

	uniform := 1 / float64(len(estimates))
	lowDistance := math.Abs(lowUncertainty.Participants[0].FirstPlaceProbability - uniform)
	highDistance := math.Abs(highUncertainty.Participants[0].FirstPlaceProbability - uniform)
	if highDistance >= lowDistance {
		t.Fatalf("high uncertainty distance = %f, want less than low uncertainty distance %f", highDistance, lowDistance)
	}
}

func TestBaselinePredictIsDeterministicAndRejectsFutureRatings(t *testing.T) {
	t.Parallel()
	model := newBaseline(t)
	result, estimates, _ := orderedResult(t, model, 5)
	request := predictionRequest(result, estimates)

	first, err := model.Predict(request)
	if err != nil {
		t.Fatalf("first Predict() error = %v", err)
	}
	second, err := model.Predict(request)
	if err != nil {
		t.Fatalf("second Predict() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("predictions differ\nfirst:  %+v\nsecond: %+v", first, second)
	}

	future := request.Estimates["player-1"]
	future.UpdatedAt = request.GeneratedAt.Add(time.Nanosecond)
	request.Estimates["player-1"] = future
	if _, err := model.Predict(request); err == nil {
		t.Fatal("Predict() error = nil for a rating updated after prediction time")
	}
}

func predictionRequest(result rating.MatchResult, estimates map[string]rating.Estimate) rating.PredictionRequest {
	return rating.PredictionRequest{
		RoomID:      result.RoomID,
		ModeID:      result.ModeID,
		GeneratedAt: result.FinishedAt.Add(-time.Minute),
		Estimates:   estimates,
	}
}

func closeTo(left, right float64) bool {
	return math.Abs(left-right) <= 1e-10
}

func assertCoherentDistribution(t *testing.T, prediction rating.RoomPrediction) {
	t.Helper()
	roomSize := len(prediction.Participants)
	placeTotals := make([]float64, roomSize)
	for _, participant := range prediction.Participants {
		playerTotal := 0.0
		for placeIndex, probability := range participant.PlaceProbabilities {
			if probability < 0 || probability > 1 {
				t.Errorf("player %q place probability = %f, want [0,1]", participant.PlayerID, probability)
			}
			playerTotal += probability
			placeTotals[placeIndex] += probability
		}
		if !closeTo(playerTotal, 1) {
			t.Errorf("player %q probabilities total %f, want 1", participant.PlayerID, playerTotal)
		}
	}
	for placeIndex, total := range placeTotals {
		if !closeTo(total, 1) {
			t.Errorf("place %d probabilities total %f, want 1", placeIndex+1, total)
		}
	}
}
