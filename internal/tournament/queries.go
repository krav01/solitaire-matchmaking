package tournament

import (
	"errors"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

var (
	ErrRoomNotFound   = errors.New("room not found")
	ErrRatingNotFound = errors.New("player rating not found")
)

// RoomState is the externally observable room lifecycle and its current
// composition. Members are ordered by seat.
type RoomState struct {
	RoomID              string
	TournamentID        string
	TournamentVersion   string
	ModeID              string
	PolicyVersion       string
	RatingModelVersion  string
	ScoringRulesVersion string
	SettlementVersion   string
	DeckID              string
	Capacity            int
	Status              RoomStatus
	AggregateVersion    int64
	CreatedAt           time.Time
	FillDeadline        time.Time
	FilledAt            *time.Time
	ResultDeadline      *time.Time
	CompletedAt         *time.Time
	ExpiredAt           *time.Time
	CancelledAt         *time.Time
	Members             []RoomMember
}

type RoomMember struct {
	TicketID    string
	PlayerID    string
	SessionID   string
	Seat        int
	Status      SessionStatus
	AssignedAt  time.Time
	StartedAt   *time.Time
	SubmittedAt *time.Time
	ForfeitedAt *time.Time
}

// PlayerRating is the latest persisted estimate for a player and mode. The
// model version remains explicit so callers can reconcile model transitions.
type PlayerRating struct {
	PlayerID string
	ModeID   string
	Estimate rating.Estimate
	Revision int64
}
