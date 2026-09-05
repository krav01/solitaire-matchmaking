package rating_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestEvaluateCalibrationScoresUniformPrediction(t *testing.T) {
	t.Parallel()
	model := newBaseline(t)
	result, estimates, _ := orderedResult(t, model, 5)
	prediction, err := model.Predict(predictionRequest(result, estimates))
	if err != nil {
		t.Fatalf("Predict() error = %v", err)
	}

	report, err := rating.EvaluateCalibration([]rating.CalibrationObservation{{Prediction: prediction, Result: result}}, 10)
	if err != nil {
		t.Fatalf("EvaluateCalibration() error = %v", err)
	}
	if report.Rooms != 1 || report.Players != 5 || report.RoomSize != 5 || report.ModelVersion != "rating-v1" {
		t.Fatalf("unexpected report metadata: %+v", report)
	}
	if !closeTo(report.MulticlassBrierScore, 0.8) {
		t.Errorf("Brier score = %f, want 0.8", report.MulticlassBrierScore)
	}
	if !closeTo(report.MeanLogLoss, math.Log(5)) {
		t.Errorf("log loss = %f, want %f", report.MeanLogLoss, math.Log(5))
	}
	if !closeTo(report.ExpectedCalibrationError, 0) {
		t.Errorf("calibration error = %f, want 0", report.ExpectedCalibrationError)
	}
}

func TestEvaluateCalibrationScoresPerfectPrediction(t *testing.T) {
	t.Parallel()
	model := newBaseline(t)
	result, _, _ := orderedResult(t, model, 5)
	prediction := rating.RoomPrediction{
		RoomID:       result.RoomID,
		ModeID:       result.ModeID,
		ModelVersion: "rating-v1",
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

	report, err := rating.EvaluateCalibration([]rating.CalibrationObservation{{Prediction: prediction, Result: result}}, 10)
	if err != nil {
		t.Fatalf("EvaluateCalibration() error = %v", err)
	}
	if report.MulticlassBrierScore != 0 || report.MeanLogLoss != 0 || report.ExpectedCalibrationError != 0 {
		t.Fatalf("perfect prediction received non-zero errors: %+v", report)
	}
}

func TestEvaluateCalibrationRejectsLeakageAndMixedSegments(t *testing.T) {
	t.Parallel()
	model := newBaseline(t)
	result, estimates, _ := orderedResult(t, model, 5)
	prediction, err := model.Predict(predictionRequest(result, estimates))
	if err != nil {
		t.Fatalf("Predict() error = %v", err)
	}

	tests := []struct {
		name         string
		observations []rating.CalibrationObservation
	}{
		{
			name: "prediction after finish",
			observations: []rating.CalibrationObservation{{
				Prediction: func() rating.RoomPrediction {
					value := prediction
					value.GeneratedAt = result.FinishedAt.Add(time.Nanosecond)
					return value
				}(),
				Result: result,
			}},
		},
		{
			name: "mixed modes",
			observations: []rating.CalibrationObservation{
				{Prediction: prediction, Result: result},
				{Prediction: func() rating.RoomPrediction {
					value := prediction
					value.ModeID = "draw-three"
					return value
				}(), Result: func() rating.MatchResult {
					value := result
					value.ModeID = "draw-three"
					value.EventID = "event-2"
					value.RoomID = result.RoomID
					return value
				}()},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := rating.EvaluateCalibration(test.observations, 10); err == nil {
				t.Fatal("EvaluateCalibration() error = nil, want error")
			}
		})
	}
}

func TestEvaluateHoldoutCalibrationRecordsTrainingBoundary(t *testing.T) {
	t.Parallel()

	model := newBaseline(t)
	result, estimates, _ := orderedResult(t, model, 5)
	prediction, err := model.Predict(predictionRequest(result, estimates))
	if err != nil {
		t.Fatalf("Predict() error = %v", err)
	}
	cutoff := result.AvailableAt.Add(-time.Hour)
	trainedThrough := cutoff.Add(-time.Minute)

	report, err := rating.EvaluateHoldoutCalibration(
		[]rating.CalibrationObservation{{Prediction: prediction, Result: result}},
		rating.HoldoutCalibrationConfig{
			TrainingCutoff:      cutoff,
			ModelTrainedThrough: trainedThrough,
			BinCount:            10,
		},
	)
	if err != nil {
		t.Fatalf("EvaluateHoldoutCalibration() error = %v", err)
	}
	if report.TrainingCutoff != cutoff || report.ModelTrainedThrough != trainedThrough || report.Calibration.Rooms != 1 {
		t.Fatalf("holdout report = %+v", report)
	}
}

func TestEvaluateHoldoutCalibrationRejectsLeakage(t *testing.T) {
	t.Parallel()

	model := newBaseline(t)
	result, estimates, _ := orderedResult(t, model, 5)
	prediction, err := model.Predict(predictionRequest(result, estimates))
	if err != nil {
		t.Fatalf("Predict() error = %v", err)
	}
	cutoff := result.AvailableAt.Add(-time.Hour)
	observation := rating.CalibrationObservation{Prediction: prediction, Result: result}

	tests := []struct {
		name        string
		observation rating.CalibrationObservation
		config      rating.HoldoutCalibrationConfig
		wantError   string
	}{
		{
			name:        "model trained after cutoff",
			observation: observation,
			config: rating.HoldoutCalibrationConfig{
				TrainingCutoff:      cutoff,
				ModelTrainedThrough: cutoff.Add(time.Nanosecond),
				BinCount:            10,
			},
			wantError: "trained beyond",
		},
		{
			name: "result available by cutoff",
			observation: func() rating.CalibrationObservation {
				value := observation
				value.Result.AvailableAt = cutoff
				value.Result.FinishedAt = cutoff
				return value
			}(),
			config: rating.HoldoutCalibrationConfig{
				TrainingCutoff:      cutoff,
				ModelTrainedThrough: cutoff,
				BinCount:            10,
			},
			wantError: "available by",
		},
		{
			name:        "missing cutoff",
			observation: observation,
			config: rating.HoldoutCalibrationConfig{
				ModelTrainedThrough: cutoff,
				BinCount:            10,
			},
			wantError: "cutoff",
		},
		{
			name: "prediction before model training horizon",
			observation: func() rating.CalibrationObservation {
				value := observation
				value.Prediction.GeneratedAt = cutoff.Add(-time.Nanosecond)
				return value
			}(),
			config: rating.HoldoutCalibrationConfig{
				TrainingCutoff:      cutoff,
				ModelTrainedThrough: cutoff,
				BinCount:            10,
			},
			wantError: "predates",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := rating.EvaluateHoldoutCalibration([]rating.CalibrationObservation{tt.observation}, tt.config)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("EvaluateHoldoutCalibration() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}
