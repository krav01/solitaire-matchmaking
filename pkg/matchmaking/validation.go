package matchmaking

import (
	"errors"
	"fmt"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

// Validate checks that the candidate contains one immutable pre-game rating
// snapshot and enough identity and timing data for deterministic evaluation.
func (c Candidate) Validate() error {
	if c.TicketID == "" || c.PlayerID == "" {
		return errors.New("candidate ticket and player ids are required")
	}
	if c.JoinedAt.IsZero() || c.SnapshotAt.IsZero() || c.SnapshotAt.After(c.JoinedAt) {
		return errors.New("candidate snapshot must be available no later than join time")
	}
	if err := c.Rating.Validate(); err != nil {
		return fmt.Errorf("candidate rating: %w", err)
	}
	if c.Rating.UpdatedAt.After(c.SnapshotAt) {
		return errors.New("candidate rating was updated after the pre-game snapshot")
	}
	return nil
}

// Validate checks room structure and immutable member compatibility. A forming
// room may be empty or full, but never exceed its configured capacity.
func (r RoomView) Validate() error {
	if r.RoomID == "" || r.ModeID == "" {
		return errors.New("room and mode ids are required")
	}
	if r.AggregateVersion < 0 {
		return errors.New("room aggregate version cannot be negative")
	}
	if r.Capacity < rating.MinRoomSize || r.Capacity > rating.MaxRoomSize {
		return fmt.Errorf("room capacity must be between %d and %d", rating.MinRoomSize, rating.MaxRoomSize)
	}
	if r.CreatedAt.IsZero() || r.Deadline.IsZero() || !r.Deadline.After(r.CreatedAt) {
		return errors.New("room requires ordered creation and deadline times")
	}
	if len(r.Members) > r.Capacity {
		return errors.New("room member count exceeds capacity")
	}
	if err := r.Policy.Validate(); err != nil {
		return fmt.Errorf("room policy: %w", err)
	}
	if r.Deadline.After(r.CreatedAt.Add(r.Policy.FillTimeout)) {
		return errors.New("room deadline exceeds the policy fill timeout")
	}

	tickets := make(map[string]struct{}, len(r.Members))
	players := make(map[string]struct{}, len(r.Members))
	for index, member := range r.Members {
		if err := member.Validate(); err != nil {
			return fmt.Errorf("room member %d: %w", index, err)
		}
		if member.SnapshotAt.After(r.Deadline) {
			return fmt.Errorf("room member %q was snapshotted after the room deadline", member.PlayerID)
		}
		if member.Rating.ModelVersion != r.Policy.RatingModelVersion {
			return fmt.Errorf("room member %q uses an incompatible rating model", member.PlayerID)
		}
		if _, duplicate := tickets[member.TicketID]; duplicate {
			return fmt.Errorf("room contains duplicate ticket %q", member.TicketID)
		}
		if _, duplicate := players[member.PlayerID]; duplicate {
			return fmt.Errorf("room contains duplicate player %q", member.PlayerID)
		}
		tickets[member.TicketID] = struct{}{}
		players[member.PlayerID] = struct{}{}
	}
	return nil
}
