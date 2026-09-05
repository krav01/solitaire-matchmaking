package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestRatingRunnerProcessesOneResult(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	queue := &ratingQueueStub{claim: &RatingClaim{EventID: "result-a", Token: "claim", Attempt: 1, LeaseUntil: now.Add(time.Minute)}, updated: 5}
	runner, err := NewRatingRunner(queue, slog.New(slog.NewTextHandler(io.Discard, nil)), RatingRunnerOptions{
		LeaseDuration: time.Minute, PollInterval: time.Second, FailureBackoff: time.Second,
	})
	if err != nil {
		t.Fatalf("NewRatingRunner() error = %v", err)
	}
	runner.now = func() time.Time { return now }
	runner.token = func() string { return "claim" }
	result, err := runner.RunOnce(context.Background())
	if err != nil || result != (RatingRunResult{Claimed: 1, Updated: 5}) {
		t.Fatalf("RunOnce() = %+v, error = %v", result, err)
	}
}

func TestRatingRunnerSchedulesFailedResult(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	queue := &ratingQueueStub{
		claim:      &RatingClaim{EventID: "result-a", Token: "claim", Attempt: 1, LeaseUntil: now.Add(time.Minute)},
		processErr: errors.New("synthetic failure"),
	}
	runner, err := NewRatingRunner(queue, slog.New(slog.NewTextHandler(io.Discard, nil)), RatingRunnerOptions{
		LeaseDuration: time.Minute, PollInterval: time.Second, FailureBackoff: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRatingRunner() error = %v", err)
	}
	runner.now = func() time.Time { return now }
	runner.token = func() string { return "claim" }
	result, err := runner.RunOnce(context.Background())
	if err == nil || result.Claimed != 1 || result.Failed != 1 || !queue.retryAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("RunOnce() = %+v, retry = %v, error = %v", result, queue.retryAt, err)
	}
}

type ratingQueueStub struct {
	claim      *RatingClaim
	updated    int
	processErr error
	retryAt    time.Time
}

func (queue *ratingQueueStub) ClaimNextRatingResult(context.Context, RatingClaimRequest) (*RatingClaim, error) {
	return queue.claim, nil
}

func (queue *ratingQueueStub) ProcessRatingResult(context.Context, RatingClaim, time.Time) (int, error) {
	return queue.updated, queue.processErr
}

func (queue *ratingQueueStub) ScheduleRatingRetry(_ context.Context, _, _ string, retryAt time.Time) error {
	queue.retryAt = retryAt
	return nil
}
