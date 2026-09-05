package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

var errInjectedPublisherFailure = errors.New("injected publisher failure")

func TestOutboxResiliencePostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openIsolatedResiliencePool(t, ctx, databaseURL)

	t.Run("bounded concurrent load", func(t *testing.T) {
		testOutboxConcurrentLoad(t, ctx, pool)
	})
	t.Run("expired lease recovery", func(t *testing.T) {
		testOutboxExpiredLeaseRecovery(t, ctx, pool)
	})
	t.Run("publisher failure retry", func(t *testing.T) {
		testOutboxPublisherFailureRetry(t, ctx, pool)
	})
}

func testOutboxConcurrentLoad(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	const (
		aggregateCount = 16
		versions       = 4
		runnerCount    = 4
		totalEvents    = aggregateCount * versions
	)
	prefix := fmt.Sprintf("resilience-load-%d", time.Now().UnixNano())
	occurredAt := time.Date(2020, time.January, 1, 12, 0, 0, 0, time.UTC)
	for aggregate := range aggregateCount {
		for version := 1; version <= versions; version++ {
			insertResilienceEvent(
				t, ctx, pool,
				fmt.Sprintf("%s-event-%02d-%02d", prefix, aggregate, version),
				fmt.Sprintf("%s-aggregate-%02d", prefix, aggregate),
				int64(version),
				occurredAt.Add(time.Duration(aggregate*versions+version)*time.Microsecond),
			)
		}
	}

	queue, err := postgres.NewOutboxQueue(pool)
	if err != nil {
		t.Fatalf("NewOutboxQueue() error = %v", err)
	}
	publisher := newResiliencePublisher("")
	runners := make([]*worker.OutboxRunner, runnerCount)
	for index := range runners {
		runners[index] = newResilienceRunner(t, queue, publisher, 8, 4)
	}

	delivered := 0
	for cycle := 0; cycle < totalEvents && delivered < totalEvents; cycle++ {
		outcomes := make(chan resilienceRunOutcome, runnerCount)
		var group sync.WaitGroup
		for _, runner := range runners {
			group.Add(1)
			go func(runner *worker.OutboxRunner) {
				defer group.Done()
				result, runErr := runner.RunOnce(ctx)
				outcomes <- resilienceRunOutcome{result: result, err: runErr}
			}(runner)
		}
		group.Wait()
		close(outcomes)
		for outcome := range outcomes {
			if outcome.err != nil || outcome.result.Failed != 0 {
				t.Fatalf("concurrent RunOnce() = %+v, error = %v", outcome.result, outcome.err)
			}
		}
		delivered = countDeliveredResilienceEvents(t, ctx, pool, prefix+"%")
	}
	if delivered != totalEvents {
		t.Fatalf("delivered events = %d, want %d", delivered, totalEvents)
	}

	publicationOrder, attempts := publisher.snapshot()
	if attempts != totalEvents {
		t.Fatalf("publication attempts = %d, want %d", attempts, totalEvents)
	}
	wantVersions := []int64{1, 2, 3, 4}
	for aggregate := range aggregateCount {
		aggregateID := fmt.Sprintf("%s-aggregate-%02d", prefix, aggregate)
		if got := publicationOrder[aggregateID]; !slices.Equal(got, wantVersions) {
			t.Fatalf("aggregate %q publication order = %v, want %v", aggregateID, got, wantVersions)
		}
	}

	var storedAttempts, claimed int
	if err := pool.QueryRow(ctx, `
SELECT sum(attempt_count),
       count(*) FILTER (WHERE claimed_by IS NOT NULL OR claimed_until IS NOT NULL)
FROM outbox_events
WHERE event_id LIKE $1`, prefix+"%",
	).Scan(&storedAttempts, &claimed); err != nil {
		t.Fatalf("read load delivery state: %v", err)
	}
	if storedAttempts != totalEvents || claimed != 0 {
		t.Fatalf("load delivery state: attempts=%d claimed=%d", storedAttempts, claimed)
	}
}

