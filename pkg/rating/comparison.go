package rating

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

const comparisonTolerance = 1e-12

// PairedCalibrationObservation evaluates two model predictions against exactly
// the same verified result. SegmentID is an externally defined stable segment,
// for example a mode, entry-fee and region combination.
type PairedCalibrationObservation struct {
	SegmentID           string         `json:"segment_id"`
	BaselinePrediction  RoomPrediction `json:"baseline_prediction"`
	CandidatePrediction RoomPrediction `json:"candidate_prediction"`
	Result              MatchResult    `json:"result"`
}

// ModelComparisonPolicy defines evidence required before a candidate can be
// activated. Thresholds are explicit because production values require data.
type ModelComparisonPolicy struct {
	MinimumRoomsPerSegment              int     `json:"minimum_rooms_per_segment"`
	MinimumOverallBrierImprovement      float64 `json:"minimum_overall_brier_improvement"`
	MaximumSegmentBrierRegression       float64 `json:"maximum_segment_brier_regression"`
	MaximumSegmentLogLossRegression     float64 `json:"maximum_segment_log_loss_regression"`
	MaximumSegmentCalibrationRegression float64 `json:"maximum_segment_calibration_regression"`
}

// ModelComparisonConfig binds both frozen model horizons to one holdout cutoff.
type ModelComparisonConfig struct {
	TrainingCutoff          time.Time             `json:"training_cutoff"`
	BaselineTrainedThrough  time.Time             `json:"baseline_trained_through"`
	CandidateTrainedThrough time.Time             `json:"candidate_trained_through"`
	BinCount                int                   `json:"bin_count"`
	Policy                  ModelComparisonPolicy `json:"policy"`
}

// ComparisonMetrics contains the three holdout metrics used by the policy.
type ComparisonMetrics struct {
	MulticlassBrierScore     float64 `json:"multiclass_brier_score"`
	MeanLogLoss              float64 `json:"mean_log_loss"`
	ExpectedCalibrationError float64 `json:"expected_calibration_error"`
}

// SegmentComparison reports candidate-minus-baseline deltas. Negative values
// are improvements.
type SegmentComparison struct {
	SegmentID              string            `json:"segment_id"`
	ModeID                 string            `json:"mode_id"`
	ScoringRulesVersion    string            `json:"scoring_rules_version"`
	RoomSize               int               `json:"room_size"`
	Rooms                  int               `json:"rooms"`
	Players                int               `json:"players"`
	Baseline               CalibrationReport `json:"baseline"`
	Candidate              CalibrationReport `json:"candidate"`
	CandidateMinusBaseline ComparisonMetrics `json:"candidate_minus_baseline"`
	Passed                 bool              `json:"passed"`
	Reasons                []string          `json:"reasons,omitempty"`
}

// ModelComparisonReport is the evidence consumed by model activation.
type ModelComparisonReport struct {
	BaselineVersion         string                `json:"baseline_version"`
	CandidateVersion        string                `json:"candidate_version"`
	TrainingCutoff          time.Time             `json:"training_cutoff"`
	BaselineTrainedThrough  time.Time             `json:"baseline_trained_through"`
	CandidateTrainedThrough time.Time             `json:"candidate_trained_through"`
	HoldoutAvailableThrough time.Time             `json:"holdout_available_through"`
	Rooms                   int                   `json:"rooms"`
	Players                 int                   `json:"players"`
	Baseline                ComparisonMetrics     `json:"baseline"`
	Candidate               ComparisonMetrics     `json:"candidate"`
	CandidateMinusBaseline  ComparisonMetrics     `json:"candidate_minus_baseline"`
	Policy                  ModelComparisonPolicy `json:"policy"`
	Segments                []SegmentComparison   `json:"segments"`
	Eligible                bool                  `json:"eligible"`
	Reasons                 []string              `json:"reasons,omitempty"`
}

