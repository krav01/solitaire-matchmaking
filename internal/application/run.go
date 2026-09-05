// Package application coordinates use cases and composes process dependencies.
package application

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/krav01/solitaire-matchmaking/internal/config"
	"github.com/krav01/solitaire-matchmaking/internal/httpapi"
	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	startupCtx, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	pool, err := postgres.Open(startupCtx, cfg.DatabaseURL, cfg.DBMaxConns)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()
	ticketStore, err := postgres.NewTicketStore(pool)
	if err != nil {
		return err
	}
	resultStore, err := postgres.NewResultStore(pool)
	if err != nil {
		return err
	}
	resultService, err := tournament.NewResultService(resultStore)
	if err != nil {
		return err
	}
	server, err := httpapi.New(pool, resultService, logger, httpapi.Options{
		APIToken: cfg.APIToken, ReadinessTimeout: cfg.ReadinessTimeout, ShutdownTimeout: cfg.ShutdownTimeout,
	})
	if err != nil {
		return err
	}
	ticketService, err := tournament.NewTicketService(ticketStore)
	if err != nil {
		return err
	}
	deadlineRunner, err := worker.NewResultDeadlineRunner(resultService, logger, worker.ResultDeadlineOptions{
		BatchSize: cfg.ResultDeadlineBatch, PollInterval: cfg.ResultDeadlinePoll,
	})
	if err != nil {
		return err
	}
	ratingQueue, err := postgres.NewRatingQueue(pool)
	if err != nil {
		return err
	}
	ratingRunner, err := worker.NewRatingRunner(ratingQueue, logger, worker.RatingRunnerOptions{
		LeaseDuration: cfg.RatingLease, PollInterval: cfg.RatingPollInterval,
		FailureBackoff: cfg.RatingFailureBackoff,
	})
	if err != nil {
		return err
	}
	matchQueue, err := postgres.NewMatchmakingQueue(pool)
	if err != nil {
		return err
	}
	processor, err := worker.NewMatchProcessor(matchQueue, matchQueue, ticketService, cfg.MatchStaleRetryDelay)
	if err != nil {
		return err
	}
	runner, err := worker.NewRunner(matchQueue, processor, logger, worker.RunnerOptions{
		BatchSize: cfg.MatchBatchSize, Concurrency: cfg.MatchConcurrency,
		LeaseDuration: cfg.MatchLease, PollInterval: cfg.MatchPollInterval,
		FailureBackoff: cfg.MatchFailureBackoff,
	})
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		runner.Run(workerCtx)
	}()
	go func() {
		defer workers.Done()
		deadlineRunner.Run(workerCtx)
	}()
	go func() {
		defer workers.Done()
		ratingRunner.Run(workerCtx)
	}()
	defer func() {
		stopWorker()
		workers.Wait()
	}()
	logger.Info("server started", "address", listener.Addr().String(), "stage", "baseline_rating_persistence")
	return server.Serve(ctx, listener)
}
