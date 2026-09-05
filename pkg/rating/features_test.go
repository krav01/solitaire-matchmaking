package rating_test

import (
	"strings"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestFeatureEncoderPreservesMissingValuesAndObservedZeroes(t *testing.T) {
	t.Parallel()

	encoder, err := rating.NewFeatureEncoder(testFeatureSchema())
	if err != nil {
		t.Fatalf("NewFeatureEncoder() error = %v", err)
	}

	result := testFeatureResult()
	result.Participants[0].Features = rating.Features{
		Score:     int64Pointer(900),
		Completed: boolPointer(true),
		Moves:     int64Pointer(73),
		UndoMoves: int64Pointer(2),
	}
	result.Participants[4].Features = rating.Features{
		Completed: boolPointer(false),
		Moves:     int64Pointer(0),
		UndoMoves: int64Pointer(0),
	}

	batch, err := encoder.Encode(result, rating.FeatureContext{DeckVersion: "deck-v3"})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if batch.SchemaVersion != "features-v1" || batch.DeckVersion != "deck-v3" || len(batch.Players) != 5 {
		t.Fatalf("Encode() metadata = %+v", batch)
	}
	if batch.Players[0].PlayerID != "player-a" || batch.Players[4].PlayerID != "player-e" {
		t.Fatalf("Encode() player order = %q ... %q", batch.Players[0].PlayerID, batch.Players[4].PlayerID)
	}

	first := batch.Players[0].Observations
	wantFirst := []rating.FeatureObservation{
		{Name: rating.FeatureScore, Value: 0, Present: false},
		{Name: rating.FeatureCompleted, Value: 0, Present: true},
		{Name: rating.FeatureMoves, Value: 0, Present: true},
		{Name: rating.FeatureUndoMoves, Value: 0, Present: true},
	}
	assertObservations(t, first, wantFirst)

	last := batch.Players[4].Observations
	wantLast := []rating.FeatureObservation{
		{Name: rating.FeatureScore, Value: 900, Present: true},
		{Name: rating.FeatureCompleted, Value: 1, Present: true},
		{Name: rating.FeatureMoves, Value: 73, Present: true},
		{Name: rating.FeatureUndoMoves, Value: 2, Present: true},
	}
	assertObservations(t, last, wantLast)
}

func TestFeatureEncoderRejectsContextMismatch(t *testing.T) {
	t.Parallel()

	encoder, err := rating.NewFeatureEncoder(testFeatureSchema())
	if err != nil {
		t.Fatalf("NewFeatureEncoder() error = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*rating.MatchResult)
		context rating.FeatureContext
	}{
		{name: "mode", mutate: func(result *rating.MatchResult) { result.ModeID = "other-mode" }, context: rating.FeatureContext{DeckVersion: "deck-v3"}},
		{name: "scoring rules", mutate: func(result *rating.MatchResult) { result.ScoringRulesVersion = "rules-v8" }, context: rating.FeatureContext{DeckVersion: "deck-v3"}},
		{name: "deck", mutate: func(*rating.MatchResult) {}, context: rating.FeatureContext{DeckVersion: "deck-v4"}},
		{name: "missing deck", mutate: func(*rating.MatchResult) {}, context: rating.FeatureContext{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := testFeatureResult()
			tt.mutate(&result)

			if _, err := encoder.Encode(result, tt.context); err == nil {
				t.Fatal("Encode() error = nil")
			}
		})
	}
}

func TestNewFeatureEncoderRejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		definitions []rating.FeatureDefinition
		wantError   string
	}{
		{
			name: "duplicate feature",
			definitions: []rating.FeatureDefinition{
				{Name: rating.FeatureMoves, SignalFamily: "moves"},
				{Name: rating.FeatureMoves, SignalFamily: "other"},
			},
			wantError: "duplicate feature",
		},
		{
			name:        "unsupported feature",
			definitions: []rating.FeatureDefinition{{Name: "luck", SignalFamily: "luck"}},
			wantError:   "unsupported feature",
		},
		{
			name: "correlated family",
			definitions: []rating.FeatureDefinition{
				{Name: rating.FeatureMoves, SignalFamily: "efficiency"},
				{Name: rating.FeatureUndoMoves, SignalFamily: "efficiency"},
			},
			wantError: "share signal family",
		},
		{
			name:        "missing family",
			definitions: []rating.FeatureDefinition{{Name: rating.FeatureMoves}},
			wantError:   "requires a signal family",
		},
		{
			name: "score and elapsed",
			definitions: []rating.FeatureDefinition{
				{Name: rating.FeatureScore, SignalFamily: "points"},
				{Name: rating.FeatureElapsedMillis, SignalFamily: "speed"},
			},
			wantError: "cannot be enabled together",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := testFeatureSchema()
			schema.Definitions = tt.definitions

			_, err := rating.NewFeatureEncoder(schema)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("NewFeatureEncoder() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestFeatureEncoderRejectsInexactInteger(t *testing.T) {
	t.Parallel()

	encoder, err := rating.NewFeatureEncoder(testFeatureSchema())
	if err != nil {
		t.Fatalf("NewFeatureEncoder() error = %v", err)
	}
	result := testFeatureResult()
	result.Participants[0].Features.Score = int64Pointer((1 << 53) + 1)

	_, err = encoder.Encode(result, rating.FeatureContext{DeckVersion: "deck-v3"})
	if err == nil || !strings.Contains(err.Error(), "exact float64 integer range") {
		t.Fatalf("Encode() error = %v", err)
	}
}

func TestFeatureEncoderOwnsSchemaCopy(t *testing.T) {
	t.Parallel()

	schema := testFeatureSchema()
	encoder, err := rating.NewFeatureEncoder(schema)
	if err != nil {
		t.Fatalf("NewFeatureEncoder() error = %v", err)
	}

	schema.Definitions[0].Name = rating.FeatureElapsedMillis
	copy := encoder.Schema()
	copy.Definitions[0].Name = rating.FeatureMoves

	stored := encoder.Schema()
	if stored.Definitions[0].Name != rating.FeatureScore {
		t.Fatalf("Schema() first feature = %q, want %q", stored.Definitions[0].Name, rating.FeatureScore)
	}
}

func TestMatchResultValidateRejectsNegativeFeatures(t *testing.T) {
	t.Parallel()

	result := testFeatureResult()
	result.Participants[0].Features.Moves = int64Pointer(-1)

	err := result.Validate()
	if err == nil || !strings.Contains(err.Error(), "moves must be non-negative") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func testFeatureSchema() rating.FeatureSchema {
	return rating.FeatureSchema{
		Version:             "features-v1",
		ModeID:              "klondike-ranked",
		ScoringRulesVersion: "rules-v7",
		DeckVersion:         "deck-v3",
		Definitions: []rating.FeatureDefinition{
			{Name: rating.FeatureScore, SignalFamily: "performance"},
			{Name: rating.FeatureCompleted, SignalFamily: "completion"},
			{Name: rating.FeatureMoves, SignalFamily: "moves"},
			{Name: rating.FeatureUndoMoves, SignalFamily: "undo"},
		},
	}
}

func testFeatureResult() rating.MatchResult {
	finishedAt := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	return rating.MatchResult{
		EventID:             "event-1",
		RoomID:              "room-1",
		ModeID:              "klondike-ranked",
		DeckID:              "deck-instance-1",
		ScoringRulesVersion: "rules-v7",
		FinishedAt:          finishedAt,
		AvailableAt:         finishedAt.Add(time.Second),
		Participants: []rating.ParticipantResult{
			{PlayerID: "player-e", Place: 1},
			{PlayerID: "player-d", Place: 2},
			{PlayerID: "player-c", Place: 3},
			{PlayerID: "player-b", Place: 4},
			{PlayerID: "player-a", Place: 5},
		},
	}
}

func assertObservations(t *testing.T, got, want []rating.FeatureObservation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("observation count = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("observation %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func boolPointer(value bool) *bool {
	return &value
}
