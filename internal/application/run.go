// Package application coordinates use cases and composes process dependencies.
package application

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/krav01/solitaire-matchmaking/internal/config"
	"github.com/krav01/solitaire-matchmaking/internal/httpapi"
	"github.com/krav01/solitaire-matchmaking/internal/postgres"
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
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}
	logger.Info("server started", "address", listener.Addr().String(), "stage", "foundation")
	return server.Serve(ctx, listener)
}
