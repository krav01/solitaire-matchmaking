package rating

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// FeatureWeight is a frozen training statistic and coefficient for one
// feature. Missing player observations contribute no adjustment.
type FeatureWeight struct {
	Name              FeatureName `json:"name"`
	Mean              float64     `json:"mean"`
	StandardDeviation float64     `json:"standard_deviation"`
	Weight            float64     `json:"weight"`
}

// ExtendedConfig is an immutable candidate definition. It deliberately has no
// default constructor: statistics and weights must come from time-ordered real
// training data rather than guessed production constants.
type ExtendedConfig struct {
	Baseline       BaselineConfig  `json:"baseline"`
	FeatureSchema  FeatureSchema   `json:"feature_schema"`
	FeatureWeights []FeatureWeight `json:"feature_weights"`
	TrainedThrough time.Time       `json:"trained_through"`
}

// ProfileObservation is a running mean of prior verified observations. Count
// zero is distinct from an observed zero value.
type ProfileObservation struct {
	Name  FeatureName `json:"name"`
	Mean  float64     `json:"mean"`
	Count uint64      `json:"count"`
}

// FeatureProfile contains only results available before the next prediction.
type FeatureProfile struct {
	SchemaVersion string               `json:"schema_version"`
	Observations  []ProfileObservation `json:"observations"`
}

// Extended combines the placement baseline with a weighted, prior-result
// feature profile. Result features update profiles only after scoring the
// immutable pre-game prediction.
type Extended struct {
	baseline *Baseline
	encoder  *FeatureEncoder
	config   ExtendedConfig
}

func NewExtended(config ExtendedConfig) (*Extended, error) {
	if config.TrainedThrough.IsZero() {
		return nil, errors.New("extended model training horizon is required")
	}
	baseline, err := NewBaseline(config.Baseline)
	if err != nil {
		return nil, err
	}
	encoder, err := NewFeatureEncoder(config.FeatureSchema)
	if err != nil {
		return nil, err
	}
	if len(config.FeatureWeights) != len(config.FeatureSchema.Definitions) {
		return nil, errors.New("extended model requires one weight per schema feature")
	}
	seen := make(map[FeatureName]struct{}, len(config.FeatureWeights))
	for index, weight := range config.FeatureWeights {
		if weight.Name != config.FeatureSchema.Definitions[index].Name {
			return nil, errors.New("extended feature weights must follow schema order")
		}
		if _, exists := seen[weight.Name]; exists {
			return nil, fmt.Errorf("extended model contains duplicate weight %q", weight.Name)
		}
		seen[weight.Name] = struct{}{}
		if !finite(weight.Mean) || !finite(weight.StandardDeviation) || weight.StandardDeviation < 0 || !finite(weight.Weight) {
			return nil, fmt.Errorf("extended feature weight %q must contain finite statistics and coefficient", weight.Name)
		}
	}

	config.FeatureSchema = cloneFeatureSchema(config.FeatureSchema)
	config.FeatureWeights = slices.Clone(config.FeatureWeights)
	return &Extended{baseline: baseline, encoder: encoder, config: config}, nil
}

func (e *Extended) Config() ExtendedConfig {
	config := e.config
	config.FeatureSchema = cloneFeatureSchema(config.FeatureSchema)
	config.FeatureWeights = slices.Clone(config.FeatureWeights)
	return config
}

func (e *Extended) InitialEstimate(at time.Time) (Estimate, error) {
	return e.baseline.InitialEstimate(at)
}

func (e *Extended) Update(result MatchResult, estimates map[string]Estimate, processedAt time.Time) ([]RatingUpdate, error) {
	return e.baseline.Update(result, estimates, processedAt)
}

// Predict applies coefficients to running means of prior verified features and
// then uses the baseline's coherent placement distribution.
func (e *Extended) Predict(request PredictionRequest, profiles map[string]FeatureProfile) (RoomPrediction, error) {
	adjusted := PredictionRequest{
		RoomID: request.RoomID, ModeID: request.ModeID, GeneratedAt: request.GeneratedAt,
		Estimates: make(map[string]Estimate, len(request.Estimates)),
	}
	for playerID, estimate := range request.Estimates {
		profile, exists := profiles[playerID]
		if !exists {
			adjusted.Estimates[playerID] = cloneEstimate(estimate)
			continue
		}
		if err := e.validateProfile(profile); err != nil {
			return RoomPrediction{}, fmt.Errorf("feature profile for player %q: %w", playerID, err)
		}
		for index, observation := range profile.Observations {
			if observation.Count == 0 {
				continue
			}
			weight := e.config.FeatureWeights[index]
			scale := weight.StandardDeviation
			if scale == 0 {
				scale = 1
			}
			estimate.Mean += weight.Weight * (observation.Mean - weight.Mean) / scale
		}
		if !finite(estimate.Mean) {
			return RoomPrediction{}, fmt.Errorf("extended prediction for player %q produced a non-finite mean", playerID)
		}
		adjusted.Estimates[playerID] = estimate
	}
	return e.baseline.Predict(adjusted)
}

// Observe updates running profiles from one verified result. Missing features
// do not change their counts or means.
func (e *Extended) Observe(result MatchResult, deckVersion string, profiles map[string]FeatureProfile) (map[string]FeatureProfile, error) {
	batch, err := e.encoder.Encode(result, FeatureContext{DeckVersion: deckVersion})
	if err != nil {
		return nil, err
	}
	updated := make(map[string]FeatureProfile, len(batch.Players))
	for _, player := range batch.Players {
		profile, exists := profiles[player.PlayerID]
		if !exists {
			profile = e.emptyProfile()
		} else if err := e.validateProfile(profile); err != nil {
			return nil, fmt.Errorf("feature profile for player %q: %w", player.PlayerID, err)
		}
		profile.Observations = slices.Clone(profile.Observations)
		for index, observation := range player.Observations {
			if !observation.Present {
				continue
			}
			current := &profile.Observations[index]
			if current.Count == math.MaxUint64 {
				return nil, fmt.Errorf("feature profile count for player %q and feature %q cannot be incremented", player.PlayerID, observation.Name)
			}
			current.Count++
			current.Mean += (observation.Value - current.Mean) / float64(current.Count)
			if !finite(current.Mean) {
				return nil, fmt.Errorf("feature profile for player %q and feature %q produced a non-finite mean", player.PlayerID, observation.Name)
			}
		}
		updated[player.PlayerID] = profile
	}
	return updated, nil
}

func (e *Extended) emptyProfile() FeatureProfile {
	profile := FeatureProfile{
		SchemaVersion: e.config.FeatureSchema.Version,
		Observations:  make([]ProfileObservation, len(e.config.FeatureSchema.Definitions)),
	}
	for index, definition := range e.config.FeatureSchema.Definitions {
		profile.Observations[index].Name = definition.Name
	}
	return profile
}

func (e *Extended) validateProfile(profile FeatureProfile) error {
	if profile.SchemaVersion != e.config.FeatureSchema.Version || len(profile.Observations) != len(e.config.FeatureSchema.Definitions) {
		return errors.New("profile does not match the extended feature schema")
	}
	for index, observation := range profile.Observations {
		if observation.Name != e.config.FeatureSchema.Definitions[index].Name || !finite(observation.Mean) {
			return errors.New("profile layout or value does not match the extended feature schema")
		}
		if observation.Count == 0 && observation.Mean != 0 {
			return fmt.Errorf("missing profile feature %q must have zero mean", observation.Name)
		}
	}
	return nil
}
