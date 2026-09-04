package tournament

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

var (
	ErrIdempotencyConflict = errors.New("idempotency identity was reused with different input")
	ErrTicketNotFound      = errors.New("ticket not found")
	ErrTicketNotQueued     = errors.New("ticket is not queued")
	ErrRoomNotAvailable    = errors.New("room is not available for assignment")
	ErrAssignmentConflict  = errors.New("assignment identity conflicts with stored assignment")
)

const digestLength = 64

// AcceptTicketCommand records an eligible, reserved game-backend entry. TicketID
// and EventID are generated identities and therefore do not affect retry equality.
type AcceptTicketCommand struct {
	Ticket  Ticket
	EventID string
}

func (command AcceptTicketCommand) Validate() error {
	ticket := command.Ticket
	if ticket.ID == "" || ticket.EntryID == "" || ticket.PlayerID == "" ||
		ticket.TournamentID == "" || ticket.TournamentVersion == "" || command.EventID == "" {
		return errors.New("ticket, entry, player, tournament and event identities are required")
	}
	if ticket.Status != TicketQueued || ticket.AssignedAt != nil || ticket.CancelledAt != nil || ticket.ExpiredAt != nil {
		return errors.New("accepted ticket must be queued without lifecycle timestamps")
	}
	if ticket.RequestedAt.IsZero() || ticket.SnapshotAt.IsZero() || ticket.SnapshotAt.After(ticket.RequestedAt) {
		return errors.New("ticket requires an available pre-game snapshot and request time")
	}
	if err := ticket.RatingSnapshot.Validate(); err != nil {
		return fmt.Errorf("ticket rating snapshot: %w", err)
	}
	if ticket.RatingSnapshot.UpdatedAt.After(ticket.SnapshotAt) {
		return errors.New("ticket rating was not available at snapshot time")
	}
	if ticket.RatingSnapshot.Games > uint64(1<<63-1) {
		return errors.New("ticket rating game count exceeds PostgreSQL bigint")
	}
	if ticket.AggregateVersion != 0 && ticket.AggregateVersion != 1 {
		return errors.New("new ticket aggregate version must be zero or one")
	}
	return nil
}

func (command AcceptTicketCommand) RequestDigest() (string, error) {
	if err := command.Validate(); err != nil {
		return "", err
	}
	ticket := command.Ticket
	ratingSnapshot := ticket.RatingSnapshot
	ratingSnapshot.UpdatedAt = ratingSnapshot.UpdatedAt.UTC()
	return sha256Digest(struct {
		EntryID           string          `json:"entry_id"`
		PlayerID          string          `json:"player_id"`
		TournamentID      string          `json:"tournament_id"`
		TournamentVersion string          `json:"tournament_version"`
		RequestedAt       time.Time       `json:"requested_at"`
		SnapshotAt        time.Time       `json:"snapshot_at"`
		RatingSnapshot    rating.Estimate `json:"rating_snapshot"`
	}{
		EntryID:           ticket.EntryID,
		PlayerID:          ticket.PlayerID,
		TournamentID:      ticket.TournamentID,
		TournamentVersion: ticket.TournamentVersion,
		RequestedAt:       ticket.RequestedAt.UTC(),
		SnapshotAt:        ticket.SnapshotAt.UTC(),
		RatingSnapshot:    ratingSnapshot,
	})
}

// CancelTicketCommand is idempotent by TicketID plus CommandID. CancelledAt is
// the first processing time and is excluded from retry equality.
type CancelTicketCommand struct {
	TicketID    string
	CommandID   string
	EventID     string
	CancelledAt time.Time
}

func (command CancelTicketCommand) Validate() error {
	if command.TicketID == "" || command.CommandID == "" || command.EventID == "" {
		return errors.New("ticket, command and event identities are required")
	}
	if command.CancelledAt.IsZero() {
		return errors.New("ticket cancellation time is required")
	}
	return nil
}

