package rating_test

import (
	"math"
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