// CompareHoldoutModels evaluates paired predictions in homogeneous segments and
// applies the policy without turning a failed threshold into a processing error.
func CompareHoldoutModels(observations []PairedCalibrationObservation, config ModelComparisonConfig) (ModelComparisonReport, error) {
	if len(observations) == 0 {
		return ModelComparisonReport{}, errors.New("model comparison requires paired holdout observations")
	}
	if err := validateModelComparisonPolicy(config.Policy); err != nil {
		return ModelComparisonReport{}, err
	}

	type segmentInput struct {
		modeID              string
		scoringRulesVersion string
		roomSize            int
		baseline            []CalibrationObservation
		candidate           []CalibrationObservation
	}
	segments := make(map[string]*segmentInput)
	eventIDs := make(map[string]struct{}, len(observations))
	baselineVersion := ""
	candidateVersion := ""
	for index, observation := range observations {
		if observation.SegmentID == "" {
			return ModelComparisonReport{}, fmt.Errorf("paired observation %d requires a segment id", index)
		}
		if _, exists := eventIDs[observation.Result.EventID]; exists {
			return ModelComparisonReport{}, fmt.Errorf("model comparison contains duplicate event %q", observation.Result.EventID)
		}
		eventIDs[observation.Result.EventID] = struct{}{}

		if baselineVersion == "" {
			baselineVersion = observation.BaselinePrediction.ModelVersion
			candidateVersion = observation.CandidatePrediction.ModelVersion
		}
		if observation.BaselinePrediction.ModelVersion != baselineVersion ||
			observation.CandidatePrediction.ModelVersion != candidateVersion {
			return ModelComparisonReport{}, errors.New("model comparison requires one baseline and one candidate version")
		}

		roomSize := len(observation.Result.Participants)
		segment, exists := segments[observation.SegmentID]
		if !exists {
			segment = &segmentInput{
				modeID:              observation.Result.ModeID,
				scoringRulesVersion: observation.Result.ScoringRulesVersion,
				roomSize:            roomSize,
			}
			segments[observation.SegmentID] = segment
		} else if segment.modeID != observation.Result.ModeID ||
			segment.scoringRulesVersion != observation.Result.ScoringRulesVersion || segment.roomSize != roomSize {
			return ModelComparisonReport{}, fmt.Errorf("segment %q mixes modes, scoring rules or room sizes", observation.SegmentID)
		}
		segment.baseline = append(segment.baseline, CalibrationObservation{
			Prediction: observation.BaselinePrediction,
			Result:     observation.Result,
		})
		segment.candidate = append(segment.candidate, CalibrationObservation{
			Prediction: observation.CandidatePrediction,
			Result:     observation.Result,
		})
	}
	if baselineVersion == "" || candidateVersion == "" || baselineVersion == candidateVersion {
		return ModelComparisonReport{}, errors.New("model comparison requires distinct non-empty baseline and candidate versions")
	}

	segmentIDs := make([]string, 0, len(segments))
	for segmentID := range segments {
		segmentIDs = append(segmentIDs, segmentID)
	}
	slices.Sort(segmentIDs)

	report := ModelComparisonReport{
		BaselineVersion:         baselineVersion,
		CandidateVersion:        candidateVersion,
		TrainingCutoff:          config.TrainingCutoff,
		BaselineTrainedThrough:  config.BaselineTrainedThrough,
		CandidateTrainedThrough: config.CandidateTrainedThrough,
		Policy:                  config.Policy,
		Eligible:                true,
		Segments:                make([]SegmentComparison, 0, len(segmentIDs)),
	}
	for _, observation := range observations {
		if observation.Result.AvailableAt.After(report.HoldoutAvailableThrough) {
			report.HoldoutAvailableThrough = observation.Result.AvailableAt
		}
	}
	baselineTotals := metricTotals{}
	candidateTotals := metricTotals{}
	for _, segmentID := range segmentIDs {
		input := segments[segmentID]
		slices.SortFunc(input.baseline, compareCalibrationObservation)
		slices.SortFunc(input.candidate, compareCalibrationObservation)
		baselineHoldout, err := EvaluateHoldoutCalibration(input.baseline, HoldoutCalibrationConfig{
			TrainingCutoff:      config.TrainingCutoff,
			ModelTrainedThrough: config.BaselineTrainedThrough,
			BinCount:            config.BinCount,
		})
		if err != nil {
			return ModelComparisonReport{}, fmt.Errorf("baseline segment %q: %w", segmentID, err)
		}
		candidateHoldout, err := EvaluateHoldoutCalibration(input.candidate, HoldoutCalibrationConfig{
			TrainingCutoff:      config.TrainingCutoff,
			ModelTrainedThrough: config.CandidateTrainedThrough,
			BinCount:            config.BinCount,
		})
		if err != nil {
			return ModelComparisonReport{}, fmt.Errorf("candidate segment %q: %w", segmentID, err)
		}

		segmentReport := compareSegment(segmentID, baselineHoldout.Calibration, candidateHoldout.Calibration, config.Policy)
		report.Segments = append(report.Segments, segmentReport)
		report.Rooms += segmentReport.Rooms
		report.Players += segmentReport.Players
		baselineTotals.add(segmentReport.Baseline)
		candidateTotals.add(segmentReport.Candidate)
		if !segmentReport.Passed {
			report.Eligible = false
			for _, reason := range segmentReport.Reasons {
				report.Reasons = append(report.Reasons, fmt.Sprintf("segment %q: %s", segmentID, reason))
			}
		}
	}

	report.Baseline = baselineTotals.metrics()
	report.Candidate = candidateTotals.metrics()
	report.CandidateMinusBaseline = subtractMetrics(report.Candidate, report.Baseline)
	overallImprovement := report.Baseline.MulticlassBrierScore - report.Candidate.MulticlassBrierScore
	if overallImprovement+comparisonTolerance < config.Policy.MinimumOverallBrierImprovement {
		report.Eligible = false
		report.Reasons = append(report.Reasons, fmt.Sprintf(
			"overall Brier improvement %.6f is below required %.6f",
			overallImprovement,
			config.Policy.MinimumOverallBrierImprovement,
		))
	}

	return report, nil
}

