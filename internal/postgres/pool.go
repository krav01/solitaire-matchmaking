// Package postgres implements PostgreSQL infrastructure adapters.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open verifies connectivity before returning a pool. Errors intentionally omit
// the connection string: driver parse/connection errors may contain credentials.
func Open(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	if maxConns <= 0 {
		return nil, errors.New("database connection limit must be positive")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("cannot parse DATABASE_URL")
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("cannot initialize PostgreSQL pool")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("PostgreSQL connectivity check failed")
	}
	return pool, nil
}
