package rating

import (
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	predictionTolerance   = 1e-9
	minimumLogProbability = 1e-15
)

// CalibrationObservation joins a pre-game prediction to a later verified result.
type CalibrationObservation struct {
	Prediction RoomPrediction `json:"prediction"`
	Result     MatchResult    `json:"result"`
}

// CalibrationBin aggregates predicted and observed placement frequencies.
type CalibrationBin struct {
	LowerBound        float64 `json:"lower_bound"`
	UpperBound        float64 `json:"upper_bound"`
	Count             int     `json:"count"`
	MeanPredicted     float64 `json:"mean_predicted"`
	ObservedFrequency float64 `json:"observed_frequency"`
}

// CalibrationReport contains proper scoring rules and reliability bins for one
// homogeneous mode, room size, scoring-rules version and rating-model version.
type CalibrationReport struct {
	ModelVersion             string           `json:"model_version"`
	ModeID                   string           `json:"mode_id"`
	ScoringRulesVersion      string           `json:"scoring_rules_version"`
	RoomSize                 int              `json:"room_size"`
	Rooms                    int              `json:"rooms"`
	Players                  int              `json:"players"`
	MulticlassBrierScore     float64          `json:"multiclass_brier_score"`
	MeanLogLoss              float64          `json:"mean_log_loss"`
	ExpectedCalibrationError float64          `json:"expected_calibration_error"`
	Bins                     []CalibrationBin `json:"bins"`
}

// HoldoutCalibrationConfig records the boundary used to train a frozen model.
type HoldoutCalibrationConfig struct {
	TrainingCutoff      time.Time `json:"training_cutoff"`
	ModelTrainedThrough time.Time `json:"model_trained_through"`
	BinCount            int       `json:"bin_count"`
}

// HoldoutCalibrationReport keeps the anti-leakage boundary beside its metrics.
type HoldoutCalibrationReport struct {
	TrainingCutoff      time.Time         `json:"training_cutoff"`
	ModelTrainedThrough time.Time         `json:"model_trained_through"`
	Calibration         CalibrationReport `json:"calibration"`
}

// EvaluateHoldoutCalibration evaluates only results that became available after
// the training cutoff and rejects a model trained beyond that boundary.
func EvaluateHoldoutCalibration(observations []CalibrationObservation, config HoldoutCalibrationConfig) (HoldoutCalibrationReport, error) {
	if config.TrainingCutoff.IsZero() || config.ModelTrainedThrough.IsZero() {
		return HoldoutCalibrationReport{}, errors.New("holdout training cutoff and model training time are required")
	}
	if config.ModelTrainedThrough.After(config.TrainingCutoff) {
		return HoldoutCalibrationReport{}, errors.New("holdout model cannot be trained beyond the training cutoff")
	}
	for index, observation := range observations {
		if !observation.Result.AvailableAt.After(config.TrainingCutoff) {
			return HoldoutCalibrationReport{}, fmt.Errorf("holdout observation %d was available by the training cutoff", index)
		}
		if observation.Prediction.GeneratedAt.Before(config.ModelTrainedThrough) {
			return HoldoutCalibrationReport{}, fmt.Errorf("holdout observation %d prediction predates the model training horizon", index)
		}
	}

	report, err := EvaluateCalibration(observations, config.BinCount)
	if err != nil {
		return HoldoutCalibrationReport{}, err
	}

	return HoldoutCalibrationReport{
		TrainingCutoff:      config.TrainingCutoff,
		ModelTrainedThrough: config.ModelTrainedThrough,
		Calibration:         report,
	}, nil
}

