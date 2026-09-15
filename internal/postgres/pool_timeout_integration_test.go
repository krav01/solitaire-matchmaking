package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/postgres"
)

func TestOpenConfiguresDatabaseTimeouts(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, databaseURL, 1)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer pool.Close()

	var statementTimeout, lockTimeout string
	if err := pool.QueryRow(ctx, "SHOW statement_timeout").Scan(&statementTimeout); err != nil {
		t.Fatalf("SHOW statement_timeout: %v", err)
	}
	if err := pool.QueryRow(ctx, "SHOW lock_timeout").Scan(&lockTimeout); err != nil {
		t.Fatalf("SHOW lock_timeout: %v", err)
	}
	if statementTimeout != "1500ms" {
		t.Fatalf("statement_timeout = %q, want 1500ms", statementTimeout)
	}
	if lockTimeout != "500ms" {
		t.Fatalf("lock_timeout = %q, want 500ms", lockTimeout)
	}
}
