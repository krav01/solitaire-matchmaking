package worker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	MaxOutboxBatchSize = 256
	maxDeliveryError   = 1024
)

var ErrOutboxClaimLost = errors.New("outbox claim is no longer active")

type OutboxEvent struct {
	EventID          string          `json:"event_id"`
	AggregateType    string          `json:"aggregate_type"`
	AggregateID      string          `json:"aggregate_id"`
	AggregateVersion int64           `json:"aggregate_version"`
	EventType        string          `json:"event_type"`
	Payload          json.RawMessage `json:"payload"`
	OccurredAt       time.Time       `json:"occurred_at"`
}

func (event OutboxEvent) Validate() error {
	if event.EventID == "" || event.AggregateType == "" || event.AggregateID == "" || event.EventType == "" {
		return errors.New("outbox event identities are required")
	}
	if event.AggregateVersion <= 0 || event.OccurredAt.IsZero() {
		return errors.New("outbox event version and occurrence time are required")
	}
	if !json.Valid(event.Payload) || len(event.Payload) == 0 || event.Payload[0] != '{' {
		return errors.New("outbox event payload must be a JSON object")
	}

	return nil
}

type OutboxClaimRequest struct {
	Token      string
	Limit      int
	ClaimedAt  time.Time
	LeaseUntil time.Time
}

func (request OutboxClaimRequest) Validate() error {
	if request.Token == "" || request.Limit <= 0 || request.Limit > MaxOutboxBatchSize {
		return errors.New("outbox claim token and bounded batch size are required")
	}
	if request.ClaimedAt.IsZero() || !request.LeaseUntil.After(request.ClaimedAt) {
		return errors.New("outbox claim requires a positive lease")
	}

	return nil
}

type OutboxClaim struct {
	Event      OutboxEvent
	Token      string
	Attempt    int
	LeaseUntil time.Time
}

func (claim OutboxClaim) Validate() error {
	if err := claim.Event.Validate(); err != nil {
		return err
	}
	if claim.Token == "" || claim.Attempt <= 0 || claim.LeaseUntil.IsZero() {
		return errors.New("outbox claim is incomplete")
	}

	return nil
}

type OutboxQueue interface {
	ClaimOutboxEvents(context.Context, OutboxClaimRequest) ([]OutboxClaim, error)
	MarkOutboxDelivered(context.Context, string, string, time.Time) error
	ScheduleOutboxRetry(context.Context, string, string, time.Time, string) error
}

type OutboxPublisher interface {
	Publish(context.Context, OutboxEvent) error
}

type OutboxRunnerOptions struct {
	BatchSize      int
	Concurrency    int
	LeaseDuration  time.Duration
	PollInterval   time.Duration
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
	Observer       WorkerObserver
}

func (options OutboxRunnerOptions) Validate() error {
	if options.BatchSize <= 0 || options.BatchSize > MaxOutboxBatchSize {
		return errors.New("outbox batch size is outside the supported range")
	}
	if options.Concurrency <= 0 || options.Concurrency > options.BatchSize {
		return errors.New("outbox concurrency must fit inside the batch")
	}
	if options.LeaseDuration <= 0 || options.LeaseDuration > 5*time.Minute {
		return errors.New("outbox lease must be positive and at most five minutes")
	}
	if options.PollInterval <= 0 || options.PollInterval > time.Minute {
		return errors.New("outbox poll interval must be positive and at most one minute")
	}
	if options.RetryBaseDelay <= 0 || options.RetryMaxDelay < options.RetryBaseDelay || options.RetryMaxDelay > time.Hour {
		return errors.New("outbox retry delays are invalid")
	}

	return nil
}

type OutboxRunResult struct {
	Claimed   int
	Delivered int
	Failed    int
}

type OutboxRunner struct {
	queue     OutboxQueue
	publisher OutboxPublisher
	logger    *slog.Logger
	options   OutboxRunnerOptions
	observer  WorkerObserver
	now       func() time.Time
	token     func() string
}

