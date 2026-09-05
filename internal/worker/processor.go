package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

type MatchProcessor struct {
	attempts        AttemptRepository
	queue           QueueRepository
	tickets         TicketLifecycle
	staleRetryDelay time.Duration
}

func NewMatchProcessor(attempts AttemptRepository, queue QueueRepository, tickets TicketLifecycle, staleRetryDelay time.Duration) (*MatchProcessor, error) {
	if attempts == nil || queue == nil || tickets == nil {
		return nil, errors.New("match processor repositories are required")
	}
	if staleRetryDelay <= 0 || staleRetryDelay > time.Second {
		return nil, errors.New("stale room retry delay must be positive and at most one second")
	}
	return &MatchProcessor{attempts: attempts, queue: queue, tickets: tickets, staleRetryDelay: staleRetryDelay}, nil
}

func (processor *MatchProcessor) Handle(ctx context.Context, claim TicketClaim, evaluatedAt time.Time) error {
	if err := claim.Validate(); err != nil {
		return err
	}
	if evaluatedAt.IsZero() || !evaluatedAt.Before(claim.LeaseUntil) {
		return errors.New("match processing time must be inside the claim lease")
	}
	attempt, err := processor.attempts.LoadMatchAttempt(ctx, claim, evaluatedAt)
	if err != nil {
		return fmt.Errorf("load match attempt: %w", err)
	}
	model, err := rating.NewBaseline(rating.DefaultBaselineConfig(attempt.Policy.RatingModelVersion))
	if err != nil {
		return fmt.Errorf("build rating predictor: %w", err)
	}
	evaluator, err := matchmaking.NewEvaluator(model)
	if err != nil {
		return fmt.Errorf("build matcher: %w", err)
	}
	result, err := evaluator.AttemptMatch(attempt)
	if err != nil {
		return fmt.Errorf("evaluate match attempt: %w", err)
	}

	switch result.Outcome {
	case matchmaking.AttemptOutcomeMatched:
		return processor.assign(ctx, claim, *result.Selection, evaluatedAt)
	case matchmaking.AttemptOutcomeRetryScheduled:
		return processor.queue.ScheduleTicketRetry(ctx, claim.Ticket.ID, claim.Token, *result.RetryAt)
	case matchmaking.AttemptOutcomeTimedOut:
		identity := workIdentity("expiry", claim)
		_, err := processor.tickets.Expire(ctx, tournament.ExpireTicketCommand{
			TicketID: claim.Ticket.ID, CommandID: identity + "-command", EventID: identity + "-event",
			ClaimToken: claim.Token, Deadline: result.Deadline, ExpiredAt: evaluatedAt,
		})
		if errors.Is(err, tournament.ErrTicketClaimLost) {
			return nil
		}
		return err
	default:
		return errors.New("matcher returned an unknown outcome")
	}
}

func (processor *MatchProcessor) assign(ctx context.Context, claim TicketClaim, selection matchmaking.RoomSelection, assignedAt time.Time) error {
	identity := workIdentity("assignment", claim)
	_, err := processor.tickets.Assign(ctx, tournament.AssignTicketCommand{
		AssignmentID: identity, TicketID: claim.Ticket.ID, RoomID: selection.RoomID,
		ExpectedRoomVersion: selection.RoomVersion, SessionID: identity + "-session",
		TicketEventID: identity + "-ticket-event", RoomFilledEventID: identity + "-room-event",
		AssignedAt: assignedAt, ClaimToken: claim.Token,
	})
	if errors.Is(err, tournament.ErrRoomNotAvailable) {
		return processor.queue.ScheduleTicketRetry(ctx, claim.Ticket.ID, claim.Token, assignedAt.Add(processor.staleRetryDelay))
	}
	if errors.Is(err, tournament.ErrTicketClaimLost) {
		return nil
	}
	return err
}

func workIdentity(kind string, claim TicketClaim) string {
	input := kind + "\x00" + claim.Ticket.ID + "\x00" + strconv.FormatInt(claim.Attempt, 10)
	digest := sha256.Sum256([]byte(input))
	return kind + "-" + hex.EncodeToString(digest[:16])
}
