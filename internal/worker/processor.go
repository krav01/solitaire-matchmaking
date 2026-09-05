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
	observer        MatchObserver
	staleRetryDelay time.Duration
}

func NewMatchProcessor(attempts AttemptRepository, queue QueueRepository, tickets TicketLifecycle, staleRetryDelay time.Duration, observers ...MatchObserver) (*MatchProcessor, error) {
	if attempts == nil || queue == nil || tickets == nil {
		return nil, errors.New("match processor repositories are required")
	}
	if staleRetryDelay <= 0 || staleRetryDelay > time.Second {
		return nil, errors.New("stale room retry delay must be positive and at most one second")
	}
	if len(observers) > 1 {
		return nil, errors.New("match processor accepts at most one observer")
	}
	observer := MatchObserver(noopMatchObserver{})
	if len(observers) == 1 && observers[0] != nil {
		observer = observers[0]
	}

	return &MatchProcessor{
		attempts: attempts, queue: queue, tickets: tickets,
		observer: observer, staleRetryDelay: staleRetryDelay,
	}, nil
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
		assigned, err := processor.assign(ctx, claim, *result.Selection, evaluatedAt)
		if err == nil && assigned {
			processor.observer.ObserveMatch(newMatchObservation(attempt, *result.Selection, claim, evaluatedAt))
		}
		return err
	case matchmaking.AttemptOutcomeRetryScheduled:
		if err := processor.queue.ScheduleTicketRetry(ctx, claim.Ticket.ID, claim.Token, *result.RetryAt); err != nil {
			return err
		}
		processor.observer.ObserveMatch(MatchObservation{Outcome: result.Outcome})
		return nil
	case matchmaking.AttemptOutcomeTimedOut:
		identity := workIdentity("expiry", claim)
		_, err := processor.tickets.Expire(ctx, tournament.ExpireTicketCommand{
			TicketID: claim.Ticket.ID, CommandID: identity + "-command", EventID: identity + "-event",
			ClaimToken: claim.Token, Deadline: result.Deadline, ExpiredAt: evaluatedAt,
		})
		if errors.Is(err, tournament.ErrTicketClaimLost) {
			return nil
		}
		if err == nil {
			processor.observer.ObserveMatch(MatchObservation{Outcome: result.Outcome})
		}
		return err
	default:
		return errors.New("matcher returned an unknown outcome")
	}
}

func (processor *MatchProcessor) assign(ctx context.Context, claim TicketClaim, selection matchmaking.RoomSelection, assignedAt time.Time) (bool, error) {
	identity := workIdentity("assignment", claim)
	_, err := processor.tickets.Assign(ctx, tournament.AssignTicketCommand{
		AssignmentID: identity, TicketID: claim.Ticket.ID, RoomID: selection.RoomID,
		ExpectedRoomVersion: selection.RoomVersion, SessionID: identity + "-session",
		TicketEventID: identity + "-ticket-event", RoomFilledEventID: identity + "-room-event",
		AssignedAt: assignedAt, ClaimToken: claim.Token,
	})
	if errors.Is(err, tournament.ErrRoomNotAvailable) {
		if retryErr := processor.queue.ScheduleTicketRetry(ctx, claim.Ticket.ID, claim.Token, assignedAt.Add(processor.staleRetryDelay)); retryErr != nil {
			return false, retryErr
		}
		processor.observer.ObserveMatch(MatchObservation{Outcome: matchmaking.AttemptOutcomeRetryScheduled})
		return false, nil
	}
	if errors.Is(err, tournament.ErrTicketClaimLost) {
		return false, nil
	}
	return err == nil, err
}

func newMatchObservation(attempt matchmaking.MatchAttempt, selection matchmaking.RoomSelection, claim TicketClaim, evaluatedAt time.Time) MatchObservation {
	observation := MatchObservation{
		Outcome:       matchmaking.AttemptOutcomeMatched,
		PolicyVersion: attempt.Policy.Version, RatingModelVersion: attempt.Policy.RatingModelVersion,
		Capacity: selection.Capacity, AssignmentLatency: evaluatedAt.Sub(claim.Ticket.RequestedAt),
		RoomFilled: selection.MembersBefore+1 == selection.Capacity,
		SkillGap:   selection.Decision.SkillGap, MaximumSkillGap: attempt.Policy.MaxSkillGap,
		WinProbabilitySpread:     selection.Decision.WinProbabilitySpread,
		MaximumProbabilitySpread: attempt.Policy.MaxWinProbabilitySpread,
		FillTimeout:              attempt.Policy.FillTimeout,
	}
	for _, room := range attempt.Rooms {
		if room.RoomID == selection.RoomID {
			observation.ModeID = room.ModeID
			observation.RoomFillDuration = evaluatedAt.Sub(room.CreatedAt)
			break
		}
	}

	return observation
}

func workIdentity(kind string, claim TicketClaim) string {
	input := kind + "\x00" + claim.Ticket.ID + "\x00" + strconv.FormatInt(claim.Attempt, 10)
	digest := sha256.Sum256([]byte(input))
	return kind + "-" + hex.EncodeToString(digest[:16])
}
