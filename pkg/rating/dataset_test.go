package rating_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestSplitFeatureBatchesUsesAvailabilityOrderAndClonesPartitions(t *testing.T) {
	t.Parallel()

	encoder := newFeatureEncoder(t)
	cutoff := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	batches := []rating.FeatureBatch{
		featureBatchAt(t, encoder, "event-c", "room-c", cutoff.Add(time.Hour), int64Pointer(30)),
		featureBatchAt(t, encoder, "event-b", "room-b", cutoff, int64Pointer(20)),
		featureBatchAt(t, encoder, "event-a", "room-a", cutoff.Add(-time.Hour), int64Pointer(10)),
	}

	set, err := rating.SplitFeatureBatches(batches, cutoff)
	if err != nil {
		t.Fatalf("SplitFeatureBatches() error = %v", err)
	}
	training := set.Training()
	holdout := set.Holdout()
	if set.TrainingCutoff() != cutoff || len(training) != 2 || len(holdout) != 1 {
		t.Fatalf("split metadata: cutoff=%v training=%d holdout=%d", set.TrainingCutoff(), len(training), len(holdout))
	}
	if training[0].EventID != "event-a" || training[1].EventID != "event-b" || holdout[0].EventID != "event-c" {
		t.Fatalf("split order: training=%q,%q holdout=%q", training[0].EventID, training[1].EventID, holdout[0].EventID)
	}

	training[0].Players[0].Observations[0].Value = 999
	if set.Training()[0].Players[0].Observations[0].Value == 999 {
		t.Fatal("Training() exposed mutable partition state")
	}
}

func TestFeatureStandardizerFitsTrainingOnlyAndPreservesMissingValues(t *testing.T) {
	t.Parallel()

	encoder := newFeatureEncoder(t)
	cutoff := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	first := featureBatchAt(t, encoder, "event-a", "room-a", cutoff.Add(-time.Hour), int64Pointer(10))
	second := featureBatchAt(t, encoder, "event-b", "room-b", cutoff, int64Pointer(30))
	holdout := featureBatchAt(t, encoder, "event-c", "room-c", cutoff.Add(time.Hour), int64Pointer(1_000_000))
	holdout.Players[0].Observations[0] = rating.FeatureObservation{Name: rating.FeatureScore}

	set, err := rating.SplitFeatureBatches([]rating.FeatureBatch{holdout, second, first}, cutoff)
	if err != nil {
		t.Fatalf("SplitFeatureBatches() error = %v", err)
	}
	standardizer, err := rating.FitFeatureStandardizer(set)
	if err != nil {
		t.Fatalf("FitFeatureStandardizer() error = %v", err)
	}

	statistics := standardizer.Statistics()
	if standardizer.TrainedThrough() != cutoff || len(statistics) != 4 {
		t.Fatalf("standardizer metadata: trained=%v statistics=%d", standardizer.TrainedThrough(), len(statistics))
	}
	if statistics[0].Name != rating.FeatureScore || statistics[0].Observations != 10 ||
		statistics[0].Mean != 20 || statistics[0].StandardDeviation != 10 {
		t.Fatalf("score statistics = %+v", statistics[0])
	}
	if statistics[1].StandardDeviation != 0 {
		t.Fatalf("constant feature standard deviation = %f, want 0", statistics[1].StandardDeviation)
	}

	standardizedTraining, err := standardizer.TransformTraining(set)
	if err != nil {
		t.Fatalf("TransformTraining() error = %v", err)
	}
	if standardizedTraining[0].Players[0].Observations[0].Value != -1 ||
		standardizedTraining[1].Players[0].Observations[0].Value != 1 {
		t.Fatalf("standardized training score values = %f, %f", standardizedTraining[0].Players[0].Observations[0].Value, standardizedTraining[1].Players[0].Observations[0].Value)
	}
	if standardizedTraining[0].Players[0].Observations[1].Value != 0 {
		t.Fatalf("constant standardized feature = %f, want 0", standardizedTraining[0].Players[0].Observations[1].Value)
	}

	standardizedHoldout, err := standardizer.TransformHoldout(set)
	if err != nil {
		t.Fatalf("TransformHoldout() error = %v", err)
	}
	missing := standardizedHoldout[0].Players[0].Observations[0]
	if missing.Present || missing.Value != 0 {
		t.Fatalf("standardized missing observation = %+v", missing)
	}
	observed := standardizedHoldout[0].Players[1].Observations[0]
	if !observed.Present || observed.Value != 99_998 {
		t.Fatalf("standardized holdout observation = %+v", observed)
	}

	statistics[0].Mean = 999
	if standardizer.Statistics()[0].Mean != 20 {
		t.Fatal("Statistics() exposed mutable standardizer state")
	}
}

