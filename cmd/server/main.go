package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/krav01/solitaire-matchmaking/internal/application"
	"github.com/krav01/solitaire-matchmaking/internal/config"
	"github.com/krav01/solitaire-matchmaking/internal/observability"
)

func main() {
	if err := run(); err != nil {
		observability.NewLogger(os.Stderr, slog.LevelError).Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := observability.NewLogger(os.Stdout, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return application.Run(ctx, cfg, logger)
}
