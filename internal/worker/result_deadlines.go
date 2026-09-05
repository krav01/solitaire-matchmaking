package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

type ResultDeadlineService interface {
	ExpireDue(context.Context, tournament.ResultDeadlineBatch) ([]tournament.ExpiredRoom, error)
}

type ResultDeadlineOptions struct {
	BatchSize    int
	PollInterval time.Duration
}

func (options ResultDeadlineOptions) Validate() error {
	if options.BatchSize <= 0 || options.BatchSize > tournament.MaxResultDeadlineBatchSize {
		return errors.New("result deadline batch size is outside the supported range")
	}
	if options.PollInterval <= 0 || options.PollInterval > time.Minute {
		return errors.New("result deadline poll interval must be positive and at most one minute")
	}
	return nil
}

type ResultDeadlineRunner struct {
	service ResultDeadlineService
	logger  *slog.Logger
	options ResultDeadlineOptions
	now     func() time.Time
}

func NewResultDeadlineRunner(service ResultDeadlineService, logger *slog.Logger, options ResultDeadlineOptions) (*ResultDeadlineRunner, error) {
	if service == nil || logger == nil {
		return nil, errors.New("result deadline service and logger are required")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &ResultDeadlineRunner{service: service, logger: logger, options: options, now: time.Now}, nil
}

func (runner *ResultDeadlineRunner) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if _, err := runner.RunOnce(ctx); err != nil {
				runner.logger.WarnContext(ctx, "result deadline cycle failed", "error", err)
			}
			timer.Reset(runner.options.PollInterval)
		}
	}
}

func (runner *ResultDeadlineRunner) RunOnce(ctx context.Context) (int, error) {
	expired, err := runner.service.ExpireDue(ctx, tournament.ResultDeadlineBatch{
		Limit: runner.options.BatchSize, ExpiredAt: runner.now().UTC(),
	})
	if err != nil {
		return 0, fmt.Errorf("expire overdue result rooms: %w", err)
	}
	return len(expired), nil
}