// EvaluateCalibration scores placement distributions against later outcomes.
// Mixed segments are rejected so an aggregate cannot hide a weak model slice.
func EvaluateCalibration(observations []CalibrationObservation, binCount int) (CalibrationReport, error) {
	if len(observations) == 0 {
		return CalibrationReport{}, errors.New("calibration requires at least one observation")
	}
	if binCount < 2 || binCount > 100 {
		return CalibrationReport{}, errors.New("calibration bin count must be between 2 and 100")
	}

	first := observations[0]
	report := CalibrationReport{
		ModelVersion:        first.Prediction.ModelVersion,
		ModeID:              first.Result.ModeID,
		ScoringRulesVersion: first.Result.ScoringRulesVersion,
		RoomSize:            len(first.Result.Participants),
		Rooms:               len(observations),
		Bins:                make([]CalibrationBin, binCount),
	}
	predictedSums := make([]float64, binCount)
	observedSums := make([]float64, binCount)
	for index := range report.Bins {
		report.Bins[index].LowerBound = float64(index) / float64(binCount)
		report.Bins[index].UpperBound = float64(index+1) / float64(binCount)
	}

	brierTotal := 0.0
	logLossTotal := 0.0
	for observationIndex, observation := range observations {
		if err := validateCalibrationSegment(report, observation); err != nil {
			return CalibrationReport{}, fmt.Errorf("calibration observation %d: %w", observationIndex, err)
		}
		places := make(map[string]int, len(observation.Result.Participants))
		for _, participant := range observation.Result.Participants {
			places[participant.PlayerID] = participant.Place
		}
		seen := make(map[string]struct{}, len(observation.Prediction.Participants))
		for _, participant := range observation.Prediction.Participants {
			actualPlace, exists := places[participant.PlayerID]
			if !exists {
				return CalibrationReport{}, fmt.Errorf("calibration observation %d: prediction contains unknown player %q", observationIndex, participant.PlayerID)
			}
			if _, duplicate := seen[participant.PlayerID]; duplicate {
				return CalibrationReport{}, fmt.Errorf("calibration observation %d: prediction contains duplicate player %q", observationIndex, participant.PlayerID)
			}
			seen[participant.PlayerID] = struct{}{}
			if err := validatePlacementPrediction(participant, report.RoomSize); err != nil {
				return CalibrationReport{}, fmt.Errorf("calibration observation %d: player %q: %w", observationIndex, participant.PlayerID, err)
			}

			for placeIndex, probability := range participant.PlaceProbabilities {
				observed := 0.0
				if placeIndex+1 == actualPlace {
					observed = 1
				}
				difference := probability - observed
				brierTotal += difference * difference
				binIndex := min(int(probability*float64(binCount)), binCount-1)
				report.Bins[binIndex].Count++
				predictedSums[binIndex] += probability
				observedSums[binIndex] += observed
			}
			logLossTotal -= math.Log(math.Max(participant.PlaceProbabilities[actualPlace-1], minimumLogProbability))
			report.Players++
		}
		if len(seen) != len(places) {
			return CalibrationReport{}, fmt.Errorf("calibration observation %d: prediction is missing result participants", observationIndex)
		}
	}

	report.MulticlassBrierScore = brierTotal / float64(report.Players)
	report.MeanLogLoss = logLossTotal / float64(report.Players)
	totalCells := report.Players * report.RoomSize
	for index := range report.Bins {
		count := report.Bins[index].Count
		if count == 0 {
			continue
		}
		report.Bins[index].MeanPredicted = predictedSums[index] / float64(count)
		report.Bins[index].ObservedFrequency = observedSums[index] / float64(count)
		report.ExpectedCalibrationError += float64(count) / float64(totalCells) *
			math.Abs(report.Bins[index].MeanPredicted-report.Bins[index].ObservedFrequency)
	}
	return report, nil
}

func validateCalibrationSegment(report CalibrationReport, observation CalibrationObservation) error {
	if err := observation.Result.Validate(); err != nil {
		return err
	}
	prediction := observation.Prediction
	if prediction.RoomID != observation.Result.RoomID || prediction.ModeID != observation.Result.ModeID {
		return errors.New("prediction room and mode must match the result")
	}
	if prediction.GeneratedAt.IsZero() || prediction.GeneratedAt.After(observation.Result.FinishedAt) {
		return errors.New("prediction must be generated before the result finishes")
	}
	if prediction.ModelVersion == "" || prediction.ModelVersion != report.ModelVersion ||
		observation.Result.ModeID != report.ModeID || observation.Result.ScoringRulesVersion != report.ScoringRulesVersion ||
		len(observation.Result.Participants) != report.RoomSize {
		return errors.New("calibration observations must belong to one model, mode, rules version and room size")
	}
	if len(prediction.Participants) != report.RoomSize {
		return errors.New("prediction participant count must match the result")
	}
	return nil
}

func validatePlacementPrediction(prediction PlacementPrediction, roomSize int) error {
	if prediction.PlayerID == "" || len(prediction.PlaceProbabilities) != roomSize {
		return errors.New("player id and one probability per place are required")
	}
	sum := 0.0
	expectedPlace := 0.0
	for placeIndex, probability := range prediction.PlaceProbabilities {
		if !finite(probability) || probability < 0 || probability > 1 {
			return errors.New("place probabilities must be finite and between zero and one")
		}
		sum += probability
		expectedPlace += float64(placeIndex+1) * probability
	}
	if math.Abs(sum-1) > predictionTolerance {
		return errors.New("place probabilities must sum to one")
	}
	if !finite(prediction.ExpectedPlace) || math.Abs(expectedPlace-prediction.ExpectedPlace) > predictionTolerance {
		return errors.New("expected place must match the place distribution")
	}
	if !finite(prediction.FirstPlaceProbability) || math.Abs(prediction.PlaceProbabilities[0]-prediction.FirstPlaceProbability) > predictionTolerance {
		return errors.New("first-place probability must match the place distribution")
	}
	return nil
}
