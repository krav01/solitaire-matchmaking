package postgres_test

import (
	"context"
	"testing"

	"github.com/krav01/solitaire-matchmaking/internal/postgres"
)

func TestOpenRejectsInvalidConfigurationBeforeConnecting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		dsn      string
		maxConns int32
	}{
		{name: "empty connection limit", dsn: "postgres://localhost/database", maxConns: 0},
		{name: "malformed database URL", dsn: "postgres://[invalid", maxConns: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pool, err := postgres.Open(context.Background(), tt.dsn, tt.maxConns)
			if err == nil {
				pool.Close()
				t.Fatal("Open() expected an error")
			}
		})
	}
}