func testOutboxExpiredLeaseRecovery(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	prefix := fmt.Sprintf("resilience-recovery-%d", time.Now().UnixNano())
	eventID := prefix + "-event"
	insertResilienceEvent(t, ctx, pool, eventID, prefix+"-aggregate", 1, time.Date(2020, time.January, 2, 0, 0, 0, 0, time.UTC))
	queue, err := postgres.NewOutboxQueue(pool)
	if err != nil {
		t.Fatalf("NewOutboxQueue() error = %v", err)
	}

	staleClaimedAt := time.Now().UTC().Add(-2 * time.Minute)
	abandoned, err := queue.ClaimOutboxEvents(ctx, worker.OutboxClaimRequest{
		Token: "abandoned-worker", Limit: 1, ClaimedAt: staleClaimedAt,
		LeaseUntil: staleClaimedAt.Add(time.Minute),
	})
	if err != nil || len(abandoned) != 1 || abandoned[0].Event.EventID != eventID {
		t.Fatalf("abandoned claim = %+v, error = %v", abandoned, err)
	}
	if err := queue.MarkOutboxDelivered(ctx, eventID, abandoned[0].Token, time.Now().UTC()); !errors.Is(err, worker.ErrOutboxClaimLost) {
		t.Fatalf("stale acknowledgement error = %v", err)
	}

	recoveredAt := time.Now().UTC()
	recovered, err := queue.ClaimOutboxEvents(ctx, worker.OutboxClaimRequest{
		Token: "replacement-worker", Limit: 1, ClaimedAt: recoveredAt,
		LeaseUntil: recoveredAt.Add(time.Minute),
	})
	if err != nil || len(recovered) != 1 || recovered[0].Event.EventID != eventID || recovered[0].Attempt != 2 {
		t.Fatalf("recovered claim = %+v, error = %v", recovered, err)
	}
	if err := queue.MarkOutboxDelivered(ctx, eventID, recovered[0].Token, time.Now().UTC()); err != nil {
		t.Fatalf("MarkOutboxDelivered(recovered) error = %v", err)
	}
	assertResilienceEventState(t, ctx, pool, eventID, 2, true, false)
}

func testOutboxPublisherFailureRetry(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	prefix := fmt.Sprintf("resilience-failure-%d", time.Now().UnixNano())
	eventID := prefix + "-event"
	insertResilienceEvent(t, ctx, pool, eventID, prefix+"-aggregate", 1, time.Date(2020, time.January, 3, 0, 0, 0, 0, time.UTC))
	queue, err := postgres.NewOutboxQueue(pool)
	if err != nil {
		t.Fatalf("NewOutboxQueue() error = %v", err)
	}
	publisher := newResiliencePublisher(eventID)
	runner := newResilienceRunner(t, queue, publisher, 1, 1)

	result, runErr := runner.RunOnce(ctx)
	if !errors.Is(runErr, errInjectedPublisherFailure) || result != (worker.OutboxRunResult{Claimed: 1, Failed: 1}) {
		t.Fatalf("failed RunOnce() = %+v, error = %v", result, runErr)
	}
	assertResilienceEventState(t, ctx, pool, eventID, 1, false, true)

	if _, err := pool.Exec(ctx, `
UPDATE outbox_events
SET available_at = clock_timestamp() - interval '1 millisecond'
WHERE event_id = $1`, eventID); err != nil {
		t.Fatalf("advance retry clock: %v", err)
	}
	result, runErr = runner.RunOnce(ctx)
	if runErr != nil || result != (worker.OutboxRunResult{Claimed: 1, Delivered: 1}) {
		t.Fatalf("recovered RunOnce() = %+v, error = %v", result, runErr)
	}
	assertResilienceEventState(t, ctx, pool, eventID, 2, true, false)

	publicationOrder, attempts := publisher.snapshot()
	if attempts != 2 || !slices.Equal(publicationOrder[prefix+"-aggregate"], []int64{1}) {
		t.Fatalf("publisher state: attempts=%d order=%v", attempts, publicationOrder)
	}
}

