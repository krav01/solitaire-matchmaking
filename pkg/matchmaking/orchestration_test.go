package matchmaking_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
)

func TestAttemptMatchSelectsRoomOnQueueEvents(t *testing.T) {
	t.Parallel()
	evaluatedAt := time.Date(2026, time.September, 4, 6, 0, 10, 0, time.UTC)
	room := testRoom(evaluatedAt.Add(-5*time.Second), 5, []float64{25, 25, 25, 25})
	candidate := testCandidate("candidate", "new-player", 25, evaluatedAt.Add(-time.Second), evaluatedAt)
	for _, trigger := range []matchmaking.AttemptTrigger{
		matchmaking.AttemptTriggerTicketAccepted,
		matchmaking.AttemptTriggerRoomChanged,
	} {
		t.Run(string(trigger), func(t *testing.T) {
			t.Parallel()
			attempt := matchmaking.MatchAttempt{
				Trigger:     trigger,
				EvaluatedAt: evaluatedAt,
				Candidate:   candidate,
				Policy:      room.Policy,
				Rooms:       []matchmaking.RoomView{room},
			}
			originalRooms := cloneRooms(attempt.Rooms)

			result, err := newEvaluator(t).AttemptMatch(attempt)
			if err != nil {
				t.Fatalf("AttemptMatch() error = %v", err)
			}
			if result.Outcome != matchmaking.AttemptOutcomeMatched || result.Selection == nil || result.Selection.RoomID != room.RoomID || result.RetryAt != nil {
				t.Fatalf("AttemptMatch() = %+v, want selected room", result)
			}
			if !result.Deadline.Equal(candidate.JoinedAt.Add(room.Policy.FillTimeout)) {
				t.Fatalf("AttemptMatch() deadline = %v, want candidate fill deadline", result.Deadline)
			}
			if !reflect.DeepEqual(attempt.Rooms, originalRooms) {
				t.Fatal("AttemptMatch() mutated input rooms")
			}
		})
	}
}

func TestAttemptMatchSchedulesEarliestRoomExpansion(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.September, 4, 6, 30, 0, 0, time.UTC)
	evaluatedAt := base.Add(6 * time.Second)
	room := testRoom(base.Add(3*time.Second), 5, []float64{25})
	candidate := testCandidate("candidate", "new-player", 29, base, evaluatedAt)

	result, err := newEvaluator(t).AttemptMatch(matchmaking.MatchAttempt{
		Trigger:     matchmaking.AttemptTriggerPeriodicRetry,
		EvaluatedAt: evaluatedAt,
		Candidate:   candidate,
		Policy:      room.Policy,
		Rooms:       []matchmaking.RoomView{room},
	})
	if err != nil {
		t.Fatalf("AttemptMatch() error = %v", err)
	}
	wantRetry := base.Add(8 * time.Second)
	if result.Outcome != matchmaking.AttemptOutcomeRetryScheduled || result.Selection != nil || result.RetryAt == nil || !result.RetryAt.Equal(wantRetry) {
		t.Fatalf("AttemptMatch() = %+v, want retry at %v", result, wantRetry)
	}
}

func TestAttemptMatchKeepsPeriodicRetriesAlignedToTicketJoin(t *testing.T) {
	t.Parallel()
	joinedAt := time.Date(2026, time.September, 4, 7, 0, 0, 0, time.UTC)
	policy := testRoom(joinedAt, 5, nil).Policy
	candidate := testCandidate("candidate", "new-player", 25, joinedAt, joinedAt)

	result, err := newEvaluator(t).AttemptMatch(matchmaking.MatchAttempt{
		Trigger:     matchmaking.AttemptTriggerPeriodicRetry,
		EvaluatedAt: joinedAt.Add(time.Second),
		Candidate:   candidate,
		Policy:      policy,
	})
	if err != nil {
		t.Fatalf("AttemptMatch() error = %v", err)
	}
	wantRetry := joinedAt.Add(policy.ExpansionInterval)
	if result.RetryAt == nil || !result.RetryAt.Equal(wantRetry) {
		t.Fatalf("AttemptMatch() retry = %v, want %v", result.RetryAt, wantRetry)
	}
}

func TestAttemptMatchTimesOutAtTicketDeadline(t *testing.T) {
	t.Parallel()
	joinedAt := time.Date(2026, time.September, 4, 7, 30, 0, 0, time.UTC)
	policy := testRoom(joinedAt, 5, nil).Policy
	candidate := testCandidate("candidate", "new-player", 25, joinedAt, joinedAt)
	deadline := joinedAt.Add(policy.FillTimeout)

	result, err := newEvaluator(t).AttemptMatch(matchmaking.MatchAttempt{
		Trigger:     matchmaking.AttemptTriggerPeriodicRetry,
		EvaluatedAt: deadline,
		Candidate:   candidate,
		Policy:      policy,
	})
	if err != nil {
		t.Fatalf("AttemptMatch() error = %v", err)
	}
	if result.Outcome != matchmaking.AttemptOutcomeTimedOut || result.Selection != nil || result.RetryAt != nil || !result.Deadline.Equal(deadline) {
		t.Fatalf("AttemptMatch() = %+v, want timeout at deadline", result)
	}
}

func TestAttemptMatchMakesFinalSelectionAtTicketDeadline(t *testing.T) {
	t.Parallel()
	joinedAt := time.Date(2026, time.September, 4, 8, 0, 0, 0, time.UTC)
	room := testRoom(joinedAt.Add(5*time.Second), 5, []float64{25})
	candidate := testCandidate("candidate", "new-player", 25, joinedAt, joinedAt)
	deadline := joinedAt.Add(room.Policy.FillTimeout)

	result, err := newEvaluator(t).AttemptMatch(matchmaking.MatchAttempt{
		Trigger:     matchmaking.AttemptTriggerPeriodicRetry,
		EvaluatedAt: deadline,
		Candidate:   candidate,
		Policy:      room.Policy,
		Rooms:       []matchmaking.RoomView{room},
	})
	if err != nil {
		t.Fatalf("AttemptMatch() error = %v", err)
	}
	if result.Outcome != matchmaking.AttemptOutcomeMatched || result.Selection == nil || result.RetryAt != nil {
		t.Fatalf("AttemptMatch() = %+v, want final selection", result)
	}
}

func TestAttemptMatchRejectsInvalidScope(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 8, 30, 0, 0, time.UTC)
	room := testRoom(now, 5, nil)
	candidate := testCandidate("candidate", "new-player", 25, now, now)
	evaluator := newEvaluator(t)

	invalidTrigger := matchmaking.MatchAttempt{
		Trigger:     matchmaking.AttemptTrigger("manual"),
		EvaluatedAt: now,
		Candidate:   candidate,
		Policy:      room.Policy,
	}
	if _, err := evaluator.AttemptMatch(invalidTrigger); err == nil {
		t.Fatal("AttemptMatch() error = nil for invalid trigger")
	}

	mismatchedPolicy := room.Policy
	mismatchedPolicy.Version = "matching-v2"
	if _, err := evaluator.AttemptMatch(matchmaking.MatchAttempt{
		Trigger:     matchmaking.AttemptTriggerTicketAccepted,
		EvaluatedAt: now,
		Candidate:   candidate,
		Policy:      mismatchedPolicy,
		Rooms:       []matchmaking.RoomView{room},
	}); err == nil {
		t.Fatal("AttemptMatch() error = nil for mismatched room policy")
	}
}
