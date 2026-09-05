package worker

import (
	"context"
	"errors"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
)

const MaxBatchSize = 256

type ClaimRequest struct {
	Token      string
	Limit      int
	ClaimedAt  time.Time
	LeaseUntil time.Time
}

func (request ClaimRequest) Validate() error {
	if request.Token == "" {
		return errors.New("claim token is required")
	}
	if request.Limit <= 0 || request.Limit > MaxBatchSize {
		return errors.New("claim limit is outside the supported range")
	}
	if request.ClaimedAt.IsZero() || request.LeaseUntil.IsZero() || !request.LeaseUntil.After(request.ClaimedAt) {
		return errors.New("claim requires an ordered lease interval")
	}
	if request.LeaseUntil.Sub(request.ClaimedAt) > 5*time.Minute {
		return errors.New("claim lease cannot exceed five minutes")
	}
	return nil
}

type TicketClaim struct {
	Ticket     tournament.Ticket
	Token      string
	Attempt    int64
	LeaseUntil time.Time
}

func (claim TicketClaim) Validate() error {
	if claim.Token == "" || claim.Attempt <= 0 || claim.LeaseUntil.IsZero() {
		return errors.New("ticket claim identity, attempt and lease are required")
	}
	if claim.Ticket.ID == "" || claim.Ticket.Status != tournament.TicketQueued {
		return errors.New("ticket claim requires a queued ticket")
	}
	return nil
}

// QueueRepository owns durable leases and retry scheduling. Implementations
// must fence every completion by ticket and claim token.
type QueueRepository interface {
	ClaimMatchmakingTickets(context.Context, ClaimRequest) ([]TicketClaim, error)
	ScheduleTicketRetry(context.Context, string, string, time.Time) error
}

// AttemptRepository builds one bounded immutable view for the pure matcher.
type AttemptRepository interface {
	LoadMatchAttempt(context.Context, TicketClaim, time.Time) (matchmaking.MatchAttempt, error)
}

type TicketLifecycle interface {
	Assign(context.Context, tournament.AssignTicketCommand) (tournament.Assignment, error)
	Expire(context.Context, tournament.ExpireTicketCommand) (tournament.TicketMutation, error)
}
