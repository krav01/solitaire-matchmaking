package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

func TestRatingWorkerPostgreSQL(t *testing.T) {
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

	prefix := fmt.Sprintf("rating-%d", time.Now().UnixNano())
	startedAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Millisecond)
	firstEvent := finalizeRatingFixture(t, ctx, pool, prefix+"-a", startedAt)
	secondEvent := finalizeRatingFixture(t, ctx, pool, prefix+"-b", startedAt.Add(time.Second))
	queue, err := postgres.NewRatingQueue(pool)
	if err != nil {
		t.Fatalf("NewRatingQueue() error = %v", err)
	}

	claimedAt := time.Now().UTC()
	claimResults := make(chan ratingClaimResult, 2)
	startClaims := make(chan struct{})
	for _, token := range []string{"first-claim-a", "first-claim-b"} {
		go func() {
			<-startClaims
			claim, claimErr := queue.ClaimNextRatingResult(ctx, worker.RatingClaimRequest{
				Token: token, ClaimedAt: claimedAt, LeaseUntil: claimedAt.Add(time.Minute),
			})
			claimResults <- ratingClaimResult{claim: claim, err: claimErr}
		}()
	}
	close(startClaims)
	var first *worker.RatingClaim
	for range 2 {
		result := <-claimResults
		if result.err != nil {
			t.Fatalf("concurrent first claim error = %v", result.err)
		}
		if result.claim != nil {
			if first != nil {
				t.Fatalf("multiple workers claimed the rating head: %+v and %+v", first, result.claim)
			}
			first = result.claim
		}
	}
	if first == nil || first.EventID != firstEvent {
		t.Fatalf("concurrent first claim = %+v, want %q", first, firstEvent)
	}
	blocked, err := queue.ClaimNextRatingResult(ctx, worker.RatingClaimRequest{
		Token: "blocked-claim", ClaimedAt: claimedAt, LeaseUntil: claimedAt.Add(time.Minute),
	})
	if err != nil || blocked != nil {
		t.Fatalf("claim behind active head = %+v, error = %v", blocked, err)
	}
	updated, err := queue.ProcessRatingResult(ctx, *first, claimedAt)
	if err != nil || updated != 5 {
		t.Fatalf("ProcessRatingResult(first) = %d, error = %v", updated, err)
	}
	if _, err := queue.ProcessRatingResult(ctx, *first, claimedAt); !errors.Is(err, worker.ErrRatingClaimLost) {
		t.Fatalf("ProcessRatingResult(replay) error = %v", err)
	}

	secondClaimAt := claimedAt.Add(time.Second)
	second, err := queue.ClaimNextRatingResult(ctx, worker.RatingClaimRequest{
		Token: "second-claim", ClaimedAt: secondClaimAt, LeaseUntil: secondClaimAt.Add(time.Minute),
	})
	if err != nil || second == nil || second.EventID != secondEvent {
		t.Fatalf("second claim = %+v, error = %v, want %q", second, err, secondEvent)
	}
	if updated, err = queue.ProcessRatingResult(ctx, *second, secondClaimAt); err != nil || updated != 5 {
		t.Fatalf("ProcessRatingResult(second) = %d, error = %v", updated, err)
	}

	var processedResults, historyRows, currentRows, ratedEvents int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM verified_results WHERE event_id = ANY($1::text[]) AND processed_at IS NOT NULL),
    (SELECT count(*) FROM rating_updates WHERE source_event_id = ANY($1::text[])),
    (SELECT count(*) FROM player_ratings WHERE player_id LIKE $2),
    (SELECT count(*) FROM outbox_events WHERE aggregate_id = ANY($1::text[]) AND event_type = 'result.rated')`,
		[]string{firstEvent, secondEvent}, prefix+"-%",
	).Scan(&processedResults, &historyRows, &currentRows, &ratedEvents); err != nil {
		t.Fatalf("read persisted ratings: %v", err)
	}
	if processedResults != 2 || historyRows != 10 || currentRows != 10 || ratedEvents != 2 {
		t.Fatalf("persisted ratings: results=%d history=%d current=%d events=%d",
			processedResults, historyRows, currentRows, ratedEvents)
	}
}

type ratingClaimResult struct {
	claim *worker.RatingClaim
	err   error
}

func finalizeRatingFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prefix string, startedAt time.Time) string {
	t.Helper()
	roomID, participants, _ := seedFilledResultRoom(t, ctx, pool, prefix, startedAt)
	store, err := postgres.NewResultStore(pool)
	if err != nil {
		t.Fatalf("NewResultStore() error = %v", err)
	}
	service, err := tournament.NewResultService(store)
	if err != nil {
		t.Fatalf("NewResultService() error = %v", err)
	}
	eventID := prefix + "-event"
	_, err = service.Finalize(ctx, tournament.FinalizeResultCommand{
		EventID: eventID, RoomID: roomID, ModeID: "solitaire", DeckID: roomID + "-deck",
		ScoringRulesVersion: "scoring-v1", FinishedAt: startedAt.Add(10 * time.Second),
		AvailableAt: startedAt.Add(11 * time.Second), AcceptedAt: startedAt.Add(12 * time.Second),
		Participants: participants,
	})
	if err != nil {
		t.Fatalf("Finalize(%q) error = %v", eventID, err)
	}
	return eventID
}