func (command CancelTicketCommand) RequestDigest() (string, error) {
	if err := command.Validate(); err != nil {
		return "", err
	}
	return sha256Digest(struct {
		TicketID  string `json:"ticket_id"`
		CommandID string `json:"command_id"`
	}{TicketID: command.TicketID, CommandID: command.CommandID})
}

// AssignTicketCommand is an internal compare-and-set request after the matcher
// selects a room. AssignedAt is excluded from retry equality.
type AssignTicketCommand struct {
	AssignmentID        string
	TicketID            string
	RoomID              string
	ExpectedRoomVersion int64
	SessionID           string
	TicketEventID       string
	RoomFilledEventID   string
	AssignedAt          time.Time
}

func (command AssignTicketCommand) Validate() error {
	if command.AssignmentID == "" || command.TicketID == "" || command.RoomID == "" ||
		command.SessionID == "" || command.TicketEventID == "" || command.RoomFilledEventID == "" {
		return errors.New("assignment, ticket, room, session and event identities are required")
	}
	if command.ExpectedRoomVersion <= 0 {
		return errors.New("positive expected room version is required")
	}
	if command.AssignedAt.IsZero() {
		return errors.New("ticket assignment time is required")
	}
	return nil
}

func (command AssignTicketCommand) RequestDigest() (string, error) {
	if err := command.Validate(); err != nil {
		return "", err
	}
	return sha256Digest(struct {
		AssignmentID string `json:"assignment_id"`
		TicketID     string `json:"ticket_id"`
		RoomID       string `json:"room_id"`
		RoomVersion  int64  `json:"room_version"`
		SessionID    string `json:"session_id"`
	}{
		AssignmentID: command.AssignmentID,
		TicketID:     command.TicketID,
		RoomID:       command.RoomID,
		RoomVersion:  command.ExpectedRoomVersion,
		SessionID:    command.SessionID,
	})
}

type TicketMutation struct {
	Ticket  Ticket
	Changed bool
	Replay  bool
}

type Assignment struct {
	AssignmentID   string
	TicketID       string
	RoomID         string
	SessionID      string
	PlayerID       string
	Seat           int
	AssignedAt     time.Time
	TicketVersion  int64
	RoomVersion    int64
	RoomFilled     bool
	ResultDeadline *time.Time
	Replay         bool
}

// TicketLifecycleRepository is implemented by the transactional persistence adapter.
type TicketLifecycleRepository interface {
	AcceptTicket(context.Context, AcceptTicketCommand) (TicketMutation, error)
	CancelTicket(context.Context, CancelTicketCommand) (TicketMutation, error)
	AssignTicket(context.Context, AssignTicketCommand) (Assignment, error)
}

type TicketService struct {
	repository TicketLifecycleRepository
}

func NewTicketService(repository TicketLifecycleRepository) (*TicketService, error) {
	if repository == nil {
		return nil, errors.New("ticket lifecycle repository is required")
	}
	return &TicketService{repository: repository}, nil
}

func (service *TicketService) Accept(ctx context.Context, command AcceptTicketCommand) (TicketMutation, error) {
	if err := command.Validate(); err != nil {
		return TicketMutation{}, err
	}
	return service.repository.AcceptTicket(ctx, command)
}

func (service *TicketService) Cancel(ctx context.Context, command CancelTicketCommand) (TicketMutation, error) {
	if err := command.Validate(); err != nil {
		return TicketMutation{}, err
	}
	return service.repository.CancelTicket(ctx, command)
}

func (service *TicketService) Assign(ctx context.Context, command AssignTicketCommand) (Assignment, error) {
	if err := command.Validate(); err != nil {
		return Assignment{}, err
	}
	return service.repository.AssignTicket(ctx, command)
}

func sha256Digest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode idempotency input: %w", err)
	}
	digest := sha256.Sum256(encoded)
	result := hex.EncodeToString(digest[:])
	if len(result) != digestLength {
		return "", errors.New("invalid SHA-256 digest length")
	}
	return result, nil
}
