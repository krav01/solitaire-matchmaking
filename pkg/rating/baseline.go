package rating

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// BaselineConfig is an immutable model definition. Changing any parameter
// requires a new Version so historical updates remain replayable.
type BaselineConfig struct {
	Version                     string  `json:"version"`
	InitialMean                 float64 `json:"initial_mean"`
	InitialUncertainty          float64 `json:"initial_uncertainty"`
	MinimumUncertainty          float64 `json:"minimum_uncertainty"`
	MaximumUncertainty          float64 `json:"maximum_uncertainty"`
	DefaultPerformanceDeviation float64 `json:"default_performance_deviation"`
}

// DefaultBaselineConfig returns conservative TrueSkill-like rating units. The
// update itself is an online Bayesian approximation over pairwise placements.
func DefaultBaselineConfig(version string) BaselineConfig {
	return BaselineConfig{
		Version:                     version,
		InitialMean:                 25,
		InitialUncertainty:          25.0 / 3.0,
		MinimumUncertainty:          1,
		MaximumUncertainty:          25,
		DefaultPerformanceDeviation: 25.0 / 6.0,
	}
}

func (c BaselineConfig) validate() error {
	if c.Version == "" {
		return errors.New("baseline rating model version is required")
	}
	if !finite(c.InitialMean) {
		return errors.New("baseline initial mean must be finite")
	}
	if !positiveFinite(c.InitialUncertainty) || !positiveFinite(c.MinimumUncertainty) || !positiveFinite(c.MaximumUncertainty) {
		return errors.New("baseline uncertainty values must be positive and finite")
	}
	if c.MinimumUncertainty > c.InitialUncertainty || c.InitialUncertainty > c.MaximumUncertainty {
		return errors.New("baseline uncertainty bounds must contain the initial uncertainty")
	}
	if !positiveFinite(c.DefaultPerformanceDeviation) {
		return errors.New("baseline performance deviation must be positive and finite")
	}
	return nil
}

// Baseline updates Gaussian skill estimates from complete room placements.
type Baseline struct {
	config BaselineConfig
}

func NewBaseline(config BaselineConfig) (*Baseline, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &Baseline{config: config}, nil
}

// InitialEstimate creates the explicit prior callers should persist for a new
// player. The timestamp is supplied by the caller to keep replays deterministic.
func (b *Baseline) InitialEstimate(at time.Time) (Estimate, error) {
	if at.IsZero() {
		return Estimate{}, errors.New("initial rating timestamp is required")
	}
	return Estimate{
		Mean:         b.config.InitialMean,
		Uncertainty:  b.config.InitialUncertainty,
		ModelVersion: b.config.Version,
		UpdatedAt:    at,
	}, nil
}

// RatingUpdate is a replayable before/after record for one finalized result.
type RatingUpdate struct {
	PlayerID      string    `json:"player_id"`
	SourceEventID string    `json:"source_event_id"`
	ModelVersion  string    `json:"model_version"`
	Before        Estimate  `json:"before"`
	After         Estimate  `json:"after"`
	ProcessedAt   time.Time `json:"processed_at"`
}

