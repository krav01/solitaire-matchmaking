// Package config loads and validates process configuration without global state.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr             string
	DatabaseURL          string
	APIToken             string
	DBMaxConns           int32
	StartupTimeout       time.Duration
	ReadinessTimeout     time.Duration
	ShutdownTimeout      time.Duration
	LogLevel             slog.Level
	MatchBatchSize       int
	MatchConcurrency     int
	MatchLease           time.Duration
	MatchPollInterval    time.Duration
	MatchFailureBackoff  time.Duration
	MatchStaleRetryDelay time.Duration
}

// Load accepts an environment accessor to keep tests isolated from os.Environ.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		HTTPAddr: "127.0.0.1:8080", DatabaseURL: getenv("DATABASE_URL"),
		APIToken: getenv("API_TOKEN"), DBMaxConns: 10,
		StartupTimeout: 10 * time.Second, ReadinessTimeout: 2 * time.Second,
		ShutdownTimeout: 10 * time.Second, LogLevel: slog.LevelInfo,
		MatchBatchSize: 32, MatchConcurrency: 8, MatchLease: 10 * time.Second,
		MatchPollInterval: 100 * time.Millisecond, MatchFailureBackoff: time.Second,
		MatchStaleRetryDelay: 50 * time.Millisecond,
	}
	if value := getenv("HTTP_ADDR"); value != "" {
		c.HTTPAddr = value
	}
	if value := getenv("DB_MAX_CONNS"); value != "" {
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil || n < 1 || n > 1000 {
			return Config{}, errors.New("DB_MAX_CONNS must be between 1 and 1000")
		}
		c.DBMaxConns = int32(n)
	}
	for _, entry := range []struct {
		key    string
		target *int
		max    int
	}{
		{"MATCH_WORKER_BATCH_SIZE", &c.MatchBatchSize, 256},
		{"MATCH_WORKER_CONCURRENCY", &c.MatchConcurrency, 256},
	} {
		if value := getenv(entry.key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > entry.max {
				return Config{}, fmt.Errorf("%s must be between 1 and %d", entry.key, entry.max)
			}
			*entry.target = n
		}
	}
	for _, entry := range []struct {
		key    string
		target *time.Duration
	}{
		{"STARTUP_TIMEOUT", &c.StartupTimeout},
		{"READINESS_TIMEOUT", &c.ReadinessTimeout},
		{"SHUTDOWN_TIMEOUT", &c.ShutdownTimeout},
		{"MATCH_WORKER_LEASE", &c.MatchLease},
		{"MATCH_WORKER_POLL_INTERVAL", &c.MatchPollInterval},
		{"MATCH_WORKER_FAILURE_BACKOFF", &c.MatchFailureBackoff},
		{"MATCH_WORKER_STALE_RETRY_DELAY", &c.MatchStaleRetryDelay},
	} {
		if value := getenv(entry.key); value != "" {
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 || d > time.Minute {
				return Config{}, fmt.Errorf("%s must be a positive duration up to one minute", entry.key)
			}
			*entry.target = d
		}
	}
	if c.ReadinessTimeout > 5*time.Second {
		return Config{}, errors.New("READINESS_TIMEOUT cannot exceed five seconds")
	}
	if c.MatchConcurrency > c.MatchBatchSize {
		return Config{}, errors.New("MATCH_WORKER_CONCURRENCY cannot exceed MATCH_WORKER_BATCH_SIZE")
	}
	if c.MatchStaleRetryDelay > time.Second {
		return Config{}, errors.New("MATCH_WORKER_STALE_RETRY_DELAY cannot exceed one second")
	}
	if value := getenv("LOG_LEVEL"); value != "" {
		if err := c.LogLevel.UnmarshalText([]byte(value)); err != nil {
			return Config{}, errors.New("invalid LOG_LEVEL")
		}
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if len(c.APIToken) < 32 || strings.ContainsAny(c.APIToken, " \r\n\t") {
		return Config{}, errors.New("API_TOKEN must contain at least 32 characters without whitespace")
	}
	_, port, err := net.SplitHostPort(c.HTTPAddr)
	if err != nil {
		return Config{}, errors.New("HTTP_ADDR must be a host:port address")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Config{}, errors.New("HTTP_ADDR port must be between 1 and 65535")
	}
	return c, nil
}
