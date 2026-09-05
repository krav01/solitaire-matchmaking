package rating

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// TimeOrderedFeatureSet separates results by when their data became available.
// Its private partitions prevent a standardizer from accidentally fitting on
// holdout observations.
type TimeOrderedFeatureSet struct {
	trainingCutoff      time.Time
	schemaVersion       string
	modeID              string
	scoringRulesVersion string
	deckVersion         string
	featureNames        []FeatureName
	training            []FeatureBatch
	holdout             []FeatureBatch
}

// SplitFeatureBatches places data available at or before the cutoff in training
// and strictly later data in holdout. Inputs are cloned and returned in stable
// availability/event order.
func SplitFeatureBatches(batches []FeatureBatch, trainingCutoff time.Time) (*TimeOrderedFeatureSet, error) {
	if trainingCutoff.IsZero() {
		return nil, errors.New("feature training cutoff is required")
	}
	if len(batches) < 2 {
		return nil, errors.New("feature split requires at least two batches")
	}

	canonical := make([]FeatureBatch, 0, len(batches))
	eventIDs := make(map[string]struct{}, len(batches))
	roomIDs := make(map[string]struct{}, len(batches))
	var featureNames []FeatureName
	var reference FeatureBatch
	for index, batch := range batches {
		validated, names, err := canonicalFeatureBatch(batch)
		if err != nil {
			return nil, fmt.Errorf("feature batch %d: %w", index, err)
		}
		if _, exists := eventIDs[validated.EventID]; exists {
			return nil, fmt.Errorf("feature split contains duplicate event %q", validated.EventID)
		}
		eventIDs[validated.EventID] = struct{}{}
		if _, exists := roomIDs[validated.RoomID]; exists {
			return nil, fmt.Errorf("feature split contains duplicate room %q", validated.RoomID)
		}
		roomIDs[validated.RoomID] = struct{}{}

		if index == 0 {
			reference = validated
			featureNames = names
		} else if err := validateFeatureBatchCompatibility(reference, featureNames, validated, names); err != nil {
			return nil, fmt.Errorf("feature batch %d: %w", index, err)
		}
		canonical = append(canonical, validated)
	}

	slices.SortFunc(canonical, compareFeatureBatch)
	set := &TimeOrderedFeatureSet{
		trainingCutoff:      trainingCutoff,
		schemaVersion:       reference.SchemaVersion,
		modeID:              reference.ModeID,
		scoringRulesVersion: reference.ScoringRulesVersion,
		deckVersion:         reference.DeckVersion,
		featureNames:        slices.Clone(featureNames),
	}
	for _, batch := range canonical {
		if batch.AvailableAt.After(trainingCutoff) {
			set.holdout = append(set.holdout, batch)
		} else {
			set.training = append(set.training, batch)
		}
	}
	if len(set.training) == 0 || len(set.holdout) == 0 {
		return nil, errors.New("feature split requires non-empty training and holdout partitions")
	}

	return set, nil
}

func (s *TimeOrderedFeatureSet) TrainingCutoff() time.Time {
	return s.trainingCutoff
}

func (s *TimeOrderedFeatureSet) Training() []FeatureBatch {
	return cloneFeatureBatches(s.training)
}

func (s *TimeOrderedFeatureSet) Holdout() []FeatureBatch {
	return cloneFeatureBatches(s.holdout)
}

// FeatureStatistic contains training-only population statistics. A zero
// StandardDeviation marks a constant feature and is transformed with unit scale.
type FeatureStatistic struct {
	Name              FeatureName `json:"name"`
	Observations      int         `json:"observations"`
	Mean              float64     `json:"mean"`
	StandardDeviation float64     `json:"standard_deviation"`
}

// FeatureStandardizer owns statistics fitted only from a set's private training
// partition. Missing observations never contribute to those statistics.
type FeatureStandardizer struct {
	trainedThrough      time.Time
	schemaVersion       string
	modeID              string
	scoringRulesVersion string
	deckVersion         string
	statistics          []FeatureStatistic
}

