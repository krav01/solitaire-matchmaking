// Package rating defines portable skill-rating data. Rating algorithms are added
// in the next stage; this package does not currently update or predict ratings.
package rating

import (
	"errors"
	"math"
	"time"
)

// Estimate separates uncertainty about skill from observed performance variation.
// A nil PerformanceDeviation means that there is not enough data to estimate it.
type Estimate struct {
	Mean                 float64   `json:"mean"`
	Uncertainty          float64   `json:"uncertainty"`
	PerformanceDeviation *float64  `json:"performance_deviation,omitempty"`
	Games                uint64    `json:"games"`
	ModelVersion         string    `json:"model_version"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// Validate rejects non-finite values, which would otherwise poison predictions.
func (e Estimate) Validate() error {
	if !finite(e.Mean) || !finite(e.Uncertainty) || e.Uncertainty <= 0 {
		return errors.New("rating requires a finite mean and positive finite uncertainty")
	}
	if e.PerformanceDeviation != nil && (!finite(*e.PerformanceDeviation) || *e.PerformanceDeviation < 0) {
		return errors.New("performance deviation must be finite and non-negative")
	}
	if e.ModelVersion == "" || e.UpdatedAt.IsZero() {
		return errors.New("rating model version and update time are required")
	}
	return nil
}

// Features contains verified optional observations, not hand-weighted bonuses.
// Score may already include time bonuses. Feature selection must avoid counting
// the same signal twice and must compare compatible decks and scoring rules.
type Features struct {
	Score         *int64 `json:"score,omitempty"`
	ElapsedMillis *int64 `json:"elapsed_ms,omitempty"`
	Completed     *bool  `json:"completed,omitempty"`
	Moves         *int64 `json:"moves,omitempty"`
	UndoMoves     *int64 `json:"undo_moves,omitempty"`
	RevealedCards *int64 `json:"revealed_cards,omitempty"`
}

// ParticipantResult is an observation in a finalized room. Place is one-based;
// tie handling is determined by the referenced scoring-rules version.
type ParticipantResult struct {
	PlayerID string   `json:"player_id"`
	Place    int      `json:"place"`
	Features Features `json:"features"`
}

// MatchResult is a complete verified outcome, not a partial open-room result.
// AvailableAt is when this outcome became available to the rating system.
type MatchResult struct {
	EventID             string              `json:"event_id"`
	RoomID              string              `json:"room_id"`
	ModeID              string              `json:"mode_id"`
	DeckID              string              `json:"deck_id"`
	ScoringRulesVersion string              `json:"scoring_rules_version"`
	FinishedAt          time.Time           `json:"finished_at"`
	AvailableAt         time.Time           `json:"available_at"`
	Participants        []ParticipantResult `json:"participants"`
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
