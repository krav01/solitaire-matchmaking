package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/migrations"
)

const migrationAdvisoryLock int64 = 7_074_651_911_357_401_243

const createMigrationsTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT schema_migrations_version_positive CHECK (version > 0),
    CONSTRAINT schema_migrations_name_present CHECK (name <> ''),
    CONSTRAINT schema_migrations_checksum_sha256 CHECK (checksum ~ '^[0-9a-f]{64}$')
)`

type migrationTx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type migrationStarter interface {
	Begin(ctx context.Context) (migrationTx, error)
}

type poolMigrationStarter struct {
	pool *pgxpool.Pool
}

func (starter poolMigrationStarter) Begin(ctx context.Context) (migrationTx, error) {
	return starter.pool.Begin(ctx)
}

// ApplyMigrations applies every pending embedded migration in one transaction.
// It must be called by an explicit migration process, never service startup.
func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	if pool == nil {
		return 0, errors.New("PostgreSQL pool is required")
	}
	catalog, err := migrations.Load()
	if err != nil {
		return 0, err
	}
	return applyMigrations(ctx, poolMigrationStarter{pool: pool}, catalog)
}

func applyMigrations(ctx context.Context, starter migrationStarter, catalog []migrations.Migration) (int, error) {
	tx, err := starter.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationAdvisoryLock); err != nil {
		return 0, fmt.Errorf("acquire migration lock: %w", err)
	}
	if _, err := tx.Exec(ctx, createMigrationsTableSQL); err != nil {
		return 0, fmt.Errorf("create migration ledger: %w", err)
	}

	applied := 0
	for _, migration := range catalog {
		var storedName string
		var storedChecksum string
		err := tx.QueryRow(
			ctx,
			"SELECT name, checksum FROM schema_migrations WHERE version = $1",
			migration.Version,
		).Scan(&storedName, &storedChecksum)
		switch {
		case err == nil:
			if storedName != migration.Name || storedChecksum != migration.Checksum {
				return 0, fmt.Errorf("migration %d differs from the applied checksum", migration.Version)
			}
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			return 0, fmt.Errorf("read migration %d state: %w", migration.Version, err)
		}

		if _, err := tx.Exec(ctx, migration.SQL); err != nil {
			return 0, fmt.Errorf("apply migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		if _, err := tx.Exec(
			ctx,
			"INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)",
			migration.Version,
			migration.Name,
			migration.Checksum,
		); err != nil {
			return 0, fmt.Errorf("record migration %d: %w", migration.Version, err)
		}
		applied++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit migrations: %w", err)
	}
	return applied, nil
}
