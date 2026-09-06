package rating_test

import (
	"strings"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestExtendedPredictUsesOnlyPresentPriorProfiles(t *testing.T) {
	t.Parallel()
	trainedThrough := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	model, err := rating.NewExtended(extendedConfig(trainedThrough))
	if err != nil {
		t.Fatalf("NewExtended() error = %v", err)
	}
	estimates := make(map[string]rating.Estimate, 5)
	for index, playerID := range []string{"a", "b", "c", "d", "e"} {
		estimate, initErr := model.InitialEstimate(trainedThrough)
		if initErr != nil {
			t.Fatalf("InitialEstimate() error = %v", initErr)
		}
		estimate.Games = uint64(index)
		estimates[playerID] = estimate
	}
	generatedAt := trainedThrough.Add(time.Hour)
	prediction, err := model.Predict(rating.PredictionRequest{
		RoomID: "room-a", ModeID: "solitaire", GeneratedAt: generatedAt, Estimates: estimates,
	}, map[string]rating.FeatureProfile{
		"a": {SchemaVersion: "features-v1", Observations: []rating.ProfileObservation{{Name: rating.FeatureScore, Mean: 150, Count: 2}}},
		"b": {SchemaVersion: "features-v1", Observations: []rating.ProfileObservation{{Name: rating.FeatureScore}}},
	})
	if err != nil {
		t.Fatalf("Predict() error = %v", err)
	}
	probabilities := make(map[string]float64, len(prediction.Participants))
	for _, participant := range prediction.Participants {
		probabilities[participant.PlayerID] = participant.FirstPlaceProbability
	}
	if probabilities["a"] <= probabilities["b"] || probabilities["b"] != probabilities["c"] {
		t.Fatalf("first-place probabilities = %+v", probabilities)
	}
}

func TestExtendedObservePreservesMissingAndRunningMeans(t *testing.T) {
	t.Parallel()
	trainedThrough := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	model, err := rating.NewExtended(extendedConfig(trainedThrough))
	if err != nil {
		t.Fatalf("NewExtended() error = %v", err)
	}
	result := extendedResult(trainedThrough.Add(time.Hour))
	score := int64(50)
	result.Participants[0].Features.Score = &score
	profiles, err := model.Observe(result, "deck-v1", nil)
	if err != nil {
		t.Fatalf("Observe(first) error = %v", err)
	}
	if got := profiles["a"].Observations[0]; got.Count != 1 || got.Mean != 50 {
		t.Fatalf("observed profile = %+v", got)
	}
	if got := profiles["b"].Observations[0]; got.Count != 0 || got.Mean != 0 {
		t.Fatalf("missing profile = %+v", got)
	}
	score = 150
	result.EventID = "event-b"
	result.RoomID = "room-b"
	result.Participants[0].Features.Score = &score
	profiles, err = model.Observe(result, "deck-v1", profiles)
	if err != nil {
		t.Fatalf("Observe(second) error = %v", err)
	}
	if got := profiles["a"].Observations[0]; got.Count != 2 || got.Mean != 100 {
		t.Fatalf("running profile = %+v", got)
	}
}

func TestExtendedRejectsGuessedOrIncompatibleDefinitions(t *testing.T) {
	t.Parallel()
	trainedThrough := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		mutate    func(*rating.ExtendedConfig)
		wantError string
	}{
		{name: "missing horizon", mutate: func(config *rating.ExtendedConfig) { config.TrainedThrough = time.Time{} }, wantError: "horizon"},
		{name: "missing coefficient", mutate: func(config *rating.ExtendedConfig) { config.FeatureWeights = nil }, wantError: "one weight"},
		{name: "schema order", mutate: func(config *rating.ExtendedConfig) { config.FeatureWeights[0].Name = rating.FeatureMoves }, wantError: "schema order"},
		{name: "negative deviation", mutate: func(config *rating.ExtendedConfig) { config.FeatureWeights[0].StandardDeviation = -1 }, wantError: "finite"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := extendedConfig(trainedThrough)
			tt.mutate(&config)
			_, err := rating.NewExtended(config)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("NewExtended() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func extendedConfig(trainedThrough time.Time) rating.ExtendedConfig {
	return rating.ExtendedConfig{
		Baseline: rating.DefaultBaselineConfig("candidate-v1"),
		FeatureSchema: rating.FeatureSchema{
			Version: "features-v1", ModeID: "solitaire",
			ScoringRulesVersion: "scoring-v1", DeckVersion: "deck-v1",
			Definitions: []rating.FeatureDefinition{{Name: rating.FeatureScore, SignalFamily: "performance"}},
		},
		FeatureWeights: []rating.FeatureWeight{{
			Name: rating.FeatureScore, Mean: 100, StandardDeviation: 50, Weight: 2,
		}},
		TrainedThrough: trainedThrough,
	}
}

func extendedResult(availableAt time.Time) rating.MatchResult {
	participants := make([]rating.ParticipantResult, 5)
	for index, playerID := range []string{"a", "b", "c", "d", "e"} {
		participants[index] = rating.ParticipantResult{PlayerID: playerID, Place: index + 1}
	}
	return rating.MatchResult{
		EventID: "event-a", RoomID: "room-a", ModeID: "solitaire", DeckID: "deck-instance",
		ScoringRulesVersion: "scoring-v1", FinishedAt: availableAt.Add(-time.Second),
		AvailableAt: availableAt, Participants: participants,
	}
}