// ValidateForActivation recomputes policy decisions from segment reports so a
// stale or internally inconsistent report cannot promote a model.
func (r ModelComparisonReport) ValidateForActivation() error {
	if !r.Eligible || len(r.Reasons) != 0 {
		return errors.New("model comparison is not eligible for activation")
	}
	if r.BaselineVersion == "" || r.CandidateVersion == "" || r.BaselineVersion == r.CandidateVersion {
		return errors.New("model comparison requires distinct baseline and candidate versions")
	}
	if r.TrainingCutoff.IsZero() || r.BaselineTrainedThrough.IsZero() || r.CandidateTrainedThrough.IsZero() ||
		r.BaselineTrainedThrough.After(r.TrainingCutoff) || r.CandidateTrainedThrough.After(r.TrainingCutoff) {
		return errors.New("model comparison contains an invalid training horizon")
	}
	if r.HoldoutAvailableThrough.IsZero() || !r.HoldoutAvailableThrough.After(r.TrainingCutoff) {
		return errors.New("model comparison requires holdout data after the training cutoff")
	}
	if err := validateModelComparisonPolicy(r.Policy); err != nil {
		return err
	}
	if len(r.Segments) == 0 {
		return errors.New("model comparison requires segment reports")
	}

	segmentIDs := make(map[string]struct{}, len(r.Segments))
	baselineTotals := metricTotals{}
	candidateTotals := metricTotals{}
	rooms := 0
	players := 0
	for _, segment := range r.Segments {
		if segment.SegmentID == "" {
			return errors.New("model comparison segment id is required")
		}
		if _, exists := segmentIDs[segment.SegmentID]; exists {
			return fmt.Errorf("model comparison contains duplicate segment %q", segment.SegmentID)
		}
		segmentIDs[segment.SegmentID] = struct{}{}
		if segment.Baseline.ModelVersion != r.BaselineVersion || segment.Candidate.ModelVersion != r.CandidateVersion ||
			segment.Baseline.ModeID != segment.Candidate.ModeID ||
			segment.Baseline.ScoringRulesVersion != segment.Candidate.ScoringRulesVersion ||
			segment.Baseline.RoomSize != segment.Candidate.RoomSize ||
			segment.Baseline.Rooms != segment.Candidate.Rooms || segment.Baseline.Players != segment.Candidate.Players {
			return fmt.Errorf("model comparison segment %q is inconsistent", segment.SegmentID)
		}
		recomputed := compareSegment(segment.SegmentID, segment.Baseline, segment.Candidate, r.Policy)
		if !recomputed.Passed || !segment.Passed || len(segment.Reasons) != 0 {
			return fmt.Errorf("model comparison segment %q fails the activation policy", segment.SegmentID)
		}
		if segment.ModeID != recomputed.ModeID || segment.ScoringRulesVersion != recomputed.ScoringRulesVersion ||
			segment.RoomSize != recomputed.RoomSize || segment.Rooms != recomputed.Rooms || segment.Players != recomputed.Players ||
			!metricsClose(segment.CandidateMinusBaseline, recomputed.CandidateMinusBaseline) {
			return fmt.Errorf("model comparison segment %q summary is inconsistent", segment.SegmentID)
		}
		rooms += segment.Baseline.Rooms
		players += segment.Baseline.Players
		baselineTotals.add(segment.Baseline)
		candidateTotals.add(segment.Candidate)
	}

	baseline := baselineTotals.metrics()
	candidate := candidateTotals.metrics()
	improvement := baseline.MulticlassBrierScore - candidate.MulticlassBrierScore
	if improvement+comparisonTolerance < r.Policy.MinimumOverallBrierImprovement {
		return errors.New("model comparison overall Brier improvement is below policy")
	}
	if r.Rooms != rooms || r.Players != players || !metricsClose(r.Baseline, baseline) ||
		!metricsClose(r.Candidate, candidate) || !metricsClose(r.CandidateMinusBaseline, subtractMetrics(candidate, baseline)) {
		return errors.New("model comparison aggregate metrics are inconsistent")
	}

	return nil
}

