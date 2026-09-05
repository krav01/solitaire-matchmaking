package worker

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

var ErrRatingClaimLost = errors.New("rating claim is no longer active")

type RatingClaimRequest struct {
	Token      string
	ClaimedAt  time.Time
	LeaseUntil time.Time
}

func (request RatingClaimRequest) Validate() error {
	if request.Token == "" || request.ClaimedAt.IsZero() || !request.LeaseUntil.After(request.ClaimedAt) {
		return errors.New("rating claim requires a token and a positive lease")
	}
	return nil
}

type RatingClaim struct {
	EventID    string
	Token      string
	Attempt    int
	LeaseUntil time.Time
}

func (claim RatingClaim) Validate() error {
	if claim.EventID == "" || claim.Token == "" || claim.Attempt <= 0 || claim.LeaseUntil.IsZero() {
		return errors.New("rating claim is incomplete")
	}
	return nil
}

type RatingQueue interface {
	ClaimNextRatingResult(context.Context, RatingClaimRequest) (*RatingClaim, error)
	ProcessRatingResult(context.Context, RatingClaim, time.Time) (int, error)
	ScheduleRatingRetry(context.Context, string, string, time.Time) error
}

type RatingRunnerOptions struct {
	LeaseDuration  time.Duration
	PollInterval   time.Duration
	FailureBackoff time.Duration
	Observer       WorkerObserver
}

func (options RatingRunnerOptions) Validate() error {
	if options.LeaseDuration <= 0 || options.LeaseDuration > 5*time.Minute {
		return errors.New("rating worker lease must be positive and at most five minutes")
	}
	if options.PollInterval <= 0 || options.PollInterval > time.Minute ||
		options.FailureBackoff <= 0 || options.FailureBackoff > time.Minute {
		return errors.New("rating worker polling and failure backoff must be positive and at most one minute")
	}
	return nil
}

type RatingRunResult struct {
	Claimed int
	Updated int
	Failed  int
}

type RatingRunner struct {
	queue    RatingQueue
	logger   *slog.Logger
	options  RatingRunnerOptions
	observer WorkerObserver
	now      func() time.Time
	token    func() string
}

func NewRatingRunner(queue RatingQueue, logger *slog.Logger, options RatingRunnerOptions) (*RatingRunner, error) {
	if queue == nil || logger == nil {
		return nil, errors.New("rating worker queue and logger are required")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &RatingRunner{
		queue: queue, logger: logger, options: options,
		observer: configuredWorkerObserver(options.Observer), now: time.Now, token: rand.Text,
	}, nil
}

func (runner *RatingRunner) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			result, err := runner.RunOnce(ctx)
			if err != nil {
				runner.logger.WarnContext(ctx, "rating worker cycle failed", "claimed", result.Claimed, "failed", result.Failed, "error", err)
			}
			timer.Reset(runner.options.PollInterval)
		}
	}
}

func (runner *RatingRunner) RunOnce(ctx context.Context) (result RatingRunResult, runErr error) {
	defer func() {
		runner.observer.ObserveWorkerCycle(WorkerCycleObservation{
			Worker: WorkerRating, Claimed: result.Claimed,
			Succeeded: result.Claimed - result.Failed, Failed: result.Failed,
			Errored: runErr != nil,
		})
	}()

	now := runner.now().UTC()
	claim, err := runner.queue.ClaimNextRatingResult(ctx, RatingClaimRequest{
		Token: runner.token(), ClaimedAt: now, LeaseUntil: now.Add(runner.options.LeaseDuration),
	})
	if err != nil {
		return RatingRunResult{}, fmt.Errorf("claim next rating result: %w", err)
	}
	if claim == nil {
		return RatingRunResult{}, nil
	}
	result = RatingRunResult{Claimed: 1}
	updated, err := runner.queue.ProcessRatingResult(ctx, *claim, now)
	if err == nil {
		result.Updated = updated
		return result, nil
	}
	result.Failed = 1
	retryAt := runner.now().UTC().Add(runner.options.FailureBackoff)
	if retryErr := runner.queue.ScheduleRatingRetry(ctx, claim.EventID, claim.Token, retryAt); retryErr != nil &&
		!errors.Is(retryErr, ErrRatingClaimLost) {
		err = errors.Join(err, fmt.Errorf("schedule failed rating result: %w", retryErr))
	}
	return result, fmt.Errorf("result %q: %w", claim.EventID, err)
}
