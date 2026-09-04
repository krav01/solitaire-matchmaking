package rating_test

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestBaselineUpdateOrdersEqualPlayersForSupportedRoomSizes(t *testing.T) {
	t.Parallel()

	for _, roomSize := range []int{5, 6, 7} {
		roomSize := roomSize
		t.Run(fmt.Sprintf("%d players", roomSize), func(t *testing.T) {
			t.Parallel()
			model := newBaseline(t)
			result, estimates, processedAt := orderedResult(t, model, roomSize)
			original := cloneEstimates(estimates)

			updates, err := model.Update(result, estimates, processedAt)
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			if len(updates) != roomSize {
				t.Fatalf("Update() returned %d updates, want %d", len(updates), roomSize)
			}
			for index, update := range updates {
				if update.After.Games != update.Before.Games+1 {
					t.Errorf("update[%d] games = %d, want %d", index, update.After.Games, update.Before.Games+1)
				}
				if update.After.Uncertainty >= update.Before.Uncertainty {
					t.Errorf("update[%d] uncertainty = %f, want less than %f", index, update.After.Uncertainty, update.Before.Uncertainty)
				}
				if update.After.UpdatedAt != processedAt || update.SourceEventID != result.EventID || update.ModelVersion != "rating-v1" {
					t.Errorf("update[%d] has inconsistent replay metadata: %+v", index, update)
				}
				if index > 0 && updates[index-1].After.Mean <= update.After.Mean {
					t.Errorf("means are not ordered by placement: %f <= %f", updates[index-1].After.Mean, update.After.Mean)
				}
			}
			if !reflect.DeepEqual(estimates, original) {
				t.Fatal("Update() mutated pre-game estimates")
			}
		})
	}
}

func TestBaselineUpdateIsDeterministicAndInputOrderIndependent(t *testing.T) {
	t.Parallel()
	model := newBaseline(t)
	result, estimates, processedAt := orderedResult(t, model, 7)

	first, err := model.Update(result, estimates, processedAt)
	if err != nil {
		t.Fatalf("first Update() error = %v", err)
	}
	slices.Reverse(result.Participants)
	second, err := model.Update(result, estimates, processedAt)
	if err != nil {
		t.Fatalf("second Update() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replayed updates differ\nfirst:  %+v\nsecond: %+v", first, second)
	}
}

func TestBaselineUpdateTreatsTiedPlayersEqually(t *testing.T) {
	t.Parallel()
	model := newBaseline(t)
	result, estimates, processedAt := orderedResult(t, model, 5)
	result.Participants[1].Place = 1

	updates, err := model.Update(result, estimates, processedAt)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updates[0].After.Mean != updates[1].After.Mean || updates[0].After.Uncertainty != updates[1].After.Uncertainty {
		t.Fatalf("tied equal players received different ratings: %+v, %+v", updates[0].After, updates[1].After)
	}
}

func TestBaselineUpdateMovesUncertainPlayerFurther(t *testing.T) {
	t.Parallel()
	model := newBaseline(t)
	result, estimates, processedAt := orderedResult(t, model, 5)

	highUncertainty := cloneEstimates(estimates)
	high := highUncertainty["player-1"]
	high.Uncertainty = 12
	highUncertainty["player-1"] = high
	highUpdates, err := model.Update(result, highUncertainty, processedAt)
	if err != nil {
		t.Fatalf("high-uncertainty Update() error = %v", err)
	}

	lowUncertainty := cloneEstimates(estimates)
	low := lowUncertainty["player-1"]
	low.Uncertainty = 2
	lowUncertainty["player-1"] = low
	lowUpdates, err := model.Update(result, lowUncertainty, processedAt)
	if err != nil {
		t.Fatalf("low-uncertainty Update() error = %v", err)
	}

	highChange := math.Abs(highUpdates[0].After.Mean - highUpdates[0].Before.Mean)
	lowChange := math.Abs(lowUpdates[0].After.Mean - lowUpdates[0].Before.Mean)
	if highChange <= lowChange {
		t.Fatalf("uncertain winner change = %f, want greater than established winner change %f", highChange, lowChange)
	}
}

func TestBaselineUpdateRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	model := newBaseline(t)
	result, estimates, processedAt := orderedResult(t, model, 5)

	tests := []struct {
		name   string
		mutate func(*rating.MatchResult, map[string]rating.Estimate, *time.Time)
	}{
		{
			name: "incomplete room",
			mutate: func(result *rating.MatchResult, _ map[string]rating.Estimate, _ *time.Time) {
				result.Participants = result.Participants[:4]
			},
		},
		{
			name: "duplicate player",
			mutate: func(result *rating.MatchResult, _ map[string]rating.Estimate, _ *time.Time) {
				result.Participants[1].PlayerID = result.Participants[0].PlayerID
			},
		},
		{
			name: "missing estimate",
			mutate: func(_ *rating.MatchResult, estimates map[string]rating.Estimate, _ *time.Time) {
				delete(estimates, "player-1")
			},
		},
		{
			name: "model mismatch",
			mutate: func(_ *rating.MatchResult, estimates map[string]rating.Estimate, _ *time.Time) {
				estimate := estimates["player-1"]
				estimate.ModelVersion = "rating-v2"
				estimates["player-1"] = estimate
			},
		},
		{
			name: "processed before availability",
			mutate: func(result *rating.MatchResult, _ map[string]rating.Estimate, processedAt *time.Time) {
				*processedAt = result.AvailableAt.Add(-time.Nanosecond)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			inputResult := result
			inputResult.Participants = slices.Clone(result.Participants)
			inputEstimates := cloneEstimates(estimates)
			inputProcessedAt := processedAt
			test.mutate(&inputResult, inputEstimates, &inputProcessedAt)
			if _, err := model.Update(inputResult, inputEstimates, inputProcessedAt); err == nil {
				t.Fatal("Update() error = nil, want error")
			}
		})
	}
}

func newBaseline(t *testing.T) *rating.Baseline {
	t.Helper()
	model, err := rating.NewBaseline(rating.DefaultBaselineConfig("rating-v1"))
	if err != nil {
		t.Fatalf("NewBaseline() error = %v", err)
	}
	return model
}

func orderedResult(t *testing.T, model *rating.Baseline, roomSize int) (rating.MatchResult, map[string]rating.Estimate, time.Time) {
	t.Helper()
	finishedAt := time.Date(2026, time.September, 4, 1, 0, 0, 0, time.UTC)
	availableAt := finishedAt.Add(time.Second)
	processedAt := availableAt.Add(time.Second)
	result := rating.MatchResult{
		EventID:             "event-1",
		RoomID:              "room-1",
		ModeID:              "classic",
		DeckID:              "deck-1",
		ScoringRulesVersion: "rules-v1",
		FinishedAt:          finishedAt,
		AvailableAt:         availableAt,
		Participants:        make([]rating.ParticipantResult, 0, roomSize),
	}
	estimates := make(map[string]rating.Estimate, roomSize)
	for index := range roomSize {
		playerID := fmt.Sprintf("player-%d", index+1)
		result.Participants = append(result.Participants, rating.ParticipantResult{PlayerID: playerID, Place: index + 1})
		estimate, err := model.InitialEstimate(finishedAt.Add(-time.Hour))
		if err != nil {
			t.Fatalf("InitialEstimate() error = %v", err)
		}
		estimates[playerID] = estimate
	}
	return result, estimates, processedAt
}

func cloneEstimates(source map[string]rating.Estimate) map[string]rating.Estimate {
	clone := make(map[string]rating.Estimate, len(source))
	for playerID, estimate := range source {
		if estimate.PerformanceDeviation != nil {
			deviation := *estimate.PerformanceDeviation
			estimate.PerformanceDeviation = &deviation
		}
		clone[playerID] = estimate
	}
	return clone
}
