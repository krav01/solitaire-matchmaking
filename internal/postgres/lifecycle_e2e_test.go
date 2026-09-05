package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/eventdelivery"
	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

func TestTournamentLifecyclePostgreSQLEndToEnd(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, databaseURL, 12)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer pool.Close()
	if _, err := postgres.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}

	prefix := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	modelVersion := prefix + "-model"
	tournamentID := prefix + "-tournament"
	roomID := prefix + "-room"
	startedAt := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Millisecond)
	seedLifecycleConfig(
		t, ctx, pool, modelVersion, prefix+"-policy", tournamentID, "v1", roomID, startedAt,
	)

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
	processor, err := worker.NewMatchProcessor(matchQueue, matchQueue, ticketService, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("NewMatchProcessor() error = %v", err)
	}

	for index := range 5 {
		command := lifecycleAcceptCommand(
			fmt.Sprintf("%s-player-%d", prefix, index), tournamentID, "v1", modelVersion, startedAt,
		)
		if _, err := ticketService.Accept(ctx, command); err != nil {
			t.Fatalf("Accept(player %d) error = %v", index, err)
		}
	}

	evaluatedAt := time.Now().UTC()
	for round := range 10 {
		claims := claimConcurrently(t, ctx, matchQueue, prefix, round, evaluatedAt)
		processClaims(t, ctx, processor, claims, evaluatedAt)
		if countQueuedTickets(t, ctx, pool, tournamentID) == 0 {
			break
		}
		if round == 9 {
			t.Fatal("matchmaking did not fill the room in ten rounds")
		}
		evaluatedAt = evaluatedAt.Add(100 * time.Millisecond)
	}

	participants := loadRoomParticipants(t, ctx, pool, roomID)
	resultStore, err := postgres.NewResultStore(pool)
	if err != nil {
		t.Fatalf("NewResultStore() error = %v", err)
	}
	resultService, err := tournament.NewResultService(resultStore)
	if err != nil {
		t.Fatalf("NewResultService() error = %v", err)
	}
	completedAt := time.Now().UTC()
	resultEventID := prefix + "-result"
	if _, err := resultService.Finalize(ctx, tournament.FinalizeResultCommand{
		EventID: resultEventID, RoomID: roomID, ModeID: "solitaire", DeckID: roomID + "-deck",
		ScoringRulesVersion: "scoring-v1", FinishedAt: completedAt,
		AvailableAt: completedAt, AcceptedAt: completedAt, Participants: participants,
	}); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ratingQueue, err := postgres.NewRatingQueue(pool)
	if err != nil {
		t.Fatalf("NewRatingQueue() error = %v", err)
	}
	ratingRunner, err := worker.NewRatingRunner(ratingQueue, logger, worker.RatingRunnerOptions{
		LeaseDuration: 10 * time.Second, PollInterval: time.Millisecond, FailureBackoff: time.Second,
	})
	if err != nil {
		t.Fatalf("NewRatingRunner() error = %v", err)
	}
	for range 10 {
		if _, err := ratingRunner.RunOnce(ctx); err != nil {
			t.Fatalf("RatingRunner.RunOnce() error = %v", err)
		}
		if resultProcessed(t, ctx, pool, resultEventID) {
			break
		}
	}
	if !resultProcessed(t, ctx, pool, resultEventID) {
		t.Fatal("rating worker did not process the end-to-end result")
	}

	receiver := newEventReceiver(t, prefix)
	defer receiver.server.Close()
	outboxPublisher, err := eventdelivery.NewHTTPPublisher(
		receiver.server.URL, strings.Repeat("d", 32), 2*time.Second,
	)
	if err != nil {
		t.Fatalf("NewHTTPPublisher() error = %v", err)
	}
	outboxQueue, err := postgres.NewOutboxQueue(pool)
	if err != nil {
		t.Fatalf("NewOutboxQueue() error = %v", err)
	}
	outboxRunner, err := worker.NewOutboxRunner(outboxQueue, outboxPublisher, logger, worker.OutboxRunnerOptions{
		BatchSize: 32, Concurrency: 8, LeaseDuration: 10 * time.Second,
		PollInterval: time.Millisecond, RetryBaseDelay: time.Second, RetryMaxDelay: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewOutboxRunner() error = %v", err)
	}
	for range 20 {
		result, err := outboxRunner.RunOnce(ctx)
		if err != nil {
			t.Fatalf("OutboxRunner.RunOnce() error = %v", err)
		}
		if receiver.count() == 13 {
			break
		}
		if result.Claimed == 0 {
			t.Fatalf("outbox delivery stopped after %d end-to-end events", receiver.count())
		}
	}

	assertEndToEndState(t, ctx, pool, prefix, roomID, resultEventID, receiver)
}

