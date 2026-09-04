package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/postgres"
)

func TestMigrationsApplyToPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, databaseURL, 2)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer pool.Close()

	applied, err := postgres.ApplyMigrations(ctx, pool)
	if err != nil {
		t.Fatalf("first ApplyMigrations() error = %v", err)
	}
	if applied < 0 || applied > 2 {
		t.Fatalf("first ApplyMigrations() = %d, want zero to two pending migrations", applied)
	}
	applied, err = postgres.ApplyMigrations(ctx, pool)
	if err != nil {
		t.Fatalf("second ApplyMigrations() error = %v", err)
	}
	if applied != 0 {
		t.Fatalf("second ApplyMigrations() = %d, want 0", applied)
	}

	for _, table := range []string{
		"schema_migrations",
		"tournament_configs",
		"matchmaking_tickets",
		"ticket_commands",
		"rooms",
		"sessions",
		"verified_results",
		"rating_updates",
		"outbox_events",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
			t.Fatalf("resolve table %q: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %q does not exist", table)
		}
	}

	var migrationCount int
	var minimumChecksumLength int
	if err := pool.QueryRow(ctx, "SELECT count(*), min(length(checksum)) FROM schema_migrations").Scan(&migrationCount, &minimumChecksumLength); err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	if migrationCount != 2 || minimumChecksumLength != 64 {
		t.Fatalf("migration count = %d, minimum checksum length = %d", migrationCount, minimumChecksumLength)
	}
}
