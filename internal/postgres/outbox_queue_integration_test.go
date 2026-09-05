package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

func TestOutboxDeliveryPostgreSQL(t *testing.T) {
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

	prefix := fmt.Sprintf("outbox-%d", time.Now().UnixNano())
	occurredAt := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, event := range []struct {
		id        string
		aggregate string
		version   int
	}{
		{id: prefix + "-a1", aggregate: prefix + "-a", version: 1},
		{id: prefix + "-a2", aggregate: prefix + "-a", version: 2},
		{id: prefix + "-b1", aggregate: prefix + "-b", version: 1},
	} {
		if _, err := pool.Exec(ctx, `
INSERT INTO outbox_events (
    event_id, aggregate_type, aggregate_id, aggregate_version,
    event_type, payload, occurred_at, available_at
) VALUES ($1, 'room', $2, $3, 'room.tested', '{}'::jsonb, $4, $4)`,
			event.id, event.aggregate, event.version, occurredAt,
		); err != nil {
			t.Fatalf("insert event %q: %v", event.id, err)
		}
	}

	queue, err := postgres.NewOutboxQueue(pool)
	if err != nil {
		t.Fatalf("NewOutboxQueue() error = %v", err)
	}
	claimedAt := time.Now().UTC()
	claimResults := make(chan outboxClaimResult, 2)
	startClaims := make(chan struct{})
	for _, token := range []string{"outbox-claim-a", "outbox-claim-b"} {
		go func(token string) {
			<-startClaims
			claims, claimErr := queue.ClaimOutboxEvents(ctx, worker.OutboxClaimRequest{
				Token: token, Limit: 2, ClaimedAt: claimedAt,
				LeaseUntil: claimedAt.Add(time.Minute),
			})
			claimResults <- outboxClaimResult{claims: claims, err: claimErr}
		}(token)
	}
	close(startClaims)

	claims := make(map[string]worker.OutboxClaim)
	for range 2 {
		result := <-claimResults
		if result.err != nil {
			t.Fatalf("concurrent claim error = %v", result.err)
		}
		for _, claim := range result.claims {
			if _, duplicate := claims[claim.Event.EventID]; duplicate {
				t.Fatalf("event %q was claimed twice", claim.Event.EventID)
			}
			claims[claim.Event.EventID] = claim
		}
	}
	if len(claims) != 2 {
		t.Fatalf("concurrent claims = %d, want 2", len(claims))
	}
	firstA, hasFirstA := claims[prefix+"-a1"]
	firstB, hasFirstB := claims[prefix+"-b1"]
	if !hasFirstA || !hasFirstB {
		t.Fatalf("initial claims = %v, want aggregate heads", claims)
	}
	if _, claimedEarly := claims[prefix+"-a2"]; claimedEarly {
		t.Fatal("second aggregate event was claimed before its predecessor")
	}

	if err := queue.MarkOutboxDelivered(ctx, firstA.Event.EventID, "wrong-token", claimedAt); !errors.Is(err, worker.ErrOutboxClaimLost) {
		t.Fatalf("MarkOutboxDelivered(wrong token) error = %v", err)
	}
	if err := queue.MarkOutboxDelivered(ctx, firstA.Event.EventID, firstA.Token, claimedAt); err != nil {
		t.Fatalf("MarkOutboxDelivered(first aggregate event) error = %v", err)
	}
	retryAt := claimedAt.Add(10 * time.Second)
	if err := queue.ScheduleOutboxRetry(ctx, firstB.Event.EventID, firstB.Token, retryAt, "temporary failure"); err != nil {
		t.Fatalf("ScheduleOutboxRetry() error = %v", err)
	}

	secondA, err := queue.ClaimOutboxEvents(ctx, worker.OutboxClaimRequest{
		Token: prefix + "-claim-a2", Limit: 2, ClaimedAt: claimedAt.Add(time.Second),
		LeaseUntil: claimedAt.Add(time.Minute),
	})
	if err != nil || len(secondA) != 1 || secondA[0].Event.EventID != prefix+"-a2" {
		t.Fatalf("second aggregate claim = %+v, error = %v", secondA, err)
	}
	if err := queue.MarkOutboxDelivered(ctx, secondA[0].Event.EventID, secondA[0].Token, claimedAt.Add(time.Second)); err != nil {
		t.Fatalf("MarkOutboxDelivered(second aggregate event) error = %v", err)
	}

	beforeRetry, err := queue.ClaimOutboxEvents(ctx, worker.OutboxClaimRequest{
		Token: prefix + "-claim-early", Limit: 2, ClaimedAt: retryAt.Add(-time.Second),
		LeaseUntil: retryAt.Add(time.Minute),
	})
	if err != nil || len(beforeRetry) != 0 {
		t.Fatalf("claim before retry = %+v, error = %v", beforeRetry, err)
	}
	retryClaims, err := queue.ClaimOutboxEvents(ctx, worker.OutboxClaimRequest{
		Token: prefix + "-claim-retry", Limit: 2, ClaimedAt: retryAt,
		LeaseUntil: retryAt.Add(time.Minute),
	})
	if err != nil || len(retryClaims) != 1 || retryClaims[0].Event.EventID != firstB.Event.EventID || retryClaims[0].Attempt != 2 {
		t.Fatalf("retry claims = %+v, error = %v", retryClaims, err)
	}
	if err := queue.MarkOutboxDelivered(ctx, retryClaims[0].Event.EventID, retryClaims[0].Token, retryAt); err != nil {
		t.Fatalf("MarkOutboxDelivered(retry) error = %v", err)
	}

	var delivered, attempts, errorsStored, claimed int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE delivered_at IS NOT NULL),
       sum(attempt_count),
       count(*) FILTER (WHERE last_error IS NOT NULL),
       count(*) FILTER (WHERE claimed_by IS NOT NULL OR claimed_until IS NOT NULL)
FROM outbox_events
WHERE event_id LIKE $1`, prefix+"-%",
	).Scan(&delivered, &attempts, &errorsStored, &claimed); err != nil {
		t.Fatalf("read delivery state: %v", err)
	}
	if delivered != 3 || attempts != 4 || errorsStored != 0 || claimed != 0 {
		t.Fatalf("delivery state: delivered=%d attempts=%d errors=%d claimed=%d", delivered, attempts, errorsStored, claimed)
	}
}

type outboxClaimResult struct {
	claims []worker.OutboxClaim
	err    error
}
