package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

func TestRunnerBoundsConcurrencyAndReleasesFailedClaims(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 17, 0, 0, 0, time.UTC)
	queue := &runnerQueue{claims: make([]TicketClaim, 5)}
	for index := range queue.claims {
		queue.claims[index] = TicketClaim{
			Ticket: tournament.Ticket{ID: string(rune('a' + index)), Status: tournament.TicketQueued},
			Token:  "claim", Attempt: 1, LeaseUntil: now.Add(time.Minute),
		}
	}
	handler := &blockingHandler{started: make(chan struct{}, 5), release: make(chan struct{}), failTicket: "a"}
	runner, err := NewRunner(queue, handler, slog.New(slog.NewTextHandler(io.Discard, nil)), RunnerOptions{
		BatchSize: 5, Concurrency: 2, LeaseDuration: time.Minute,
		PollInterval: time.Second, FailureBackoff: time.Second,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	runner.now = func() time.Time { return now }
	runner.token = func() string { return "claim" }

	done := make(chan struct{})
	var result RunResult
	var runErr error
	go func() {
		result, runErr = runner.RunOnce(context.Background())
		close(done)
	}()
	<-handler.started
	<-handler.started
	select {
	case <-handler.started:
		t.Fatal("runner exceeded configured concurrency")
	case <-time.After(20 * time.Millisecond):
	}
	close(handler.release)
	<-done

	if result.Claimed != 5 || result.Failed != 1 || runErr == nil {
		t.Fatalf("RunOnce() = %+v, error = %v", result, runErr)
	}
	if queue.retryTicket != "a" || !queue.retryAt.Equal(now.Add(time.Second)) {
		t.Fatalf("failed retry = %q at %v", queue.retryTicket, queue.retryAt)
	}
}

type runnerQueue struct {
	claims      []TicketClaim
	mutex       sync.Mutex
	retryTicket string
	retryAt     time.Time
}

func (queue *runnerQueue) ClaimMatchmakingTickets(context.Context, ClaimRequest) ([]TicketClaim, error) {
	return queue.claims, nil
}

func (queue *runnerQueue) ScheduleTicketRetry(_ context.Context, ticketID, _ string, retryAt time.Time) error {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	queue.retryTicket = ticketID
	queue.retryAt = retryAt
	return nil
}

type blockingHandler struct {
	started    chan struct{}
	release    chan struct{}
	failTicket string
}

func (handler *blockingHandler) Handle(_ context.Context, claim TicketClaim, _ time.Time) error {
	handler.started <- struct{}{}
	<-handler.release
	if claim.Ticket.ID == handler.failTicket {
		return errors.New("synthetic failure")
	}
	return nil
}
