package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestRatingShadowPostgreSQLOrdersRoomBeforeSameTimeResult(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, databaseURL, 4)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer pool.Close()
	if _, err := postgres.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}

	prefix := fmt.Sprintf("shadow-order-%d", time.Now().UnixNano())
	position := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	mustExec(t, ctx, pool, `
INSERT INTO rating_shadow_work (
    work_kind, source_id, timeline_position, ordering_priority, next_attempt_at
) VALUES
    ('result', $1, $3, 1, $3),
    ('room', $2, $3, 0, $3)`, prefix+"-result", prefix+"-room", position)
	queue, err := postgres.NewRatingShadowQueue(pool)
	if err != nil {
		t.Fatalf("NewRatingShadowQueue() error = %v", err)
	}
	now := time.Now().UTC()
	first, err := queue.ClaimNextRatingShadowWork(ctx, worker.RatingClaimRequest{
		Token: prefix + "-first", ClaimedAt: now, LeaseUntil: now.Add(time.Minute),
	})
	if err != nil || first == nil || first.Kind != "room" || first.SourceID != prefix+"-room" {
		t.Fatalf("first claim = %+v, error = %v", first, err)
	}
	blocked, err := queue.ClaimNextRatingShadowWork(ctx, worker.RatingClaimRequest{
		Token: prefix + "-blocked", ClaimedAt: now, LeaseUntil: now.Add(time.Minute),
	})
	if err != nil || blocked != nil {
		t.Fatalf("claim behind active head = %+v, error = %v", blocked, err)
	}
	if _, err := queue.ProcessRatingShadowWork(ctx, *first, now); err != nil {
		t.Fatalf("ProcessRatingShadowWork(first) error = %v", err)
	}
	if _, err := queue.ProcessRatingShadowWork(ctx, *first, now); !errors.Is(err, worker.ErrRatingShadowClaimLost) {
		t.Fatalf("ProcessRatingShadowWork(replay) error = %v", err)
	}
	second, err := queue.ClaimNextRatingShadowWork(ctx, worker.RatingClaimRequest{
		Token: prefix + "-second", ClaimedAt: now, LeaseUntil: now.Add(time.Minute),
	})
	if err != nil || second == nil || second.Kind != "result" || second.SourceID != prefix+"-result" {
		t.Fatalf("second claim = %+v, error = %v", second, err)
	}
	if _, err := queue.ProcessRatingShadowWork(ctx, *second, now); err != nil {
		t.Fatalf("ProcessRatingShadowWork(second) error = %v", err)
	}
}

