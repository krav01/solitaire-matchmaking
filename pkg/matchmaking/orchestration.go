package matchmaking

import (
	"errors"
	"fmt"
	"time"
)

// AttemptTrigger identifies why the application evaluated a waiting ticket.
// Both triggers use the same deterministic matching rules.
type AttemptTrigger string

const (
	AttemptTriggerTicketAccepted AttemptTrigger = "ticket_accepted"
	AttemptTriggerRoomChanged    AttemptTrigger = "room_changed"
	AttemptTriggerPeriodicRetry  AttemptTrigger = "periodic_retry"
)

// AttemptOutcome is the next application action for a waiting ticket.
type AttemptOutcome string

const (
	AttemptOutcomeMatched        AttemptOutcome = "matched"
	AttemptOutcomeRetryScheduled AttemptOutcome = "retry_scheduled"
	AttemptOutcomeTimedOut       AttemptOutcome = "timed_out"
)

// MatchAttempt is one immutable, bounded view of a ticket and its compatible
// room partition. Policy also defines the ticket timeout and retry cadence.
type MatchAttempt struct {
	Trigger     AttemptTrigger
	EvaluatedAt time.Time
	Candidate   Candidate
	Policy      Policy
	Rooms       []RoomView
}

// MatchAttemptResult tells the application whether to claim a room, schedule
// another evaluation, or expire the ticket. Deadline is always the ticket's
// absolute fill deadline. Exactly one of Selection and RetryAt is populated for
// the corresponding outcome.
type MatchAttemptResult struct {
	Outcome   AttemptOutcome
	Deadline  time.Time
	Selection *RoomSelection
	RetryAt   *time.Time
}

// AttemptMatch performs one event-driven or timer-driven selection attempt.
// It does not mutate rooms, claim capacity, start timers or persist state.
func (e *Evaluator) AttemptMatch(attempt MatchAttempt) (MatchAttemptResult, error) {
	if err := validateMatchAttempt(attempt); err != nil {
		return MatchAttemptResult{}, err
	}

	deadline := attempt.Candidate.JoinedAt.Add(attempt.Policy.FillTimeout)
	if attempt.EvaluatedAt.After(deadline) {
		return MatchAttemptResult{Outcome: AttemptOutcomeTimedOut, Deadline: deadline}, nil
	}

	selection, found, err := e.SelectRoom(attempt.Rooms, attempt.Candidate, attempt.EvaluatedAt)
	if err != nil {
		return MatchAttemptResult{}, err
	}
	if found {
		return MatchAttemptResult{
			Outcome:   AttemptOutcomeMatched,
			Deadline:  deadline,
			Selection: &selection,
		}, nil
	}
	if !attempt.EvaluatedAt.Before(deadline) {
		return MatchAttemptResult{Outcome: AttemptOutcomeTimedOut, Deadline: deadline}, nil
	}

	retryAt := nextRetryBoundary(attempt, deadline)
	return MatchAttemptResult{
		Outcome:  AttemptOutcomeRetryScheduled,
		Deadline: deadline,
		RetryAt:  &retryAt,
	}, nil
}

func validateMatchAttempt(attempt MatchAttempt) error {
	if attempt.Trigger != AttemptTriggerTicketAccepted &&
		attempt.Trigger != AttemptTriggerRoomChanged &&
		attempt.Trigger != AttemptTriggerPeriodicRetry {
		return errors.New("match attempt trigger is invalid")
	}
	if attempt.EvaluatedAt.IsZero() {
		return errors.New("match attempt evaluation time is required")
	}
	if err := attempt.Policy.Validate(); err != nil {
		return fmt.Errorf("match attempt policy: %w", err)
	}
	if err := attempt.Candidate.Validate(); err != nil {
		return fmt.Errorf("match attempt candidate: %w", err)
	}
	if attempt.Candidate.JoinedAt.After(attempt.EvaluatedAt) || attempt.Candidate.SnapshotAt.After(attempt.EvaluatedAt) {
		return errors.New("match attempt requires a candidate snapshot available at evaluation time")
	}
	if attempt.Candidate.Rating.ModelVersion != attempt.Policy.RatingModelVersion {
		return errors.New("match attempt candidate uses an incompatible rating model")
	}
	if len(attempt.Rooms) > attempt.Policy.RoomLimit {
		return errors.New("match attempt room batch exceeds the policy scan limit")
	}
	if len(attempt.Rooms) > 0 {
		if err := validateSelectionScope(attempt.Rooms); err != nil {
			return fmt.Errorf("match attempt rooms: %w", err)
		}
		if attempt.Rooms[0].Policy != attempt.Policy {
			return errors.New("match attempt rooms use an incompatible policy")
		}
	}
	for index, room := range attempt.Rooms {
		if attempt.EvaluatedAt.Before(room.CreatedAt) {
			return fmt.Errorf("match attempt room %d was created after evaluation time", index)
		}
	}
	return nil
}

func nextRetryBoundary(attempt MatchAttempt, deadline time.Time) time.Time {
	next := nextPeriodicBoundary(attempt.Candidate.JoinedAt, attempt.Policy.ExpansionInterval, attempt.EvaluatedAt)
	if next.After(deadline) {
		next = deadline
	}

	for _, room := range attempt.Rooms {
		if !attempt.EvaluatedAt.Before(room.Deadline) {
			continue
		}
		roomNext := room.Deadline
		if room.Policy.InitialSkillGap < room.Policy.MaxSkillGap {
			expansion := nextPeriodicBoundary(room.CreatedAt, room.Policy.ExpansionInterval, attempt.EvaluatedAt)
			if expansion.Before(roomNext) {
				roomNext = expansion
			}
		}
		if roomNext.Before(next) {
			next = roomNext
		}
	}
	return next
}

func nextPeriodicBoundary(anchor time.Time, interval time.Duration, after time.Time) time.Time {
	elapsed := after.Sub(anchor)
	return after.Add(interval - elapsed%interval)
}
