package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestMatchProcessorPersistsEveryMatcherOutcome(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 18, 0, 0, 0, time.UTC)
	policy := matchmaking.Policy{
		Version: "policy-v1", RatingModelVersion: "rating-v1",
		InitialSkillGap: 100, MaxSkillGap: 100, MaxWinProbabilitySpread: 1,
		ExpansionInterval: 5 * time.Second, FillTimeout: 30 * time.Second,
		AgePriorityAfter: 15 * time.Second, CandidateLimit: 10, RoomLimit: 10,
		PreferNearlyFull: true,
	}
	candidate := processorCandidate("candidate", now.Add(-10*time.Second))
	claim := TicketClaim{
		Ticket: tournament.Ticket{ID: candidate.TicketID, Status: tournament.TicketQueued},
		Token:  "claim", Attempt: 1, LeaseUntil: now.Add(time.Minute),
	}

	t.Run("retry", func(t *testing.T) {
		queue := &processorQueue{}
		lifecycle := &processorLifecycle{}
		processor := newProcessorForTest(t, matchmaking.MatchAttempt{
			Trigger: matchmaking.AttemptTriggerPeriodicRetry, EvaluatedAt: now,
			Candidate: candidate, Policy: policy,
		}, queue, lifecycle)
		if err := processor.Handle(context.Background(), claim, now); err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
		if queue.retryAt.IsZero() || lifecycle.expire.CommandID != "" || lifecycle.assign.AssignmentID != "" {
			t.Fatalf("retry = %v, expiry = %+v, assignment = %+v", queue.retryAt, lifecycle.expire, lifecycle.assign)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		deadline := candidate.JoinedAt.Add(policy.FillTimeout)
		timedClaim := claim
		timedClaim.LeaseUntil = deadline.Add(time.Minute)
		queue := &processorQueue{}
		lifecycle := &processorLifecycle{}
		processor := newProcessorForTest(t, matchmaking.MatchAttempt{
			Trigger: matchmaking.AttemptTriggerPeriodicRetry, EvaluatedAt: deadline,
			Candidate: candidate, Policy: policy,
		}, queue, lifecycle)
		if err := processor.Handle(context.Background(), timedClaim, deadline); err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
		if lifecycle.expire.TicketID != claim.Ticket.ID || !lifecycle.expire.Deadline.Equal(deadline) {
			t.Fatalf("expiry command = %+v", lifecycle.expire)
		}
	})

	t.Run("stale room", func(t *testing.T) {
		queue := &processorQueue{}
		lifecycle := &processorLifecycle{assignErr: tournament.ErrRoomNotAvailable}
		members := make([]matchmaking.Candidate, 4)
		for index := range members {
			members[index] = processorCandidate(string(rune('a'+index)), now.Add(-20*time.Second))
		}
		processor := newProcessorForTest(t, matchmaking.MatchAttempt{
			Trigger: matchmaking.AttemptTriggerPeriodicRetry, EvaluatedAt: now,
			Candidate: candidate, Policy: policy,
			Rooms: []matchmaking.RoomView{{
				RoomID: "room-1", ModeID: "solitaire", AggregateVersion: 7,
				Capacity: 5, CreatedAt: now.Add(-20 * time.Second),
				Deadline: now.Add(10 * time.Second), Policy: policy, Members: members,
			}},
		}, queue, lifecycle)
		if err := processor.Handle(context.Background(), claim, now); err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
		if lifecycle.assign.ExpectedRoomVersion != 7 || !queue.retryAt.Equal(now.Add(50*time.Millisecond)) {
			t.Fatalf("assignment = %+v, retry = %v", lifecycle.assign, queue.retryAt)
		}
	})

	t.Run("successful room fill", func(t *testing.T) {
		queue := &processorQueue{}
		lifecycle := &processorLifecycle{}
		observer := &matchObserverStub{}
		members := make([]matchmaking.Candidate, 4)
		for index := range members {
			members[index] = processorCandidate(string(rune('a'+index)), now.Add(-20*time.Second))
		}
		attempt := matchmaking.MatchAttempt{
			Trigger: matchmaking.AttemptTriggerPeriodicRetry, EvaluatedAt: now,
			Candidate: candidate, Policy: policy,
			Rooms: []matchmaking.RoomView{{
				RoomID: "room-1", ModeID: "solitaire", AggregateVersion: 7,
				Capacity: 5, CreatedAt: now.Add(-20 * time.Second),
				Deadline: now.Add(10 * time.Second), Policy: policy, Members: members,
			}},
		}
		processor, err := NewMatchProcessor(
			processorAttemptStore{attempt: attempt}, queue, lifecycle, 50*time.Millisecond, observer,
		)
		if err != nil {
			t.Fatalf("NewMatchProcessor() error = %v", err)
		}
		observedClaim := claim
		observedClaim.Ticket.RequestedAt = candidate.JoinedAt
		if err := processor.Handle(context.Background(), observedClaim, now); err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
		observation := observer.observation
		if observation.Outcome != matchmaking.AttemptOutcomeMatched || observation.ModeID != "solitaire" ||
			observation.Capacity != 5 || !observation.RoomFilled || observation.RoomFillDuration != 20*time.Second ||
			observation.AssignmentLatency != 10*time.Second || observation.WinProbabilitySpread == nil {
			t.Fatalf("match observation = %+v", observation)
		}
	})
}

type matchObserverStub struct {
	observation MatchObservation
}

func (observer *matchObserverStub) ObserveMatch(observation MatchObservation) {
	observer.observation = observation
}

func newProcessorForTest(t *testing.T, attempt matchmaking.MatchAttempt, queue *processorQueue, lifecycle *processorLifecycle) *MatchProcessor {
	t.Helper()
	processor, err := NewMatchProcessor(processorAttemptStore{attempt: attempt}, queue, lifecycle, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("NewMatchProcessor() error = %v", err)
	}
	return processor
}

func processorCandidate(identity string, joinedAt time.Time) matchmaking.Candidate {
	return matchmaking.Candidate{
		TicketID: "ticket-" + identity, PlayerID: "player-" + identity,
		JoinedAt: joinedAt, SnapshotAt: joinedAt,
		Rating: rating.Estimate{
			Mean: 25, Uncertainty: 8, Games: 5,
			ModelVersion: "rating-v1", UpdatedAt: joinedAt.Add(-time.Minute),
		},
	}
}

type processorAttemptStore struct{ attempt matchmaking.MatchAttempt }

func (store processorAttemptStore) LoadMatchAttempt(context.Context, TicketClaim, time.Time) (matchmaking.MatchAttempt, error) {
	return store.attempt, nil
}

type processorQueue struct{ retryAt time.Time }

func (queue *processorQueue) ClaimMatchmakingTickets(context.Context, ClaimRequest) ([]TicketClaim, error) {
	return nil, errors.New("unexpected claim")
}

func (queue *processorQueue) ScheduleTicketRetry(_ context.Context, _, _ string, retryAt time.Time) error {
	queue.retryAt = retryAt
	return nil
}

type processorLifecycle struct {
	assign    tournament.AssignTicketCommand
	expire    tournament.ExpireTicketCommand
	assignErr error
}

func (lifecycle *processorLifecycle) Assign(_ context.Context, command tournament.AssignTicketCommand) (tournament.Assignment, error) {
	lifecycle.assign = command
	return tournament.Assignment{}, lifecycle.assignErr
}

func (lifecycle *processorLifecycle) Expire(_ context.Context, command tournament.ExpireTicketCommand) (tournament.TicketMutation, error) {
	lifecycle.expire = command
	return tournament.TicketMutation{}, nil
}
