package config_test

import (
	"strings"
	"testing"

	"github.com/krav01/solitaire-matchmaking/internal/config"
)

func TestLoad(t *testing.T) {
	t.Parallel()
	base := map[string]string{
		"DATABASE_URL": "test-database-address",
		"API_TOKEN":    strings.Repeat("x", 32),
	}
	tests := []struct {
		name    string
		values  map[string]string
		wantErr bool
	}{
		{name: "valid defaults"},
		{name: "valid overrides", values: map[string]string{"HTTP_ADDR": "0.0.0.0:9090", "DB_MAX_CONNS": "20", "LOG_LEVEL": "debug"}},
		{name: "valid worker overrides", values: map[string]string{"MATCH_WORKER_BATCH_SIZE": "16", "MATCH_WORKER_CONCURRENCY": "4", "MATCH_WORKER_LEASE": "5s"}},
		{name: "missing database URL", values: map[string]string{"DATABASE_URL": ""}, wantErr: true},
		{name: "short API token", values: map[string]string{"API_TOKEN": "short"}, wantErr: true},
		{name: "invalid listen port", values: map[string]string{"HTTP_ADDR": "127.0.0.1:70000"}, wantErr: true},
		{name: "unbounded connection pool", values: map[string]string{"DB_MAX_CONNS": "1001"}, wantErr: true},
		{name: "long readiness timeout", values: map[string]string{"READINESS_TIMEOUT": "6s"}, wantErr: true},
		{name: "concurrency exceeds batch", values: map[string]string{"MATCH_WORKER_BATCH_SIZE": "2", "MATCH_WORKER_CONCURRENCY": "3"}, wantErr: true},
		{name: "stale retry is too slow", values: map[string]string{"MATCH_WORKER_STALE_RETRY_DELAY": "2s"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values := make(map[string]string, len(base)+len(tt.values))
			for key, value := range base {
				values[key] = value
			}
			for key, value := range tt.values {
				values[key] = value
			}
			got, err := config.Load(func(key string) string { return values[key] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got.HTTPAddr == "" {
				t.Fatal("Load() returned an empty HTTP address")
			}
		})
	}
}