// Update applies one finalized result. Every participant must have an explicit
// pre-game estimate from the same model version; input estimates are not mutated.
func (b *Baseline) Update(result MatchResult, estimates map[string]Estimate, processedAt time.Time) ([]RatingUpdate, error) {
	if err := result.Validate(); err != nil {
		return nil, err
	}
	if processedAt.IsZero() || processedAt.Before(result.AvailableAt) {
		return nil, errors.New("rating processing time must be at or after result availability")
	}

	participants := slices.Clone(result.Participants)
	slices.SortFunc(participants, func(a, other ParticipantResult) int {
		if a.PlayerID < other.PlayerID {
			return -1
		}
		if a.PlayerID > other.PlayerID {
			return 1
		}
		return 0
	})

	before := make(map[string]Estimate, len(participants))
	for _, participant := range participants {
		estimate, exists := estimates[participant.PlayerID]
		if !exists {
			return nil, fmt.Errorf("pre-game rating for player %q is required", participant.PlayerID)
		}
		if err := estimate.Validate(); err != nil {
			return nil, fmt.Errorf("pre-game rating for player %q: %w", participant.PlayerID, err)
		}
		if estimate.ModelVersion != b.config.Version {
			return nil, fmt.Errorf("pre-game rating for player %q uses model %q, want %q", participant.PlayerID, estimate.ModelVersion, b.config.Version)
		}
		if estimate.Uncertainty < b.config.MinimumUncertainty || estimate.Uncertainty > b.config.MaximumUncertainty {
			return nil, fmt.Errorf("pre-game uncertainty for player %q is outside model bounds", participant.PlayerID)
		}
		if estimate.Games == math.MaxUint64 {
			return nil, fmt.Errorf("pre-game count for player %q cannot be incremented", participant.PlayerID)
		}
		before[participant.PlayerID] = cloneEstimate(estimate)
	}

	updates := make([]RatingUpdate, 0, len(participants))
	for _, participant := range participants {
		current := before[participant.PlayerID]
		gradient := 0.0
		information := 0.0

		for _, opponent := range participants {
			if opponent.PlayerID == participant.PlayerID {
				continue
			}
			opponentEstimate := before[opponent.PlayerID]
			scale := comparisonScale(current, opponentEstimate, b.config.DefaultPerformanceDeviation)
			probability := logistic((current.Mean - opponentEstimate.Mean) / scale)
			outcome := pairwiseOutcome(participant.Place, opponent.Place)
			gradient += (outcome - probability) / scale
			information += probability * (1 - probability) / (scale * scale)
		}

		priorVariance := current.Uncertainty * current.Uncertainty
		posteriorVariance := 1 / (1/priorVariance + information)
		after := cloneEstimate(current)
		after.Mean += posteriorVariance * gradient
		if !finite(after.Mean) {
			return nil, fmt.Errorf("rating update for player %q produced a non-finite mean", participant.PlayerID)
		}
		after.Uncertainty = math.Max(b.config.MinimumUncertainty, math.Sqrt(posteriorVariance))
		after.Games++
		after.UpdatedAt = processedAt

		updates = append(updates, RatingUpdate{
			PlayerID:      participant.PlayerID,
			SourceEventID: result.EventID,
			ModelVersion:  b.config.Version,
			Before:        cloneEstimate(current),
			After:         after,
			ProcessedAt:   processedAt,
		})
	}
	return updates, nil
}

func comparisonScale(left, right Estimate, fallbackDeviation float64) float64 {
	leftDeviation := fallbackDeviation
	if left.PerformanceDeviation != nil {
		leftDeviation = *left.PerformanceDeviation
	}
	rightDeviation := fallbackDeviation
	if right.PerformanceDeviation != nil {
		rightDeviation = *right.PerformanceDeviation
	}
	skillScale := math.Hypot(left.Uncertainty, right.Uncertainty)
	performanceScale := math.Hypot(leftDeviation, rightDeviation)
	return math.Hypot(skillScale, performanceScale)
}

func pairwiseOutcome(place, opponentPlace int) float64 {
	switch {
	case place < opponentPlace:
		return 1
	case place > opponentPlace:
		return 0
	default:
		return 0.5
	}
}

func logistic(value float64) float64 {
	if value >= 0 {
		return 1 / (1 + math.Exp(-value))
	}
	exponential := math.Exp(value)
	return exponential / (1 + exponential)
}

func cloneEstimate(estimate Estimate) Estimate {
	if estimate.PerformanceDeviation != nil {
		deviation := *estimate.PerformanceDeviation
		estimate.PerformanceDeviation = &deviation
	}
	return estimate
}

func positiveFinite(value float64) bool {
	return value > 0 && finite(value)
}
