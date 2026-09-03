// Package observability owns process logging and future metrics adapters.
package observability

import (
	"io"
	"log/slog"
)

func NewLogger(output io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level})).With("service", "solitaire-matchmaking")
}