func openIsolatedResiliencePool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()

	adminPool, err := postgres.Open(ctx, databaseURL, 2)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	schema := fmt.Sprintf("resilience_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		adminPool.Close()
		t.Fatalf("create isolated schema: %v", err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
		adminPool.Close()
	})

	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	parameters := parsedURL.Query()
	parameters.Set("search_path", schema)
	parsedURL.RawQuery = parameters.Encode()
	pool, err = postgres.Open(ctx, parsedURL.String(), 16)
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	if _, err := postgres.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}
	return pool
}

func insertResilienceEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventID, aggregateID string, version int64, occurredAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
INSERT INTO outbox_events (
    event_id, aggregate_type, aggregate_id, aggregate_version,
    event_type, payload, occurred_at, available_at
) VALUES ($1, 'resilience', $2, $3, 'resilience.tested', '{}'::jsonb, $4, $4)`,
		eventID, aggregateID, version, occurredAt,
	); err != nil {
		t.Fatalf("insert resilience event %q: %v", eventID, err)
	}
}

func newResilienceRunner(t *testing.T, queue worker.OutboxQueue, publisher worker.OutboxPublisher, batchSize, concurrency int) *worker.OutboxRunner {
	t.Helper()
	runner, err := worker.NewOutboxRunner(
		queue,
		publisher,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		worker.OutboxRunnerOptions{
			BatchSize: batchSize, Concurrency: concurrency, LeaseDuration: time.Minute,
			PollInterval: time.Second, RetryBaseDelay: time.Millisecond, RetryMaxDelay: time.Second,
		},
	)
	if err != nil {
		t.Fatalf("NewOutboxRunner() error = %v", err)
	}
	return runner
}

func countDeliveredResilienceEvents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pattern string) int {
	t.Helper()
	var delivered int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM outbox_events
WHERE event_id LIKE $1 AND delivered_at IS NOT NULL`, pattern).Scan(&delivered); err != nil {
		t.Fatalf("count delivered resilience events: %v", err)
	}
	return delivered
}

func assertResilienceEventState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventID string, wantAttempts int, wantDelivered, wantError bool) {
	t.Helper()
	var attempts int
	var delivered, hasError, claimed bool
	if err := pool.QueryRow(ctx, `
SELECT attempt_count,
       delivered_at IS NOT NULL,
       last_error IS NOT NULL,
       claimed_by IS NOT NULL OR claimed_until IS NOT NULL
FROM outbox_events
WHERE event_id = $1`, eventID).Scan(&attempts, &delivered, &hasError, &claimed); err != nil {
		t.Fatalf("read resilience event %q: %v", eventID, err)
	}
	if attempts != wantAttempts || delivered != wantDelivered || hasError != wantError || claimed {
		t.Fatalf("event %q state: attempts=%d delivered=%t error=%t claimed=%t", eventID, attempts, delivered, hasError, claimed)
	}
}

type resilienceRunOutcome struct {
	result worker.OutboxRunResult
	err    error
}

type resiliencePublisher struct {
	mutex            sync.Mutex
	failFirstEventID string
	attempts         int
	publicationOrder map[string][]int64
}

func newResiliencePublisher(failFirstEventID string) *resiliencePublisher {
	return &resiliencePublisher{
		failFirstEventID: failFirstEventID,
		publicationOrder: make(map[string][]int64),
	}
}

func (publisher *resiliencePublisher) Publish(_ context.Context, event worker.OutboxEvent) error {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()

	publisher.attempts++
	if event.EventID == publisher.failFirstEventID && publisher.attempts == 1 {
		return errInjectedPublisherFailure
	}
	publisher.publicationOrder[event.AggregateID] = append(
		publisher.publicationOrder[event.AggregateID], event.AggregateVersion,
	)
	return nil
}

func (publisher *resiliencePublisher) snapshot() (map[string][]int64, int) {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()

	publicationOrder := make(map[string][]int64, len(publisher.publicationOrder))
	for aggregateID, versions := range publisher.publicationOrder {
		publicationOrder[aggregateID] = slices.Clone(versions)
	}
	return publicationOrder, publisher.attempts
}