func TestRatingShadowPostgreSQLTimelineAndIsolation(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, databaseURL, 8)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer pool.Close()
	if _, err := postgres.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}

	prefix := fmt.Sprintf("shadow-%d", time.Now().UnixNano())
	baselineVersion := prefix + "-baseline"
	candidateVersion := prefix + "-candidate"
	tournamentID := prefix + "-tournament"
	roomID := prefix + "-room"
	startedAt := time.Now().UTC().Add(-20 * time.Second).Truncate(time.Millisecond)
	seedLifecycleConfig(t, ctx, pool, baselineVersion, prefix+"-policy", tournamentID, "v1", roomID, startedAt)
	mustExec(t, ctx, pool, "UPDATE rooms SET deck_version = 'deck-v1' WHERE room_id = $1", roomID)
	seedShadowDeployment(t, ctx, pool, candidateVersion, baselineVersion, startedAt)
	if _, err := pool.Exec(ctx, `
UPDATE rating_shadow_deployments
SET definition = '{}'::jsonb
WHERE candidate_version = $1`, candidateVersion); err == nil {
		t.Fatal("rating shadow deployment definition update succeeded")
	}

	ticketStore, err := postgres.NewTicketStore(pool)
	if err != nil {
		t.Fatalf("NewTicketStore() error = %v", err)
	}
	ticketService, err := tournament.NewTicketService(ticketStore)
	if err != nil {
		t.Fatalf("NewTicketService() error = %v", err)
	}
	matchQueue, err := postgres.NewMatchmakingQueue(pool)
	if err != nil {
		t.Fatalf("NewMatchmakingQueue() error = %v", err)
	}
	processor, err := worker.NewMatchProcessor(matchQueue, matchQueue, ticketService, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("NewMatchProcessor() error = %v", err)
	}
	for index := range 5 {
		command := lifecycleAcceptCommand(fmt.Sprintf("%s-player-%d", prefix, index), tournamentID, "v1", baselineVersion, startedAt)
		if _, err := ticketService.Accept(ctx, command); err != nil {
			t.Fatalf("Accept(player %d) error = %v", index, err)
		}
	}
	evaluatedAt := startedAt.Add(time.Second)
	for round := range 10 {
		claims, err := matchQueue.ClaimMatchmakingTickets(ctx, worker.ClaimRequest{
			Token: fmt.Sprintf("%s-match-%d", prefix, round), Limit: 5,
			ClaimedAt: evaluatedAt, LeaseUntil: time.Now().UTC().Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("ClaimMatchmakingTickets() error = %v", err)
		}
		for _, claim := range claims {
			if err := processor.Handle(ctx, claim, evaluatedAt); err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
		}
		if countQueuedTickets(t, ctx, pool, tournamentID) == 0 {
			break
		}
		evaluatedAt = evaluatedAt.Add(100 * time.Millisecond)
	}

	shadowQueue, err := postgres.NewRatingShadowQueue(pool)
	if err != nil {
		t.Fatalf("NewRatingShadowQueue() error = %v", err)
	}
	processShadowThrough(t, ctx, shadowQueue, "room", roomID)
	var predictions int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM rating_shadow_predictions
WHERE room_id = $1 AND candidate_version = $2`, roomID, candidateVersion).Scan(&predictions); err != nil {
		t.Fatalf("count shadow predictions: %v", err)
	}
	if predictions != 1 {
		t.Fatalf("shadow predictions = %d, want 1", predictions)
	}

	participants := loadRoomParticipants(t, ctx, pool, roomID)
	for index := range participants {
		score := int64(1000 - index*100)
		participants[index].Features.Score = &score
	}
	resultStore, err := postgres.NewResultStore(pool)
	if err != nil {
		t.Fatalf("NewResultStore() error = %v", err)
	}
	resultService, err := tournament.NewResultService(resultStore)
	if err != nil {
		t.Fatalf("NewResultService() error = %v", err)
	}
	availableAt := startedAt.Add(5 * time.Second)
	eventID := prefix + "-result"
	if _, err := resultService.Finalize(ctx, tournament.FinalizeResultCommand{
		EventID: eventID, RoomID: roomID, ModeID: "solitaire", DeckID: roomID + "-deck",
		ScoringRulesVersion: "scoring-v1", FinishedAt: availableAt.Add(-time.Second),
		AvailableAt: availableAt, AcceptedAt: availableAt, Participants: participants,
	}); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	processShadowThrough(t, ctx, shadowQueue, "result", eventID)

	var observations, updates, states, activeCandidateRows int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM rating_shadow_observations WHERE source_event_id = $1),
    (SELECT count(*) FROM rating_shadow_updates WHERE source_event_id = $1),
    (SELECT count(*) FROM rating_shadow_player_states WHERE candidate_version = $2 AND player_id LIKE $3),
    (SELECT count(*) FROM player_ratings WHERE model_version = $2 AND player_id LIKE $3)`,
		eventID, candidateVersion, prefix+"-%",
	).Scan(&observations, &updates, &states, &activeCandidateRows); err != nil {
		t.Fatalf("read rating shadow state: %v", err)
	}
	if observations != 1 || updates != 5 || states != 5 || activeCandidateRows != 0 {
		t.Fatalf("shadow rows: observations=%d updates=%d states=%d active_candidate=%d",
			observations, updates, states, activeCandidateRows)
	}
	reportStore, err := postgres.NewRatingShadowReportStore(pool)
	if err != nil {
		t.Fatalf("NewRatingShadowReportStore() error = %v", err)
	}
	report, err := reportStore.BuildComparison(ctx, postgres.RatingShadowComparisonRequest{
		CandidateVersion: candidateVersion,
		BinCount:         10,
		Policy: rating.ModelComparisonPolicy{
			MinimumRoomsPerSegment:              1,
			MinimumOverallBrierImprovement:      0,
			MaximumSegmentBrierRegression:       10,
			MaximumSegmentLogLossRegression:     10,
			MaximumSegmentCalibrationRegression: 10,
		},
	})
	if err != nil {
		t.Fatalf("BuildComparison() error = %v", err)
	}
	if report.Rooms != 1 || report.BaselineVersion != baselineVersion || report.CandidateVersion != candidateVersion {
		t.Fatalf("rating shadow comparison = %+v", report)
	}

	ratingQueue, err := postgres.NewRatingQueue(pool)
	if err != nil {
		t.Fatalf("NewRatingQueue() error = %v", err)
	}
	processBaselineThrough(t, ctx, ratingQueue, eventID)
	queries, err := postgres.NewQueryStore(pool)
	if err != nil {
		t.Fatalf("NewQueryStore() error = %v", err)
	}
	current, err := queries.GetRating(ctx, participants[0].PlayerID, "solitaire")
	if err != nil {
		t.Fatalf("GetRating() error = %v", err)
	}
	if current.Estimate.ModelVersion != baselineVersion {
		t.Fatalf("active rating model = %q, want %q", current.Estimate.ModelVersion, baselineVersion)
	}
}

