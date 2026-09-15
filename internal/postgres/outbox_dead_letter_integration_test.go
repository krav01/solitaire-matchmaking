package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

func TestOutboxDeadLetterBlocksAggregateUntilRedrive(t *testing.T) {
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

	prefix := fmt.Sprintf("dead-letter-%d", time.Now().UnixNano())
	occurredAt := time.Now().UTC().Add(-time.Minute)
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
	now := time.Now().UTC()
	claims, err := queue.ClaimOutboxEvents(ctx, worker.OutboxClaimRequest{
		Token: prefix + "-initial", Limit: 3, ClaimedAt: now, LeaseUntil: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ClaimOutboxEvents() error = %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("initial claims = %d, want 2 aggregate heads", len(claims))
	}
	byID := make(map[string]worker.OutboxClaim, len(claims))
	for _, claim := range claims {
		byID[claim.Event.EventID] = claim
	}
	firstA, ok := byID[prefix+"-a1"]
	if !ok {
		t.Fatalf("aggregate A head not claimed: %+v", claims)
	}
	firstB, ok := byID[prefix+"-b1"]
	if !ok {
		t.Fatalf("aggregate B head not claimed: %+v", claims)
	}

	if err := queue.MarkOutboxDeadLettered(ctx, firstA.Event.EventID, firstA.Token, now, "permanent downstream rejection"); err != nil {
		t.Fatalf("MarkOutboxDeadLettered() error = %v", err)
	}
	if err := queue.MarkOutboxDelivered(ctx, firstB.Event.EventID, firstB.Token, now); err != nil {
		t.Fatalf("MarkOutboxDelivered(B1) error = %v", err)
	}

	blocked, err := queue.ClaimOutboxEvents(ctx, worker.OutboxClaimRequest{
		Token: prefix + "-blocked", Limit: 3, ClaimedAt: now.Add(time.Second), LeaseUntil: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("claim while dead-letter blocks aggregate: %v", err)
	}
	if len(blocked) != 0 {
		t.Fatalf("claims while aggregate head is dead-lettered = %+v, want none", blocked)
	}

	redriveAt := now.Add(2 * time.Second)
	if err := queue.RedriveOutboxDeadLetter(ctx, firstA.Event.EventID, redriveAt); err != nil {
		t.Fatalf("RedriveOutboxDeadLetter() error = %v", err)
	}
	redriven, err := queue.ClaimOutboxEvents(ctx, worker.OutboxClaimRequest{
		Token: prefix + "-redrive", Limit: 3, ClaimedAt: redriveAt, LeaseUntil: redriveAt.Add(time.Minute),
	})
	if err != nil || len(redriven) != 1 || redriven[0].Event.EventID != firstA.Event.EventID {
		t.Fatalf("redriven claims = %+v, error = %v", redriven, err)
	}
	if err := queue.MarkOutboxDelivered(ctx, redriven[0].Event.EventID, redriven[0].Token, redriveAt); err != nil {
		t.Fatalf("deliver redriven event: %v", err)
	}

	next, err := queue.ClaimOutboxEvents(ctx, worker.OutboxClaimRequest{
		Token: prefix + "-next", Limit: 3, ClaimedAt: redriveAt.Add(time.Second), LeaseUntil: redriveAt.Add(time.Minute),
	})
	if err != nil || len(next) != 1 || next[0].Event.EventID != prefix+"-a2" {
		t.Fatalf("next aggregate claim = %+v, error = %v", next, err)
	}
}