func FitFeatureStandardizer(set *TimeOrderedFeatureSet) (*FeatureStandardizer, error) {
	if set == nil {
		return nil, errors.New("time-ordered feature set is required")
	}

	type accumulator struct {
		count int
		mean  float64
		m2    float64
	}
	accumulators := make([]accumulator, len(set.featureNames))
	for _, batch := range set.training {
		for _, player := range batch.Players {
			for index, observation := range player.Observations {
				if !observation.Present {
					continue
				}

				current := &accumulators[index]
				current.count++
				delta := observation.Value - current.mean
				current.mean += delta / float64(current.count)
				current.m2 += delta * (observation.Value - current.mean)
			}
		}
	}

	statistics := make([]FeatureStatistic, len(set.featureNames))
	for index, name := range set.featureNames {
		current := accumulators[index]
		if current.count == 0 {
			return nil, fmt.Errorf("feature %q has no present training observations", name)
		}
		standardDeviation := math.Sqrt(math.Max(current.m2/float64(current.count), 0))
		if !finite(current.mean) || !finite(standardDeviation) {
			return nil, fmt.Errorf("feature %q produced non-finite training statistics", name)
		}
		statistics[index] = FeatureStatistic{
			Name:              name,
			Observations:      current.count,
			Mean:              current.mean,
			StandardDeviation: standardDeviation,
		}
	}

	return &FeatureStandardizer{
		trainedThrough:      set.trainingCutoff,
		schemaVersion:       set.schemaVersion,
		modeID:              set.modeID,
		scoringRulesVersion: set.scoringRulesVersion,
		deckVersion:         set.deckVersion,
		statistics:          statistics,
	}, nil
}

func (s *FeatureStandardizer) TrainedThrough() time.Time {
	return s.trainedThrough
}

func (s *FeatureStandardizer) Statistics() []FeatureStatistic {
	return slices.Clone(s.statistics)
}

// StandardizedFeatureObservation preserves the raw observation's presence mask.
type StandardizedFeatureObservation struct {
	Name    FeatureName `json:"name"`
	Value   float64     `json:"value"`
	Present bool        `json:"present"`
}

type StandardizedPlayerFeatureVector struct {
	PlayerID     string                           `json:"player_id"`
	Observations []StandardizedFeatureObservation `json:"observations"`
}

// StandardizedFeatureBatch retains source metadata for replay and auditing.
type StandardizedFeatureBatch struct {
	SchemaVersion       string                            `json:"schema_version"`
	EventID             string                            `json:"event_id"`
	RoomID              string                            `json:"room_id"`
	ModeID              string                            `json:"mode_id"`
	ScoringRulesVersion string                            `json:"scoring_rules_version"`
	DeckVersion         string                            `json:"deck_version"`
	DeckID              string                            `json:"deck_id"`
	FinishedAt          time.Time                         `json:"finished_at"`
	AvailableAt         time.Time                         `json:"available_at"`
	Players             []StandardizedPlayerFeatureVector `json:"players"`
}

func (s *FeatureStandardizer) TransformTraining(set *TimeOrderedFeatureSet) ([]StandardizedFeatureBatch, error) {
	if err := s.validateSet(set); err != nil {
		return nil, err
	}

	return s.transformBatches(set.training), nil
}

func (s *FeatureStandardizer) TransformHoldout(set *TimeOrderedFeatureSet) ([]StandardizedFeatureBatch, error) {
	if err := s.validateSet(set); err != nil {
		return nil, err
	}

	return s.transformBatches(set.holdout), nil
}

func (s *FeatureStandardizer) validateSet(set *TimeOrderedFeatureSet) error {
	if s == nil || set == nil {
		return errors.New("feature standardizer and time-ordered set are required")
	}
	if s.trainedThrough != set.trainingCutoff || s.schemaVersion != set.schemaVersion ||
		s.modeID != set.modeID || s.scoringRulesVersion != set.scoringRulesVersion || s.deckVersion != set.deckVersion ||
		len(s.statistics) != len(set.featureNames) {
		return errors.New("feature standardizer does not match the time-ordered set")
	}
	for index, statistic := range s.statistics {
		if statistic.Name != set.featureNames[index] {
			return errors.New("feature standardizer layout does not match the time-ordered set")
		}
	}

	return nil
}

func (s *FeatureStandardizer) transformBatches(batches []FeatureBatch) []StandardizedFeatureBatch {
	transformed := make([]StandardizedFeatureBatch, 0, len(batches))
	for _, batch := range batches {
		players := make([]StandardizedPlayerFeatureVector, 0, len(batch.Players))
		for _, player := range batch.Players {
			observations := make([]StandardizedFeatureObservation, len(player.Observations))
			for index, observation := range player.Observations {
				standardized := StandardizedFeatureObservation{Name: observation.Name, Present: observation.Present}
				if observation.Present {
					statistic := s.statistics[index]
					scale := statistic.StandardDeviation
					if scale == 0 {
						scale = 1
					}
					standardized.Value = (observation.Value - statistic.Mean) / scale
				}
				observations[index] = standardized
			}
			players = append(players, StandardizedPlayerFeatureVector{
				PlayerID:     player.PlayerID,
				Observations: observations,
			})
		}
		transformed = append(transformed, StandardizedFeatureBatch{
			SchemaVersion:       batch.SchemaVersion,
			EventID:             batch.EventID,
			RoomID:              batch.RoomID,
			ModeID:              batch.ModeID,
			ScoringRulesVersion: batch.ScoringRulesVersion,
			DeckVersion:         batch.DeckVersion,
			DeckID:              batch.DeckID,
			FinishedAt:          batch.FinishedAt,
			AvailableAt:         batch.AvailableAt,
			Players:             players,
		})
	}

	return transformed
}