func countQueuedTickets(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tournamentID string) int {
	t.Helper()

	var count int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM matchmaking_tickets
WHERE tournament_id = $1 AND status = 'queued'`, tournamentID).Scan(&count); err != nil {
		t.Fatalf("count queued tickets: %v", err)
	}

	return count
}

func loadRoomParticipants(t *testing.T, ctx context.Context, pool *pgxpool.Pool, roomID string) []tournament.VerifiedParticipant {
	t.Helper()

	rows, err := pool.Query(ctx, `
SELECT session_id, player_id, seat
FROM sessions
WHERE room_id = $1
ORDER BY seat`, roomID)
	if err != nil {
		t.Fatalf("load room sessions: %v", err)
	}
	defer rows.Close()

	participants := make([]tournament.VerifiedParticipant, 0, 5)
	for rows.Next() {
		var participant tournament.VerifiedParticipant
		if err := rows.Scan(&participant.SessionID, &participant.PlayerID, &participant.Place); err != nil {
			t.Fatalf("scan room participant: %v", err)
		}
		participants = append(participants, participant)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read room participants: %v", err)
	}
	if len(participants) != 5 {
		t.Fatalf("room participants = %d, want 5", len(participants))
	}

	return participants
}

func resultProcessed(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventID string) bool {
	t.Helper()

	var processed bool
	if err := pool.QueryRow(ctx, `
SELECT processed_at IS NOT NULL
FROM verified_results
WHERE event_id = $1`, eventID).Scan(&processed); err != nil {
		t.Fatalf("read result processing state: %v", err)
	}

	return processed
}

type eventReceiver struct {
	server *httptest.Server
	prefix string
	mutex  sync.Mutex
	events map[string]worker.OutboxEvent
}

func newEventReceiver(t *testing.T, prefix string) *eventReceiver {
	t.Helper()

	receiver := &eventReceiver{prefix: prefix, events: make(map[string]worker.OutboxEvent)}
	receiver.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+strings.Repeat("d", 32) {
			t.Errorf("outbox authorization = %q", request.Header.Get("Authorization"))
		}

		var event worker.OutboxEvent
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			http.Error(response, "invalid event", http.StatusBadRequest)
			return
		}
		if request.Header.Get("Idempotency-Key") != event.EventID {
			t.Errorf("idempotency key = %q, event = %q", request.Header.Get("Idempotency-Key"), event.EventID)
		}
		if strings.HasPrefix(event.AggregateID, receiver.prefix) {
			receiver.mutex.Lock()
			receiver.events[event.EventID] = event
			receiver.mutex.Unlock()
		}
		response.WriteHeader(http.StatusNoContent)
	}))

	return receiver
}

func (receiver *eventReceiver) count() int {
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()

	return len(receiver.events)
}

func assertEndToEndState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	prefix string,
	roomID string,
	resultEventID string,
	receiver *eventReceiver,
) {
	t.Helper()

	var assignedTickets, ratingUpdates, currentRatings, deliveredEvents int
	var roomStatus string
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM matchmaking_tickets WHERE tournament_id = $1 AND status = 'assigned'),
    (SELECT status FROM rooms WHERE room_id = $2),
    (SELECT count(*) FROM rating_updates WHERE source_event_id = $3),
    (SELECT count(*) FROM player_ratings WHERE player_id LIKE $4),
    (SELECT count(*) FROM outbox_events WHERE aggregate_id LIKE $4 AND delivered_at IS NOT NULL)`,
		prefix+"-tournament", roomID, resultEventID, prefix+"%",
	).Scan(&assignedTickets, &roomStatus, &ratingUpdates, &currentRatings, &deliveredEvents); err != nil {
		t.Fatalf("read end-to-end state: %v", err)
	}
	if assignedTickets != 5 || roomStatus != "completed" || ratingUpdates != 5 || currentRatings != 5 || deliveredEvents != 13 {
		t.Fatalf(
			"end-to-end state: tickets=%d room=%q updates=%d ratings=%d delivered=%d",
			assignedTickets, roomStatus, ratingUpdates, currentRatings, deliveredEvents,
		)
	}

	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	typeCounts := make(map[string]int)
	for _, event := range receiver.events {
		typeCounts[event.EventType]++
	}
	wantTypes := map[string]int{
		"ticket.accepted": 5,
		"ticket.assigned": 5,
		"room.filled":     1,
		"room.completed":  1,
		"result.rated":    1,
	}
	if len(receiver.events) != 13 || !maps.Equal(typeCounts, wantTypes) {
		t.Fatalf("received events = %d, types = %v, want %v", len(receiver.events), typeCounts, wantTypes)
	}
}