func validateModelComparisonPolicy(policy ModelComparisonPolicy) error {
	if policy.MinimumRoomsPerSegment < 1 {
		return errors.New("comparison policy requires at least one room per segment")
	}
	values := []float64{
		policy.MinimumOverallBrierImprovement,
		policy.MaximumSegmentBrierRegression,
		policy.MaximumSegmentLogLossRegression,
		policy.MaximumSegmentCalibrationRegression,
	}
	for _, value := range values {
		if !finite(value) || value < 0 {
			return errors.New("comparison policy thresholds must be finite and non-negative")
		}
	}

	return nil
}

func compareSegment(segmentID string, baseline, candidate CalibrationReport, policy ModelComparisonPolicy) SegmentComparison {
	delta := subtractMetrics(calibrationMetrics(candidate), calibrationMetrics(baseline))
	report := SegmentComparison{
		SegmentID:              segmentID,
		ModeID:                 baseline.ModeID,
		ScoringRulesVersion:    baseline.ScoringRulesVersion,
		RoomSize:               baseline.RoomSize,
		Rooms:                  baseline.Rooms,
		Players:                baseline.Players,
		Baseline:               baseline,
		Candidate:              candidate,
		CandidateMinusBaseline: delta,
		Passed:                 true,
	}
	if report.Rooms < policy.MinimumRoomsPerSegment {
		report.Passed = false
		report.Reasons = append(report.Reasons, fmt.Sprintf(
			"rooms %d are below required %d",
			report.Rooms,
			policy.MinimumRoomsPerSegment,
		))
	}
	thresholds := []struct {
		name    string
		value   float64
		maximum float64
	}{
		{name: "Brier regression", value: delta.MulticlassBrierScore, maximum: policy.MaximumSegmentBrierRegression},
		{name: "log-loss regression", value: delta.MeanLogLoss, maximum: policy.MaximumSegmentLogLossRegression},
		{name: "calibration regression", value: delta.ExpectedCalibrationError, maximum: policy.MaximumSegmentCalibrationRegression},
	}
	for _, threshold := range thresholds {
		if threshold.value > threshold.maximum+comparisonTolerance {
			report.Passed = false
			report.Reasons = append(report.Reasons, fmt.Sprintf(
				"%s %.6f exceeds allowed %.6f",
				threshold.name,
				threshold.value,
				threshold.maximum,
			))
		}
	}

	return report
}

type metricTotals struct {
	brierWeighted       float64
	logLossWeighted     float64
	calibrationWeighted float64
	players             float64
	cells               float64
}

func (t *metricTotals) add(report CalibrationReport) {
	players := float64(report.Players)
	cells := players * float64(report.RoomSize)
	t.brierWeighted += report.MulticlassBrierScore * players
	t.logLossWeighted += report.MeanLogLoss * players
	t.calibrationWeighted += report.ExpectedCalibrationError * cells
	t.players += players
	t.cells += cells
}

func (t metricTotals) metrics() ComparisonMetrics {
	return ComparisonMetrics{
		MulticlassBrierScore:     t.brierWeighted / t.players,
		MeanLogLoss:              t.logLossWeighted / t.players,
		ExpectedCalibrationError: t.calibrationWeighted / t.cells,
	}
}

func calibrationMetrics(report CalibrationReport) ComparisonMetrics {
	return ComparisonMetrics{
		MulticlassBrierScore:     report.MulticlassBrierScore,
		MeanLogLoss:              report.MeanLogLoss,
		ExpectedCalibrationError: report.ExpectedCalibrationError,
	}
}

func subtractMetrics(left, right ComparisonMetrics) ComparisonMetrics {
	return ComparisonMetrics{
		MulticlassBrierScore:     left.MulticlassBrierScore - right.MulticlassBrierScore,
		MeanLogLoss:              left.MeanLogLoss - right.MeanLogLoss,
		ExpectedCalibrationError: left.ExpectedCalibrationError - right.ExpectedCalibrationError,
	}
}

func compareCalibrationObservation(left, right CalibrationObservation) int {
	if comparison := left.Result.AvailableAt.Compare(right.Result.AvailableAt); comparison != 0 {
		return comparison
	}
	if left.Result.EventID < right.Result.EventID {
		return -1
	}
	if left.Result.EventID > right.Result.EventID {
		return 1
	}
	return 0
}

func metricsClose(left, right ComparisonMetrics) bool {
	return math.Abs(left.MulticlassBrierScore-right.MulticlassBrierScore) <= comparisonTolerance &&
		math.Abs(left.MeanLogLoss-right.MeanLogLoss) <= comparisonTolerance &&
		math.Abs(left.ExpectedCalibrationError-right.ExpectedCalibrationError) <= comparisonTolerance
}