func TestSplitFeatureBatchesRejectsInvalidDatasets(t *testing.T) {
	t.Parallel()

	encoder := newFeatureEncoder(t)
	cutoff := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	valid := func() []rating.FeatureBatch {
		return []rating.FeatureBatch{
			featureBatchAt(t, encoder, "event-a", "room-a", cutoff, int64Pointer(10)),
			featureBatchAt(t, encoder, "event-b", "room-b", cutoff.Add(time.Hour), int64Pointer(20)),
		}
	}

	tests := []struct {
		name      string
		cutoff    time.Time
		mutate    func([]rating.FeatureBatch) []rating.FeatureBatch
		wantError string
	}{
		{name: "missing cutoff", mutate: func(batches []rating.FeatureBatch) []rating.FeatureBatch { return batches }, wantError: "cutoff"},
		{name: "one batch", cutoff: cutoff, mutate: func(batches []rating.FeatureBatch) []rating.FeatureBatch { return batches[:1] }, wantError: "at least two"},
		{name: "empty holdout", cutoff: cutoff.Add(2 * time.Hour), mutate: func(batches []rating.FeatureBatch) []rating.FeatureBatch { return batches }, wantError: "non-empty"},
		{name: "mixed schema", cutoff: cutoff, mutate: func(batches []rating.FeatureBatch) []rating.FeatureBatch {
			batches[1].SchemaVersion = "features-v2"
			return batches
		}, wantError: "share one schema"},
		{name: "duplicate event", cutoff: cutoff, mutate: func(batches []rating.FeatureBatch) []rating.FeatureBatch {
			batches[1].EventID = batches[0].EventID
			return batches
		}, wantError: "duplicate event"},
		{name: "non-canonical missing value", cutoff: cutoff, mutate: func(batches []rating.FeatureBatch) []rating.FeatureBatch {
			batches[0].Players[0].Observations[0].Present = false
			batches[0].Players[0].Observations[0].Value = 5
			return batches
		}, wantError: "missing feature"},
		{name: "non-finite value", cutoff: cutoff, mutate: func(batches []rating.FeatureBatch) []rating.FeatureBatch {
			batches[0].Players[0].Observations[0].Value = math.Inf(1)
			return batches
		}, wantError: "must be finite"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			batches := tt.mutate(valid())

			_, err := rating.SplitFeatureBatches(batches, tt.cutoff)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("SplitFeatureBatches() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestFeatureStandardizerRejectsFeatureMissingFromTraining(t *testing.T) {
	t.Parallel()

	encoder := newFeatureEncoder(t)
	cutoff := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	training := featureBatchAt(t, encoder, "event-a", "room-a", cutoff, nil)
	holdout := featureBatchAt(t, encoder, "event-b", "room-b", cutoff.Add(time.Hour), int64Pointer(20))
	set, err := rating.SplitFeatureBatches([]rating.FeatureBatch{training, holdout}, cutoff)
	if err != nil {
		t.Fatalf("SplitFeatureBatches() error = %v", err)
	}

	_, err = rating.FitFeatureStandardizer(set)
	if err == nil || !strings.Contains(err.Error(), "no present training observations") {
		t.Fatalf("FitFeatureStandardizer() error = %v", err)
	}
}

func TestFeatureStandardizerRejectsOverflowedTrainingStatistics(t *testing.T) {
	t.Parallel()

	encoder := newFeatureEncoder(t)
	cutoff := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	first := featureBatchAt(t, encoder, "event-a", "room-a", cutoff.Add(-time.Hour), int64Pointer(10))
	second := featureBatchAt(t, encoder, "event-b", "room-b", cutoff, int64Pointer(20))
	holdout := featureBatchAt(t, encoder, "event-c", "room-c", cutoff.Add(time.Hour), int64Pointer(30))
	for index := range first.Players {
		first.Players[index].Observations[0].Value = math.MaxFloat64
		second.Players[index].Observations[0].Value = -math.MaxFloat64
	}

	set, err := rating.SplitFeatureBatches([]rating.FeatureBatch{first, second, holdout}, cutoff)
	if err != nil {
		t.Fatalf("SplitFeatureBatches() error = %v", err)
	}
	_, err = rating.FitFeatureStandardizer(set)
	if err == nil || !strings.Contains(err.Error(), "non-finite training statistics") {
		t.Fatalf("FitFeatureStandardizer() error = %v", err)
	}
}

func newFeatureEncoder(t *testing.T) *rating.FeatureEncoder {
	t.Helper()

	encoder, err := rating.NewFeatureEncoder(testFeatureSchema())
	if err != nil {
		t.Fatalf("NewFeatureEncoder() error = %v", err)
	}
	return encoder
}

func featureBatchAt(
	t *testing.T,
	encoder *rating.FeatureEncoder,
	eventID string,
	roomID string,
	availableAt time.Time,
	score *int64,
) rating.FeatureBatch {
	t.Helper()

	result := testFeatureResult()
	result.EventID = eventID
	result.RoomID = roomID
	result.DeckID = roomID + "-deck"
	result.FinishedAt = availableAt.Add(-time.Second)
	result.AvailableAt = availableAt
	for index := range result.Participants {
		result.Participants[index].Features = rating.Features{
			Score:     score,
			Completed: boolPointer(true),
			Moves:     int64Pointer(50),
			UndoMoves: int64Pointer(0),
		}
	}

	batch, err := encoder.Encode(result, rating.FeatureContext{DeckVersion: "deck-v3"})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	return batch
}