func NewOutboxRunner(queue OutboxQueue, publisher OutboxPublisher, logger *slog.Logger, options OutboxRunnerOptions) (*OutboxRunner, error) {
	if queue == nil || publisher == nil || logger == nil {
		return nil, errors.New("outbox queue, publisher and logger are required")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}

	return &OutboxRunner{
		queue: queue, publisher: publisher, logger: logger, options: options,
		observer: configuredWorkerObserver(options.Observer), now: time.Now, token: rand.Text,
	}, nil
}

func (runner *OutboxRunner) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			result, err := runner.RunOnce(ctx)
			if err != nil {
				runner.logger.WarnContext(ctx, "outbox delivery cycle failed", "claimed", result.Claimed, "failed", result.Failed, "error", err)
			}
			timer.Reset(runner.options.PollInterval)
		}
	}
}

func (runner *OutboxRunner) RunOnce(ctx context.Context) (result OutboxRunResult, runErr error) {
	defer func() {
		runner.observer.ObserveWorkerCycle(WorkerCycleObservation{
			Worker: WorkerOutbox, Claimed: result.Claimed,
			Succeeded: result.Delivered, Failed: result.Failed,
			Errored: runErr != nil,
		})
	}()

	claimedAt := runner.now().UTC()
	claims, err := runner.queue.ClaimOutboxEvents(ctx, OutboxClaimRequest{
		Token: runner.token(), Limit: runner.options.BatchSize,
		ClaimedAt: claimedAt, LeaseUntil: claimedAt.Add(runner.options.LeaseDuration),
	})
	if err != nil {
		return OutboxRunResult{}, fmt.Errorf("claim outbox events: %w", err)
	}

	result = OutboxRunResult{Claimed: len(claims)}
	if len(claims) == 0 {
		return result, nil
	}

	semaphore := make(chan struct{}, runner.options.Concurrency)
	errorsByClaim := make(chan error, len(claims))
	delivered := make(chan struct{}, len(claims))
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
		go func(claim OutboxClaim) {
			defer group.Done()
			defer func() { <-semaphore }()

			if publishErr := runner.publisher.Publish(ctx, claim.Event); publishErr != nil {
				retryAt := runner.now().UTC().Add(runner.retryDelay(claim.Attempt))
				retryErr := runner.queue.ScheduleOutboxRetry(
					ctx, claim.Event.EventID, claim.Token, retryAt, boundedDeliveryError(publishErr),
				)
				if retryErr != nil && !errors.Is(retryErr, ErrOutboxClaimLost) {
					publishErr = errors.Join(publishErr, fmt.Errorf("schedule outbox retry: %w", retryErr))
				}
				errorsByClaim <- fmt.Errorf("event %q: %w", claim.Event.EventID, publishErr)
				return
			}

			deliveredAt := runner.now().UTC()
			if err := runner.queue.MarkOutboxDelivered(ctx, claim.Event.EventID, claim.Token, deliveredAt); err != nil {
				errorsByClaim <- fmt.Errorf("acknowledge event %q: %w", claim.Event.EventID, err)
				return
			}

			delivered <- struct{}{}
		}(claim)
	}

	group.Wait()
	close(errorsByClaim)
	close(delivered)

	for range delivered {
		result.Delivered++
	}

	var combined error
	for claimErr := range errorsByClaim {
		result.Failed++
		combined = errors.Join(combined, claimErr)
	}

	return result, combined
}

func (runner *OutboxRunner) retryDelay(attempt int) time.Duration {
	delay := runner.options.RetryBaseDelay
	for current := 1; current < attempt && delay < runner.options.RetryMaxDelay; current++ {
		if delay > runner.options.RetryMaxDelay/2 {
			return runner.options.RetryMaxDelay
		}
		delay *= 2
	}

	return min(delay, runner.options.RetryMaxDelay)
}

func boundedDeliveryError(err error) string {
	message := []rune(err.Error())
	if len(message) > maxDeliveryError {
		message = message[:maxDeliveryError]
	}

	return string(message)
}