func canonicalFeatureBatch(batch FeatureBatch) (FeatureBatch, []FeatureName, error) {
	if batch.SchemaVersion == "" || batch.EventID == "" || batch.RoomID == "" || batch.ModeID == "" ||
		batch.ScoringRulesVersion == "" || batch.DeckVersion == "" || batch.DeckID == "" {
		return FeatureBatch{}, nil, errors.New("feature batch identifiers and versions are required")
	}
	if batch.FinishedAt.IsZero() || batch.AvailableAt.IsZero() || batch.AvailableAt.Before(batch.FinishedAt) {
		return FeatureBatch{}, nil, errors.New("feature batch requires valid finish and availability times")
	}
	if len(batch.Players) < MinRoomSize || len(batch.Players) > MaxRoomSize {
		return FeatureBatch{}, nil, fmt.Errorf("feature batch requires %d to %d players", MinRoomSize, MaxRoomSize)
	}

	canonical := cloneFeatureBatch(batch)
	slices.SortFunc(canonical.Players, func(left, right PlayerFeatureVector) int {
		if left.PlayerID < right.PlayerID {
			return -1
		}
		if left.PlayerID > right.PlayerID {
			return 1
		}
		return 0
	})

	featureNames := make([]FeatureName, len(canonical.Players[0].Observations))
	if len(featureNames) == 0 || len(featureNames) > 6 {
		return FeatureBatch{}, nil, errors.New("feature batch requires one to six observations per player")
	}
	seenFeatures := make(map[FeatureName]struct{}, len(featureNames))
	for index, observation := range canonical.Players[0].Observations {
		if !supportedFeature(observation.Name) {
			return FeatureBatch{}, nil, fmt.Errorf("feature batch contains unsupported feature %q", observation.Name)
		}
		if _, exists := seenFeatures[observation.Name]; exists {
			return FeatureBatch{}, nil, fmt.Errorf("feature batch contains duplicate feature %q", observation.Name)
		}
		seenFeatures[observation.Name] = struct{}{}
		featureNames[index] = observation.Name
	}

	previousPlayerID := ""
	for _, player := range canonical.Players {
		if player.PlayerID == "" || player.PlayerID == previousPlayerID {
			return FeatureBatch{}, nil, errors.New("feature batch requires unique non-empty player ids")
		}
		previousPlayerID = player.PlayerID
		if len(player.Observations) != len(featureNames) {
			return FeatureBatch{}, nil, fmt.Errorf("feature batch player %q has an incompatible observation count", player.PlayerID)
		}
		for index, observation := range player.Observations {
			if observation.Name != featureNames[index] {
				return FeatureBatch{}, nil, fmt.Errorf("feature batch player %q has an incompatible feature layout", player.PlayerID)
			}
			if !finite(observation.Value) {
				return FeatureBatch{}, nil, fmt.Errorf("feature batch player %q feature %q must be finite", player.PlayerID, observation.Name)
			}
			if !observation.Present && observation.Value != 0 {
				return FeatureBatch{}, nil, fmt.Errorf("feature batch player %q missing feature %q must have zero value", player.PlayerID, observation.Name)
			}
		}
	}

	return canonical, featureNames, nil
}

func validateFeatureBatchCompatibility(reference FeatureBatch, referenceNames []FeatureName, batch FeatureBatch, names []FeatureName) error {
	if batch.SchemaVersion != reference.SchemaVersion || batch.ModeID != reference.ModeID ||
		batch.ScoringRulesVersion != reference.ScoringRulesVersion || batch.DeckVersion != reference.DeckVersion {
		return errors.New("feature batches must share one schema, mode, scoring rules and deck version")
	}
	if !slices.Equal(names, referenceNames) {
		return errors.New("feature batches must share one feature layout")
	}

	return nil
}

func compareFeatureBatch(left, right FeatureBatch) int {
	if comparison := left.AvailableAt.Compare(right.AvailableAt); comparison != 0 {
		return comparison
	}
	if left.EventID < right.EventID {
		return -1
	}
	if left.EventID > right.EventID {
		return 1
	}
	return 0
}

func cloneFeatureBatches(source []FeatureBatch) []FeatureBatch {
	cloned := make([]FeatureBatch, len(source))
	for index, batch := range source {
		cloned[index] = cloneFeatureBatch(batch)
	}
	return cloned
}

func cloneFeatureBatch(batch FeatureBatch) FeatureBatch {
	batch.Players = slices.Clone(batch.Players)
	for index := range batch.Players {
		batch.Players[index].Observations = slices.Clone(batch.Players[index].Observations)
	}
	return batch
}