func seedShadowDeployment(t *testing.T, ctx context.Context, pool *pgxpool.Pool, candidateVersion, baselineVersion string, startedAt time.Time) {
	t.Helper()
	trainedThrough := startedAt.Add(-2 * time.Hour)
	definition := struct {
		Candidate rating.ExtendedConfig `json:"candidate"`
	}{Candidate: rating.ExtendedConfig{
		Baseline: rating.DefaultBaselineConfig(candidateVersion),
		FeatureSchema: rating.FeatureSchema{
			Version: "features-v1", ModeID: "solitaire",
			ScoringRulesVersion: "scoring-v1", DeckVersion: "deck-v1",
			Definitions: []rating.FeatureDefinition{{Name: rating.FeatureScore, SignalFamily: "performance"}},
		},
		FeatureWeights: []rating.FeatureWeight{{Name: rating.FeatureScore, Mean: 500, StandardDeviation: 100, Weight: 1}},
		TrainedThrough: trainedThrough,
	}}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("marshal shadow definition: %v", err)
	}
	mustExec(t, ctx, pool,
		"INSERT INTO rating_models (model_version, feature_schema, parameters_digest) VALUES ($1, $2, $3)",
		candidateVersion, encoded, strings.Repeat("c", 64),
	)
	mustExec(t, ctx, pool, `
INSERT INTO rating_shadow_deployments (
    candidate_version, baseline_version, mode_id, scoring_rules_version,
    deck_version, training_cutoff, trained_through, definition,
    definition_digest, activated_at, created_at
) VALUES ($1, $2, 'solitaire', 'scoring-v1', 'deck-v1', $3, $4, $5, $6, $7, $8)`,
		candidateVersion, baselineVersion, startedAt.Add(-time.Hour), trainedThrough, encoded,
		strings.Repeat("d", 64), startedAt.Add(-30*time.Minute), startedAt.Add(-31*time.Minute),
	)
}

func processShadowThrough(t *testing.T, ctx context.Context, queue *postgres.RatingShadowQueue, kind, sourceID string) {
	t.Helper()
	for attempt := range 100 {
		now := time.Now().UTC()
		claim, err := queue.ClaimNextRatingShadowWork(ctx, worker.RatingClaimRequest{
			Token:     fmt.Sprintf("shadow-drain-%d-%d", time.Now().UnixNano(), attempt),
			ClaimedAt: now, LeaseUntil: now.Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("ClaimNextRatingShadowWork() error = %v", err)
		}
		if claim == nil {
			t.Fatalf("shadow work %s/%s was not claimable", kind, sourceID)
		}
		if _, err := queue.ProcessRatingShadowWork(ctx, *claim, now); err != nil {
			t.Fatalf("ProcessRatingShadowWork(%s/%s) error = %v", claim.Kind, claim.SourceID, err)
		}
		if claim.Kind == kind && claim.SourceID == sourceID {
			return
		}
	}
	t.Fatalf("shadow work %s/%s was not reached", kind, sourceID)
}

func processBaselineThrough(t *testing.T, ctx context.Context, queue *postgres.RatingQueue, eventID string) {
	t.Helper()
	for attempt := range 100 {
		now := time.Now().UTC()
		claim, err := queue.ClaimNextRatingResult(ctx, worker.RatingClaimRequest{
			Token:     fmt.Sprintf("rating-drain-%d-%d", time.Now().UnixNano(), attempt),
			ClaimedAt: now, LeaseUntil: now.Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("ClaimNextRatingResult() error = %v", err)
		}
		if claim == nil {
			t.Fatalf("baseline result %s was not claimable", eventID)
		}
		if _, err := queue.ProcessRatingResult(ctx, *claim, now); err != nil {
			t.Fatalf("ProcessRatingResult(%s) error = %v", claim.EventID, err)
		}
		if claim.EventID == eventID {
			return
		}
	}
	t.Fatalf("baseline result %s was not reached", eventID)
}
