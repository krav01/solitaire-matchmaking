package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/postgres"
)

const migrationTimeout = 2 * time.Minute

func main() {
	os.Exit(run(context.Background(), os.Getenv, os.Stderr))
}

func run(parent context.Context, getenv func(string) string, stderr io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	databaseURL := strings.TrimSpace(getenv("DATABASE_URL"))
	if databaseURL == "" {
		_, _ = fmt.Fprintln(stderr, "DATABASE_URL is required")
		return 2
	}

	ctx, cancel := context.WithTimeout(parent, migrationTimeout)
	defer cancel()
	pool, err := postgres.Open(ctx, databaseURL, 1)
	if err != nil {
		logger.Error("open PostgreSQL for migrations", "error", err)
		return 1
	}
	defer pool.Close()

	applied, err := postgres.ApplyMigrations(ctx, pool)
	if err != nil {
		logger.Error("apply PostgreSQL migrations", "error", err)
		return 1
	}
	logger.Info("PostgreSQL migrations complete", "applied", applied)
	return 0
}
