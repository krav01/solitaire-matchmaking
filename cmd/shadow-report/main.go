package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(context.Background(), os.Args[1:], os.Getenv, os.Stdout); err != nil {
		logger.Error("rating shadow report failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, getenv func(string) string, output io.Writer) error {
	flags := flag.NewFlagSet("shadow-report", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	candidateVersion := flags.String("candidate-version", "", "immutable candidate model version")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if strings.TrimSpace(*candidateVersion) == "" {
		return errors.New("-candidate-version is required")
	}
	databaseURL := getenv("DATABASE_URL")
	if strings.TrimSpace(databaseURL) == "" {
		return errors.New("DATABASE_URL is required")
	}
	var policy rating.ModelComparisonPolicy
	if err := json.Unmarshal([]byte(getenv("RATING_SHADOW_COMPARISON_POLICY")), &policy); err != nil {
		return fmt.Errorf("decode RATING_SHADOW_COMPARISON_POLICY: %w", err)
	}
	binCount := 10
	if value := getenv("RATING_SHADOW_BIN_COUNT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return errors.New("RATING_SHADOW_BIN_COUNT must be an integer")
		}
		binCount = parsed
	}

	startupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := postgres.Open(startupCtx, databaseURL, 2)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()
	store, err := postgres.NewRatingShadowReportStore(pool)
	if err != nil {
		return err
	}
	report, err := store.BuildComparison(ctx, postgres.RatingShadowComparisonRequest{
		CandidateVersion: *candidateVersion, BinCount: binCount, Policy: policy,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode rating shadow report: %w", err)
	}
	return nil
}
