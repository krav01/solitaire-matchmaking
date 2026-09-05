package rating_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestCompareHoldoutModelsApprovesStableCandidateGain(t *testing.T) {
	t.Parallel()

	observations, config := passingModelComparison(t)
	report, err := rating.CompareHoldoutModels(observations, config)
	if err != nil {
		t.Fatalf("CompareHoldoutModels() error = %v", err)
	}
	if !report.Eligible || len(report.Reasons) != 0 {
		t.Fatalf("comparison eligibility = %v, reasons = %v", report.Eligible, report.Reasons)
	}
	if report.BaselineVersion != "rating-v1" || report.CandidateVersion != "rating-v2" || report.Rooms != 2 || report.Players != 11 {
		t.Fatalf("comparison metadata = %+v", report)
	}
	if len(report.Segments) != 2 || report.Segments[0].SegmentID != "five-player" || report.Segments[1].SegmentID != "six-player" {
		t.Fatalf("comparison segment order = %+v", report.Segments)
	}
	if report.Candidate.MulticlassBrierScore != 0 || report.Candidate.MeanLogLoss != 0 ||
		report.CandidateMinusBaseline.MulticlassBrierScore >= 0 {
		t.Fatalf("candidate metrics = %+v, delta = %+v", report.Candidate, report.CandidateMinusBaseline)
	}
	if err := report.ValidateForActivation(); err != nil {
		t.Fatalf("ValidateForActivation() error = %v", err)
	}
}

func TestCompareHoldoutModelsRejectsWeakSegmentWithoutProcessingError(t *testing.T) {
	t.Parallel()

	observations, config := passingModelComparison(t)
	observations[1].CandidatePrediction = reversedPrediction(observations[1].Result, "rating-v2")

	report, err := rating.CompareHoldoutModels(observations, config)
	if err != nil {
		t.Fatalf("CompareHoldoutModels() error = %v", err)
	}
	if report.Eligible || report.Segments[0].Passed || len(report.Reasons) == 0 {
		t.Fatalf("weak candidate report: eligible=%v segment=%v reasons=%v", report.Eligible, report.Segments[0].Passed, report.Reasons)
	}
	if !strings.Contains(strings.Join(report.Reasons, " "), "segment \"five-player\"") {
		t.Fatalf("comparison reasons = %v", report.Reasons)
	}
	if err := report.ValidateForActivation(); err == nil {
		t.Fatal("ValidateForActivation() error = nil for weak candidate")
	}
}

func TestCompareHoldoutModelsRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mutate    func([]rating.PairedCalibrationObservation, *rating.ModelComparisonConfig)
		wantError string
	}{
		{
			name: "duplicate event",
			mutate: func(observations []rating.PairedCalibrationObservation, _ *rating.ModelComparisonConfig) {
				observations[1].Result.EventID = observations[0].Result.EventID
			},
			wantError: "duplicate event",
		},
		{
			name: "same model version",
			mutate: func(observations []rating.PairedCalibrationObservation, _ *rating.ModelComparisonConfig) {
				for index := range observations {
					observations[index].CandidatePrediction.ModelVersion = "rating-v1"
				}
			},
			wantError: "distinct",
		},
		{
			name: "mixed segment",
			mutate: func(observations []rating.PairedCalibrationObservation, _ *rating.ModelComparisonConfig) {
				observations[1].SegmentID = observations[0].SegmentID
			},
			wantError: "mixes",
		},
		{
			name: "invalid policy",
			mutate: func(_ []rating.PairedCalibrationObservation, config *rating.ModelComparisonConfig) {
				config.Policy.MaximumSegmentBrierRegression = math.NaN()
			},
			wantError: "finite",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			observations, config := passingModelComparison(t)
			tt.mutate(observations, &config)

			_, err := rating.CompareHoldoutModels(observations, config)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("CompareHoldoutModels() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestModelComparisonValidationRejectsTamperedReport(t *testing.T) {
	t.Parallel()

	observations, config := passingModelComparison(t)
	report, err := rating.CompareHoldoutModels(observations, config)
	if err != nil {
		t.Fatalf("CompareHoldoutModels() error = %v", err)
	}
	report.CandidateMinusBaseline.MulticlassBrierScore = 0

	if err := report.ValidateForActivation(); err == nil || !strings.Contains(err.Error(), "aggregate metrics") {
		t.Fatalf("ValidateForActivation() error = %v", err)
	}
}

func passingModelComparison(t *testing.T) ([]rating.PairedCalibrationObservation, rating.ModelComparisonConfig) {
	t.Helper()

	model := newBaseline(t)
	five := pairedObservation(t, model, 5, "event-5", "room-5", "five-player")
	six := pairedObservation(t, model, 6, "event-6", "room-6", "six-player")
	cutoff := five.Result.AvailableAt.Add(-time.Hour)
	config := rating.ModelComparisonConfig{
		TrainingCutoff:          cutoff,
		BaselineTrainedThrough:  cutoff,
		CandidateTrainedThrough: cutoff,
		BinCount:                10,
		Policy: rating.ModelComparisonPolicy{
			MinimumRoomsPerSegment:              1,
			MinimumOverallBrierImprovement:      0.1,
			MaximumSegmentBrierRegression:       0,
			MaximumSegmentLogLossRegression:     0,
			MaximumSegmentCalibrationRegression: 0,
		},
	}

	return []rating.PairedCalibrationObservation{six, five}, config
}

func pairedObservation(
	t *testing.T,
	model *rating.Baseline,
	roomSize int,
	eventID string,
	roomID string,
	segmentID string,
) rating.PairedCalibrationObservation {
	t.Helper()

	result, estimates, _ := orderedResult(t, model, roomSize)
	result.EventID = eventID
	result.RoomID = roomID
	result.DeckID = roomID + "-deck"
	baseline, err := model.Predict(predictionRequest(result, estimates))
	if err != nil {
		t.Fatalf("Predict() error = %v", err)
	}

	return rating.PairedCalibrationObservation{
		SegmentID:           segmentID,
		BaselinePrediction:  baseline,
		CandidatePrediction: perfectPrediction(result, "rating-v2"),
		Result:              result,
	}
}

func perfectPrediction(result rating.MatchResult, modelVersion string) rating.RoomPrediction {
	prediction := rating.RoomPrediction{
		RoomID:       result.RoomID,
		ModeID:       result.ModeID,
		ModelVersion: modelVersion,
		GeneratedAt:  result.FinishedAt.Add(-time.Minute),
		Participants: make([]rating.PlacementPrediction, len(result.Participants)),
	}
	for index, participant := range result.Participants {
		probabilities := make([]float64, len(result.Participants))
		probabilities[participant.Place-1] = 1
		prediction.Participants[index] = rating.PlacementPrediction{
			PlayerID:              participant.PlayerID,
			PlaceProbabilities:    probabilities,
			FirstPlaceProbability: probabilities[0],
			ExpectedPlace:         float64(participant.Place),
		}
	}
	return prediction
}

func reversedPrediction(result rating.MatchResult, modelVersion string) rating.RoomPrediction {
	prediction := rating.RoomPrediction{
		RoomID:       result.RoomID,
		ModeID:       result.ModeID,
		ModelVersion: modelVersion,
		GeneratedAt:  result.FinishedAt.Add(-time.Minute),
		Participants: make([]rating.PlacementPrediction, len(result.Participants)),
	}
	roomSize := len(result.Participants)
	for index, participant := range result.Participants {
		predictedPlace := roomSize - participant.Place + 1
		probabilities := make([]float64, roomSize)
		probabilities[predictedPlace-1] = 1
		prediction.Participants[index] = rating.PlacementPrediction{
			PlayerID:              participant.PlayerID,
			PlaceProbabilities:    probabilities,
			FirstPlaceProbability: probabilities[0],
			ExpectedPlace:         float64(predictedPlace),
		}
	}
	return prediction
}
