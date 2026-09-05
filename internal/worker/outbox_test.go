package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestOutboxRunnerBoundsConcurrencyAndRetriesFailures(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 5, 6, 0, 0, 0, time.UTC)
	queue := &outboxQueueStub{claims: make([]OutboxClaim, 5)}
	for index := range queue.claims {
		queue.claims[index] = outboxClaim(string(rune('a'+index)), now, index+1)
	}
	publisher := &blockingPublisher{
		started: make(chan struct{}, 5), release: make(chan struct{}), failEvent: "a",
	}
	runner, err := NewOutboxRunner(queue, publisher, slog.New(slog.NewTextHandler(io.Discard, nil)), OutboxRunnerOptions{
		BatchSize: 5, Concurrency: 2, LeaseDuration: time.Minute,
		PollInterval: time.Second, RetryBaseDelay: time.Second, RetryMaxDelay: 8 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOutboxRunner() error = %v", err)
	}
	runner.now = func() time.Time { return now }
	runner.token = func() string { return "claim" }

	done := make(chan struct{})
	var result OutboxRunResult
	var runErr error
	go func() {
		result, runErr = runner.RunOnce(context.Background())
		close(done)
	}()

	<-publisher.started
	<-publisher.started
	select {
	case <-publisher.started:
		t.Fatal("runner exceeded configured concurrency")
	case <-time.After(20 * time.Millisecond):
	}
	close(publisher.release)
	<-done

	if result != (OutboxRunResult{Claimed: 5, Delivered: 4, Failed: 1}) || runErr == nil {
		t.Fatalf("RunOnce() = %+v, error = %v", result, runErr)
	}
	if queue.retryEvent != "a" || !queue.retryAt.Equal(now.Add(time.Second)) || len(queue.delivered) != 4 {
		t.Fatalf("queue retry = %q at %v, delivered = %v", queue.retryEvent, queue.retryAt, queue.delivered)
	}
}

func TestOutboxRunnerCapsExponentialRetry(t *testing.T) {
	t.Parallel()

	runner := OutboxRunner{options: OutboxRunnerOptions{RetryBaseDelay: time.Second, RetryMaxDelay: 10 * time.Second}}
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 4: 8 * time.Second, 5: 10 * time.Second, 100: 10 * time.Second} {
		if got := runner.retryDelay(attempt); got != want {
			t.Fatalf("retryDelay(%d) = %v, want %v", attempt, got, want)
		}
	}
}

func outboxClaim(eventID string, now time.Time, attempt int) OutboxClaim {
	return OutboxClaim{
		Event: OutboxEvent{
			EventID: eventID, AggregateType: "room", AggregateID: "room-" + eventID,
			AggregateVersion: 1, EventType: "room.completed",
			Payload: json.RawMessage(`{"room_id":"room-` + eventID + `"}`), OccurredAt: now.Add(-time.Minute),
		},
		Token: "claim", Attempt: attempt, LeaseUntil: now.Add(time.Minute),
	}
}

type outboxQueueStub struct {
	claims     []OutboxClaim
	mutex      sync.Mutex
	delivered  []string
	retryEvent string
	retryAt    time.Time
}

func (queue *outboxQueueStub) ClaimOutboxEvents(context.Context, OutboxClaimRequest) ([]OutboxClaim, error) {
	return queue.claims, nil
}

func (queue *outboxQueueStub) MarkOutboxDelivered(_ context.Context, eventID, _ string, _ time.Time) error {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()

	queue.delivered = append(queue.delivered, eventID)
	return nil
}

func (queue *outboxQueueStub) ScheduleOutboxRetry(_ context.Context, eventID, _ string, retryAt time.Time, _ string) error {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()

	queue.retryEvent = eventID
	queue.retryAt = retryAt
	return nil
}

type blockingPublisher struct {
	started   chan struct{}
	release   chan struct{}
	failEvent string
}

func (publisher *blockingPublisher) Publish(_ context.Context, event OutboxEvent) error {
	publisher.started <- struct{}{}
	<-publisher.release
	if event.EventID == publisher.failEvent {
		return errors.New("synthetic delivery failure")
	}

	return nil
}
