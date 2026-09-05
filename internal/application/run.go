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
	server, err := httpapi.New(pool, logger, httpapi.Options{
		APIToken: cfg.APIToken, ReadinessTimeout: cfg.ReadinessTimeout, ShutdownTimeout: cfg.ShutdownTimeout,
	})
	if err != nil {
		return err
	}
	ticketStore, err := postgres.NewTicketStore(pool)
	if err != nil {
		return err
	}
	ticketService, err := tournament.NewTicketService(ticketStore)
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
	workers.Add(1)
	go func() {
		defer workers.Done()
		runner.Run(workerCtx)
	}()
	defer func() {
		stopWorker()
		workers.Wait()
	}()
	logger.Info("server started", "address", listener.Addr().String(), "stage", "transactional_lifecycle")
	return server.Serve(ctx, listener)
}
