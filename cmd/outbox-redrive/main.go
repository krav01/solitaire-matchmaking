package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/postgres"
)

const operationTimeout = 15 * time.Second

func main() {
	os.Exit(run(context.Background(), os.Getenv, os.Args[1:], os.Stderr))
}

func run(parent context.Context, getenv func(string) string, args []string, stderr *os.File) int {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		_, _ = fmt.Fprintln(stderr, "usage: outbox-redrive <event_id>")
		return 2
	}
	databaseURL := strings.TrimSpace(getenv("DATABASE_URL"))
	if databaseURL == "" {
		_, _ = fmt.Fprintln(stderr, "DATABASE_URL is required")
		return 2
	}

	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	ctx, cancel := context.WithTimeout(parent, operationTimeout)
	defer cancel()
	pool, err := postgres.Open(ctx, databaseURL, 1)
	if err != nil {
		logger.Error("open PostgreSQL", "error", err)
		return 1
	}
	defer pool.Close()
	queue, err := postgres.NewOutboxQueue(pool)
	if err != nil {
		logger.Error("create outbox queue", "error", err)
		return 1
	}
	if err := queue.RedriveOutboxDeadLetter(ctx, args[0], time.Now().UTC()); err != nil {
		logger.Error("re-drive outbox dead-letter", "event_id", args[0], "error", err)
		return 1
	}
	logger.Info("outbox dead-letter scheduled for re-delivery", "event_id", args[0])
	return 0
}
