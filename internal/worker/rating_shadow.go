package worker

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

var ErrRatingShadowClaimLost = errors.New("rating shadow claim is no longer active")

const WorkerRatingShadow = "rating_shadow"

type RatingShadowClaim struct {
	Kind       string
	SourceID   string
	Token      string
	Attempt    int
	LeaseUntil time.Time
}

func (claim RatingShadowClaim) Validate() error {
	if (claim.Kind != "room" && claim.Kind != "result") || claim.SourceID == "" || claim.Token == "" || claim.Attempt <= 0 || claim.LeaseUntil.IsZero() {
		return errors.New("rating shadow claim is incomplete")
	}
	return nil
}

type RatingShadowQueue interface {
	ClaimNextRatingShadowWork(context.Context, RatingClaimRequest) (*RatingShadowClaim, error)
	ProcessRatingShadowWork(context.Context, RatingShadowClaim, time.Time) (int, error)
	ScheduleRatingShadowRetry(context.Context, RatingShadowClaim, time.Time) error
}

type RatingShadowRunnerOptions struct {
	LeaseDuration  time.Duration
	PollInterval   time.Duration
	FailureBackoff time.Duration
	Observer       WorkerObserver
}

func (options RatingShadowRunnerOptions) Validate() error {
	return RatingRunnerOptions{
		LeaseDuration: options.LeaseDuration, PollInterval: options.PollInterval,
		FailureBackoff: options.FailureBackoff,
	}.Validate()
}

type RatingShadowRunResult struct {
	Claimed   int
	Persisted int
	Failed    int
}

type RatingShadowRunner struct {
	queue    RatingShadowQueue
	logger   *slog.Logger
	options  RatingShadowRunnerOptions
	observer WorkerObserver
	now      func() time.Time
	token    func() string
}

func NewRatingShadowRunner(queue RatingShadowQueue, logger *slog.Logger, options RatingShadowRunnerOptions) (*RatingShadowRunner, error) {
	if queue == nil || logger == nil {
		return nil, errors.New("rating shadow worker queue and logger are required")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &RatingShadowRunner{
		queue: queue, logger: logger, options: options,
		observer: configuredWorkerObserver(options.Observer), now: time.Now, token: rand.Text,
	}, nil
}

func (runner *RatingShadowRunner) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			result, err := runner.RunOnce(ctx)
			if err != nil {
				runner.logger.WarnContext(ctx, "rating shadow worker cycle failed", "claimed", result.Claimed, "failed", result.Failed, "error", err)
			}
			timer.Reset(runner.options.PollInterval)
		}
	}
}

func (runner *RatingShadowRunner) RunOnce(ctx context.Context) (result RatingShadowRunResult, runErr error) {
	defer func() {
		runner.observer.ObserveWorkerCycle(WorkerCycleObservation{
			Worker: WorkerRatingShadow, Claimed: result.Claimed,
			Succeeded: result.Claimed - result.Failed, Failed: result.Failed,
			Errored: runErr != nil,
		})
	}()

	now := runner.now().UTC()
	claim, err := runner.queue.ClaimNextRatingShadowWork(ctx, RatingClaimRequest{
		Token: runner.token(), ClaimedAt: now, LeaseUntil: now.Add(runner.options.LeaseDuration),
	})
	if err != nil {
		return result, fmt.Errorf("claim next rating shadow work: %w", err)
	}
	if claim == nil {
		return result, nil
	}
	result.Claimed = 1
	persisted, err := runner.queue.ProcessRatingShadowWork(ctx, *claim, now)
	if err == nil {
		result.Persisted = persisted
		return result, nil
	}
	result.Failed = 1
	retryAt := runner.now().UTC().Add(runner.options.FailureBackoff)
	if retryErr := runner.queue.ScheduleRatingShadowRetry(ctx, *claim, retryAt); retryErr != nil &&
		!errors.Is(retryErr, ErrRatingShadowClaimLost) {
		err = errors.Join(err, fmt.Errorf("schedule failed rating shadow work: %w", retryErr))
	}
	return result, fmt.Errorf("%s %q: %w", claim.Kind, claim.SourceID, err)
}
