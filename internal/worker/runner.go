package worker

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

type Handler interface {
	Handle(context.Context, TicketClaim, time.Time) error
}

type RunnerOptions struct {
	BatchSize      int
	Concurrency    int
	LeaseDuration  time.Duration
	PollInterval   time.Duration
	FailureBackoff time.Duration
}

func (options RunnerOptions) Validate() error {
	if options.BatchSize <= 0 || options.BatchSize > MaxBatchSize {
		return errors.New("worker batch size is outside the supported range")
	}
	if options.Concurrency <= 0 || options.Concurrency > options.BatchSize {
		return errors.New("worker concurrency must fit inside the batch")
	}
	if options.LeaseDuration <= 0 || options.LeaseDuration > 5*time.Minute {
		return errors.New("worker lease duration must be positive and at most five minutes")
	}
	if options.PollInterval <= 0 || options.PollInterval > time.Minute ||
		options.FailureBackoff <= 0 || options.FailureBackoff > time.Minute {
		return errors.New("worker polling and failure backoff must be positive")
	}
	return nil
}

type RunResult struct {
	Claimed int
	Failed  int
}

type Runner struct {
	queue   QueueRepository
	handler Handler
	logger  *slog.Logger
	options RunnerOptions
	now     func() time.Time
	token   func() string
}

func NewRunner(queue QueueRepository, handler Handler, logger *slog.Logger, options RunnerOptions) (*Runner, error) {
	if queue == nil || handler == nil || logger == nil {
		return nil, errors.New("worker queue, handler and logger are required")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &Runner{queue: queue, handler: handler, logger: logger, options: options, now: time.Now, token: rand.Text}, nil
}

func (runner *Runner) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			result, err := runner.RunOnce(ctx)
			if err != nil {
				runner.logger.WarnContext(ctx, "matchmaking worker cycle failed", "claimed", result.Claimed, "failed", result.Failed, "error", err)
			}
			timer.Reset(runner.options.PollInterval)
		}
	}
}

func (runner *Runner) RunOnce(ctx context.Context) (RunResult, error) {
	now := runner.now().UTC()
	claims, err := runner.queue.ClaimMatchmakingTickets(ctx, ClaimRequest{
		Token: runner.token(), Limit: runner.options.BatchSize,
		ClaimedAt: now, LeaseUntil: now.Add(runner.options.LeaseDuration),
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("claim matchmaking tickets: %w", err)
	}
	result := RunResult{Claimed: len(claims)}
	if len(claims) == 0 {
		return result, nil
	}

	semaphore := make(chan struct{}, runner.options.Concurrency)
	errorsByClaim := make(chan error, len(claims))
	var group sync.WaitGroup
claimLoop:
	for _, claim := range claims {
		select {
		case <-ctx.Done():
			errorsByClaim <- context.Cause(ctx)
			break claimLoop
		case semaphore <- struct{}{}:
		}
		group.Add(1)
		go func(claim TicketClaim) {
			defer group.Done()
			defer func() { <-semaphore }()
			if err := runner.handler.Handle(ctx, claim, now); err != nil {
				retryAt := runner.now().UTC().Add(runner.options.FailureBackoff)
				if retryErr := runner.queue.ScheduleTicketRetry(ctx, claim.Ticket.ID, claim.Token, retryAt); retryErr != nil &&
					!errors.Is(retryErr, tournament.ErrTicketClaimLost) {
					err = errors.Join(err, fmt.Errorf("schedule failed claim: %w", retryErr))
				}
				errorsByClaim <- fmt.Errorf("ticket %q: %w", claim.Ticket.ID, err)
			}
		}(claim)
	}
	group.Wait()
	close(errorsByClaim)
	var combined error
	for claimErr := range errorsByClaim {
		result.Failed++
		combined = errors.Join(combined, claimErr)
	}
	return result, combined
}
