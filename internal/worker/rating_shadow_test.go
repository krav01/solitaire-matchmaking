package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestRatingShadowRunnerProcessesOneTimelineItem(t *testing.T) {
	t.Parallel()
	queue := &ratingShadowQueueStub{claim: &RatingShadowClaim{
		Kind: "room", SourceID: "room-a", Token: "claim", Attempt: 1, LeaseUntil: time.Now().Add(time.Minute),
	}, persisted: 1}
	observer := &workerObserverStub{}
	runner, err := NewRatingShadowRunner(queue, slog.New(slog.NewTextHandler(io.Discard, nil)), RatingShadowRunnerOptions{
		LeaseDuration: time.Second, PollInterval: time.Millisecond, FailureBackoff: time.Second, Observer: observer,
	})
	if err != nil {
		t.Fatalf("NewRatingShadowRunner() error = %v", err)
	}
	result, err := runner.RunOnce(context.Background())
	if err != nil || result != (RatingShadowRunResult{Claimed: 1, Persisted: 1}) {
		t.Fatalf("RunOnce() = %+v, error = %v", result, err)
	}
	if observer.observation != (WorkerCycleObservation{Worker: WorkerRatingShadow, Claimed: 1, Succeeded: 1}) {
		t.Fatalf("observer = %+v", observer.observation)
	}
}

func TestRatingShadowRunnerSchedulesFailure(t *testing.T) {
	t.Parallel()
	queue := &ratingShadowQueueStub{claim: &RatingShadowClaim{
		Kind: "result", SourceID: "event-a", Token: "claim", Attempt: 1, LeaseUntil: time.Now().Add(time.Minute),
	}, processErr: errors.New("boom")}
	runner, err := NewRatingShadowRunner(queue, slog.New(slog.NewTextHandler(io.Discard, nil)), RatingShadowRunnerOptions{
		LeaseDuration: time.Second, PollInterval: time.Millisecond, FailureBackoff: time.Second,
	})
	if err != nil {
		t.Fatalf("NewRatingShadowRunner() error = %v", err)
	}
	result, err := runner.RunOnce(context.Background())
	if err == nil || result.Failed != 1 || !queue.retried {
		t.Fatalf("RunOnce() = %+v, error = %v, retried = %v", result, err, queue.retried)
	}
}

type ratingShadowQueueStub struct {
	claim      *RatingShadowClaim
	persisted  int
	processErr error
	retried    bool
}

func (stub *ratingShadowQueueStub) ClaimNextRatingShadowWork(context.Context, RatingClaimRequest) (*RatingShadowClaim, error) {
	return stub.claim, nil
}

func (stub *ratingShadowQueueStub) ProcessRatingShadowWork(context.Context, RatingShadowClaim, time.Time) (int, error) {
	return stub.persisted, stub.processErr
}

func (stub *ratingShadowQueueStub) ScheduleRatingShadowRetry(context.Context, RatingShadowClaim, time.Time) error {
	stub.retried = true
	return nil
}
