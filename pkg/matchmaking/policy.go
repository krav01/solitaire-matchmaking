// Package matchmaking defines portable candidate, policy, room-evaluation and
// retry-scheduling contracts. Durable queue orchestration is implemented by the
// consuming application.
package matchmaking

import (
	"errors"
	"math"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

// Policy is immutable once referenced by a room. Skill gaps are expressed in the
// selected model's units. Probability spread is max(p)-min(p) across the room.
// No numerical policy is enabled by default: thresholds need simulation and data.
type Policy struct {
	Version                 string
	RatingModelVersion      string
	InitialSkillGap         float64
	MaxSkillGap             float64
	MaxWinProbabilitySpread float64
	ExpansionInterval       time.Duration
	FillTimeout             time.Duration
	AgePriorityAfter        time.Duration
	CandidateLimit          int
	RoomLimit               int
	PreferNearlyFull        bool
}

// Validate checks configuration bounds, not the fairness of a particular room.
func (p Policy) Validate() error {
	if p.Version == "" || p.RatingModelVersion == "" {
		return errors.New("policy and rating model versions are required")
	}
	if !finite(p.InitialSkillGap) || !finite(p.MaxSkillGap) || p.InitialSkillGap < 0 || p.MaxSkillGap < p.InitialSkillGap {
		return errors.New("skill gaps must be finite, non-negative and ordered")
	}
	if !finite(p.MaxWinProbabilitySpread) || p.MaxWinProbabilitySpread < 0 || p.MaxWinProbabilitySpread > 1 {
		return errors.New("win probability spread must be between zero and one")
	}
	if p.FillTimeout <= 0 || p.ExpansionInterval <= 0 || p.ExpansionInterval > p.FillTimeout {
		return errors.New("expansion interval must be positive and no longer than fill timeout")
	}
	if p.AgePriorityAfter < 0 || p.AgePriorityAfter > p.FillTimeout || p.CandidateLimit <= 0 || p.RoomLimit <= 0 {
		return errors.New("invalid age priority or scan limit")
	}
	return nil
}

// Candidate intentionally has no current-game score or result. The snapshot
// must have been captured before the participant received the game deck.
type Candidate struct {
	TicketID   string
	PlayerID   string
	JoinedAt   time.Time
	SnapshotAt time.Time
	Rating     rating.Estimate
}

// RoomView is the immutable input to candidate evaluation. The tournament
// application checks mode, fee, size, eligibility and policy versions first.
type RoomView struct {
	RoomID    string
	ModeID    string
	Capacity  int
	CreatedAt time.Time
	Deadline  time.Time
	Policy    Policy
	Members   []Candidate
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
