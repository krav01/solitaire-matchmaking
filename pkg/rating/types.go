// Package rating defines portable skill-rating data and versioned rating models.
package rating

import (
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	// MinRoomSize is the smallest supported competitive room.
	MinRoomSize = 5
	// MaxRoomSize is the largest supported competitive room.
	MaxRoomSize = 7
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

// Validate rejects observations that cannot describe a completed solitaire
// game. Score is intentionally allowed to be negative because its range belongs
// to the referenced scoring-rules version.
func (f Features) Validate() error {
	if f.ElapsedMillis != nil && *f.ElapsedMillis < 0 {
		return errors.New("elapsed milliseconds must be non-negative")
	}
	if f.Moves != nil && *f.Moves < 0 {
		return errors.New("moves must be non-negative")
	}
	if f.UndoMoves != nil && *f.UndoMoves < 0 {
		return errors.New("undo moves must be non-negative")
	}
	if f.RevealedCards != nil && *f.RevealedCards < 0 {
		return errors.New("revealed cards must be non-negative")
	}
	return nil
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

// Validate ensures an outcome is complete enough to be replayed by a rating
// model. ScoringRulesVersion remains responsible for the meaning of ties.
func (r MatchResult) Validate() error {
	if r.EventID == "" || r.RoomID == "" || r.ModeID == "" || r.DeckID == "" || r.ScoringRulesVersion == "" {
		return errors.New("rating result identifiers and scoring rules version are required")
	}
	if r.FinishedAt.IsZero() || r.AvailableAt.IsZero() || r.AvailableAt.Before(r.FinishedAt) {
		return errors.New("rating result requires valid finish and availability times")
	}
	if len(r.Participants) < MinRoomSize || len(r.Participants) > MaxRoomSize {
		return fmt.Errorf("rating result requires %d to %d participants", MinRoomSize, MaxRoomSize)
	}

	players := make(map[string]struct{}, len(r.Participants))
	hasWinner := false
	for _, participant := range r.Participants {
		if participant.PlayerID == "" {
			return errors.New("rating result participant player id is required")
		}
		if _, exists := players[participant.PlayerID]; exists {
			return fmt.Errorf("rating result contains duplicate player %q", participant.PlayerID)
		}
		players[participant.PlayerID] = struct{}{}
		if participant.Place < 1 || participant.Place > len(r.Participants) {
			return fmt.Errorf("rating result place for player %q is outside the room", participant.PlayerID)
		}
		if err := participant.Features.Validate(); err != nil {
			return fmt.Errorf("rating result features for player %q: %w", participant.PlayerID, err)
		}
		hasWinner = hasWinner || participant.Place == 1
	}
	if !hasWinner {
		return errors.New("rating result requires at least one first-place participant")
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
