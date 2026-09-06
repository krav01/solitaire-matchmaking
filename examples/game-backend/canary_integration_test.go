package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/application"
	"github.com/krav01/solitaire-matchmaking/internal/config"
	"github.com/krav01/solitaire-matchmaking/internal/eventdelivery"
	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

const (
	canaryAPIToken    = "canary-api-token-0123456789abcdef"
	canaryOutboxToken = "canary-outbox-token-0123456789abc"
	canaryEventCount  = 13
)

type canaryTicketMutation struct {
	Ticket struct {
		TicketID string `json:"ticket_id"`
	} `json:"ticket"`
	Replay bool `json:"replay"`
}

type canaryTicketState struct {
	Assignment *struct {
		RoomID    string `json:"room_id"`
		SessionID string `json:"session_id"`
		Seat      int    `json:"seat"`
	} `json:"assignment"`
}

type canaryRoomState struct {
	RoomID  string `json:"room_id"`
	DeckID  string `json:"deck_id"`
	Status  string `json:"status"`
	Members []struct {
		PlayerID  string `json:"player_id"`
		SessionID string `json:"session_id"`
		Seat      int    `json:"seat"`
	} `json:"members"`
}

type canaryRatingState struct {
	PlayerID string `json:"player_id"`
	ModeID   string `json:"mode_id"`
	Rating   struct {
		Games        int64  `json:"games"`
		ModelVersion string `json:"model_version"`
	} `json:"rating"`
	Revision int64 `json:"revision"`
}

type canaryResultState struct {
	Replay bool `json:"replay"`
}

type canaryRecorder struct {
	mutex      sync.Mutex
	events     map[string]outboxEvent
	applyCalls int
}

func (recorder *canaryRecorder) apply(event outboxEvent) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.applyCalls++
	recorder.events[event.EventID] = event

	return nil
}

func (recorder *canaryRecorder) snapshot() (int, map[string]int, outboxEvent) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()

	types := make(map[string]int)
	var replay outboxEvent
	for _, event := range recorder.events {
		types[event.EventType]++
		if replay.EventID == "" {
			replay = event
		}
	}

	return recorder.applyCalls, types, replay
}

func TestCanaryLifecycleWithGameBackend(t *testing.T) {
	if os.Getenv("CANARY_RUN") != "1" {
		t.Skip("CANARY_RUN=1 is required")
	}
	databaseURL := os.Getenv("CANARY_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CANARY_DATABASE_URL is not set")
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, databaseURL, 16)
	if err != nil {
		t.Fatalf("open canary PostgreSQL: %v", err)
	}
	defer pool.Close()
	assertDisposableCanaryDatabase(t, ctx, pool)
	if _, err := postgres.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("apply canary migrations: %v", err)
	}
	assertEmptyCanaryDatabase(t, ctx, pool)

	fixtureStartedAt := time.Now().UTC().Add(-2 * time.Second).Truncate(time.Millisecond)
	seedCanaryConfiguration(t, ctx, pool, fixtureStartedAt)

	recorder := &canaryRecorder{events: make(map[string]outboxEvent)}
	receiver, err := newReceiver(canaryOutboxToken, recorder.apply)
	if err != nil {
		t.Fatalf("create game-backend receiver: %v", err)
	}
	gameBackend := httptest.NewServer(receiver)
	defer gameBackend.Close()

	address := reserveCanaryAddress(t)
	cfg := loadCanaryConfig(t, databaseURL, address, gameBackend.URL+"/events")
	serviceCtx, stopService := context.WithCancel(ctx)
	serviceErrors := make(chan error, 1)
	go func() {
		logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
		serviceErrors <- application.Run(serviceCtx, cfg, logger)
	}()
	defer func() {
		stopService()
		select {
		case serviceErr := <-serviceErrors:
			if serviceErr != nil {
				t.Errorf("stop canary service: %v", serviceErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("canary service did not stop within five seconds")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + address
	waitForCanary(t, ctx, "service readiness", func() (bool, error) {
		status, _, _, requestErr := canaryRequest(ctx, client, http.MethodGet, baseURL+"/readyz", "", nil)
		return status == http.StatusOK, requestErr
	})
	assertCanaryOperationalEndpoints(t, ctx, client, baseURL)

	requestedAt := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	ticketIDs := make([]string, 5)
	requestBodies := make([][]byte, 5)
	for index := range ticketIDs {
		body, marshalErr := json.Marshal(map[string]any{
			"entry_id":           fmt.Sprintf("canary-entry-%d", index+1),
			"player_id":          fmt.Sprintf("canary-player-%d", index+1),
			"tournament_id":      "canary-tournament",
			"tournament_version": "v1",
			"requested_at":       requestedAt,
			"snapshot_at":        requestedAt,
			"rating_snapshot": map[string]any{
				"mean":          25,
				"uncertainty":   8,
				"games":         3,
				"model_version": "canary-model-v1",
				"updated_at":    requestedAt.Add(-time.Minute),
			},
		})
		if marshalErr != nil {
			t.Fatalf("encode ticket %d: %v", index+1, marshalErr)
		}
		requestBodies[index] = body
		var accepted canaryTicketMutation
		mustCanaryJSON(t, ctx, client, http.MethodPost, baseURL+"/v1/tickets", body, http.StatusCreated, &accepted)
		if accepted.Ticket.TicketID == "" || accepted.Replay {
			t.Fatalf("ticket %d acceptance = %+v", index+1, accepted)
		}
		ticketIDs[index] = accepted.Ticket.TicketID
	}

	var ticketReplay canaryTicketMutation
	mustCanaryJSON(t, ctx, client, http.MethodPost, baseURL+"/v1/tickets", requestBodies[0], http.StatusOK, &ticketReplay)
	if !ticketReplay.Replay || ticketReplay.Ticket.TicketID != ticketIDs[0] {
		t.Fatalf("ticket replay = %+v, original ticket = %q", ticketReplay, ticketIDs[0])
	}

	assignments := make([]canaryTicketState, len(ticketIDs))
	waitForCanary(t, ctx, "five ticket assignments", func() (bool, error) {
		for index, ticketID := range ticketIDs {
			status, body, _, requestErr := canaryRequest(
				ctx, client, http.MethodGet, baseURL+"/v1/tickets/"+ticketID, canaryAPIToken, nil,
			)
			if requestErr != nil {
				return false, requestErr
			}
			if status != http.StatusOK {
				return false, fmt.Errorf("read ticket %q returned HTTP %d", ticketID, status)
			}
			var state canaryTicketState
			if err := json.Unmarshal(body, &state); err != nil {
				return false, fmt.Errorf("decode ticket %q: %w", ticketID, err)
			}
			if state.Assignment == nil {
				return false, nil
			}
			assignments[index] = state
		}
		return true, nil
	})

	roomID := assignments[0].Assignment.RoomID
	if roomID == "" {
		t.Fatal("assigned room identity is empty")
	}
	for index, state := range assignments {
		if state.Assignment.RoomID != roomID {
			t.Fatalf("ticket %d room = %q, want %q", index+1, state.Assignment.RoomID, roomID)
		}
	}

	var collectingRoom canaryRoomState
	mustCanaryJSON(t, ctx, client, http.MethodGet, baseURL+"/v1/rooms/"+roomID, nil, http.StatusOK, &collectingRoom)
	if collectingRoom.Status != "collecting" || len(collectingRoom.Members) != 5 {
		t.Fatalf("collecting room = %+v", collectingRoom)
	}

	finishedAt := time.Now().UTC().Truncate(time.Millisecond)
	participants := make([]map[string]any, len(collectingRoom.Members))
	for index, member := range collectingRoom.Members {
		participants[index] = map[string]any{
			"session_id": member.SessionID,
			"player_id":  member.PlayerID,
			"place":      member.Seat,
			"features": map[string]any{
				"score":      1000 - member.Seat*100,
				"elapsed_ms": 60000 + member.Seat*1000,
				"completed":  true,
				"moves":      90 + member.Seat,
				"undo_moves": member.Seat - 1,
			},
		}
	}
	resultBody, err := json.Marshal(map[string]any{
		"event_id":              "canary-result-v1",
		"room_id":               roomID,
		"mode_id":               "solitaire",
		"deck_id":               collectingRoom.DeckID,
		"scoring_rules_version": "scoring-v1",
		"finished_at":           finishedAt,
		"available_at":          finishedAt,
		"participants":          participants,
	})
	if err != nil {
		t.Fatalf("encode canary result: %v", err)
	}
	var finalized canaryResultState
	mustCanaryJSON(t, ctx, client, http.MethodPost, baseURL+"/v1/results", resultBody, http.StatusCreated, &finalized)
	if finalized.Replay {
		t.Fatal("initial result was reported as replay")
	}
	var resultReplay canaryResultState
	mustCanaryJSON(t, ctx, client, http.MethodPost, baseURL+"/v1/results", resultBody, http.StatusOK, &resultReplay)
	if !resultReplay.Replay {
		t.Fatal("identical result retry was not reported as replay")
	}

	waitForCanary(t, ctx, "room completion, ratings and outbox delivery", func() (bool, error) {
		var delivered int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE delivered_at IS NOT NULL").Scan(&delivered); err != nil {
			return false, err
		}
		applied, _, _ := recorder.snapshot()
		return delivered == canaryEventCount && applied == canaryEventCount, nil
	})

	var completedRoom canaryRoomState
	mustCanaryJSON(t, ctx, client, http.MethodGet, baseURL+"/v1/rooms/"+roomID, nil, http.StatusOK, &completedRoom)
	if completedRoom.Status != "completed" {
		t.Fatalf("completed room status = %q", completedRoom.Status)
	}
	for index := range ticketIDs {
		var current canaryRatingState
		mustCanaryJSON(
			t, ctx, client, http.MethodGet,
			fmt.Sprintf("%s/v1/ratings/canary-player-%d?mode_id=solitaire", baseURL, index+1),
			nil, http.StatusOK, &current,
		)
		if current.PlayerID != fmt.Sprintf("canary-player-%d", index+1) || current.ModeID != "solitaire" ||
			current.Rating.ModelVersion != "canary-model-v1" || current.Rating.Games != 4 || current.Revision != 1 {
			t.Fatalf("player %d rating = %+v", index+1, current)
		}
	}

	appliedBeforeReplay, eventTypes, replayEvent := recorder.snapshot()
	wantTypes := map[string]int{
		"ticket.accepted": 5,
		"ticket.assigned": 5,
		"room.filled":     1,
		"room.completed":  1,
		"result.rated":    1,
	}
	if appliedBeforeReplay != canaryEventCount || !maps.Equal(eventTypes, wantTypes) {
		t.Fatalf("game-backend events = %d, types = %v, want %v", appliedBeforeReplay, eventTypes, wantTypes)
	}
	publisher, err := eventdelivery.NewHTTPPublisher(gameBackend.URL+"/events", canaryOutboxToken, time.Second)
	if err != nil {
		t.Fatalf("create replay publisher: %v", err)
	}
	if err := publisher.Publish(ctx, worker.OutboxEvent{
		EventID: replayEvent.EventID, AggregateType: replayEvent.AggregateType,
		AggregateID: replayEvent.AggregateID, AggregateVersion: replayEvent.AggregateVersion,
		EventType: replayEvent.EventType, Payload: replayEvent.Payload, OccurredAt: replayEvent.OccurredAt,
	}); err != nil {
		t.Fatalf("replay delivered event: %v", err)
	}
	appliedAfterReplay, _, _ := recorder.snapshot()
	if appliedAfterReplay != appliedBeforeReplay {
		t.Fatalf("game-backend side effects after replay = %d, want %d", appliedAfterReplay, appliedBeforeReplay)
	}

	t.Logf(
		"canary passed: room=%s tickets=5 ratings=5 events=%d ticket_replay=true result_replay=true outbox_replay_deduplicated=true duration=%s",
		roomID, appliedAfterReplay, time.Since(started).Round(time.Millisecond),
	)
}

func assertDisposableCanaryDatabase(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var databaseName string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&databaseName); err != nil {
		t.Fatalf("read canary database name: %v", err)
	}
	if !strings.HasSuffix(databaseName, "_canary") {
		t.Fatalf("CANARY_DATABASE_URL must target a database ending in _canary, got %q", databaseName)
	}
}

func assertEmptyCanaryDatabase(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var rows int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM matchmaking_tickets) +
    (SELECT count(*) FROM rooms) +
    (SELECT count(*) FROM verified_results) +
    (SELECT count(*) FROM outbox_events)`).Scan(&rows); err != nil {
		t.Fatalf("inspect canary database: %v", err)
	}
	if rows != 0 {
		t.Fatalf("canary database is not empty: %d operational rows", rows)
	}
}

func seedCanaryConfiguration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, startedAt time.Time) {
	t.Helper()
	statements := []struct {
		sql       string
		arguments []any
	}{
		{sql: "INSERT INTO rating_models (model_version, parameters_digest) VALUES ($1, $2)", arguments: []any{"canary-model-v1", strings.Repeat("a", 64)}},
		{sql: `
INSERT INTO matching_policies (policy_version, rating_model_version, definition, definition_digest)
VALUES (
    'canary-policy-v1',
    'canary-model-v1',
    '{
        "initial_skill_gap": 100,
        "max_skill_gap": 100,
        "max_win_probability_spread": 1,
        "expansion_interval_ms": 1000,
        "fill_timeout_ms": 60000,
        "age_priority_after_ms": 30000,
        "candidate_limit": 100,
        "room_limit": 100,
        "prefer_nearly_full": true
    }'::jsonb,
    $1
)`, arguments: []any{strings.Repeat("b", 64)}},
		{sql: `
INSERT INTO tournament_configs (
    tournament_id, version, mode_id, capacity, entry_fee_minor, currency,
    scoring_rules_version, settlement_version, policy_version,
    rating_model_version, result_timeout_ms, active_from
) VALUES (
    'canary-tournament', 'v1', 'solitaire', 5, 100, 'USD',
    'scoring-v1', 'settlement-v1', 'canary-policy-v1',
    'canary-model-v1', 60000, $1
)`, arguments: []any{startedAt}},
		{sql: `
INSERT INTO rooms (
    room_id, tournament_id, tournament_version, mode_id, policy_version,
    rating_model_version, scoring_rules_version, settlement_version,
    deck_id, capacity, status, created_at, fill_deadline
) VALUES (
    'canary-room', 'canary-tournament', 'v1', 'solitaire', 'canary-policy-v1',
    'canary-model-v1', 'scoring-v1', 'settlement-v1',
    'canary-deck', 5, 'forming', $1, $2
)`, arguments: []any{startedAt, startedAt.Add(time.Minute)}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.sql, statement.arguments...); err != nil {
			t.Fatalf("seed canary configuration: %v", err)
		}
	}
}

func reserveCanaryAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve canary address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release canary address: %v", err)
	}

	return address
}

func loadCanaryConfig(t *testing.T, databaseURL, address, outboxURL string) config.Config {
	t.Helper()
	values := map[string]string{
		"DATABASE_URL":                   databaseURL,
		"API_TOKEN":                      canaryAPIToken,
		"HTTP_ADDR":                      address,
		"DB_MAX_CONNS":                   "16",
		"STARTUP_TIMEOUT":                "3s",
		"READINESS_TIMEOUT":              "1s",
		"SHUTDOWN_TIMEOUT":               "2s",
		"MATCH_WORKER_BATCH_SIZE":        "16",
		"MATCH_WORKER_CONCURRENCY":       "2",
		"MATCH_WORKER_LEASE":             "2s",
		"MATCH_WORKER_POLL_INTERVAL":     "10ms",
		"MATCH_WORKER_FAILURE_BACKOFF":   "100ms",
		"MATCH_WORKER_STALE_RETRY_DELAY": "10ms",
		"RESULT_DEADLINE_BATCH_SIZE":     "8",
		"RESULT_DEADLINE_POLL_INTERVAL":  "100ms",
		"RATING_WORKER_LEASE":            "2s",
		"RATING_WORKER_POLL_INTERVAL":    "10ms",
		"RATING_WORKER_FAILURE_BACKOFF":  "100ms",
		"OUTBOX_DELIVERY_URL":            outboxURL,
		"OUTBOX_DELIVERY_TOKEN":          canaryOutboxToken,
		"OUTBOX_WORKER_BATCH_SIZE":       "32",
		"OUTBOX_WORKER_CONCURRENCY":      "4",
		"OUTBOX_WORKER_LEASE":            "2s",
		"OUTBOX_WORKER_POLL_INTERVAL":    "10ms",
		"OUTBOX_REQUEST_TIMEOUT":         "500ms",
		"OUTBOX_RETRY_BASE_DELAY":        "50ms",
		"OUTBOX_RETRY_MAX_DELAY":         "1s",
		"LOG_LEVEL":                      "error",
	}
	cfg, err := config.Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("load canary configuration: %v", err)
	}

	return cfg
}

func assertCanaryOperationalEndpoints(t *testing.T, ctx context.Context, client *http.Client, baseURL string) {
	t.Helper()
	for _, endpoint := range []struct {
		path  string
		token string
		body  string
	}{
		{path: "/healthz", body: `"status":"ok"`},
		{path: "/readyz", body: `"status":"ready"`},
		{path: "/v1/capabilities", token: canaryAPIToken, body: `"outbox_delivery_enabled":true`},
		{path: "/metrics", token: canaryAPIToken, body: "solitaire_matchmaking_http_requests_total"},
	} {
		status, body, headers, err := canaryRequest(ctx, client, http.MethodGet, baseURL+endpoint.path, endpoint.token, nil)
		if err != nil {
			t.Fatalf("GET %s: %v", endpoint.path, err)
		}
		if status != http.StatusOK || !bytes.Contains(body, []byte(endpoint.body)) {
			t.Fatalf("GET %s status = %d, body = %s", endpoint.path, status, body)
		}
		if headers.Get("X-Request-ID") == "" {
			t.Fatalf("GET %s did not return X-Request-ID", endpoint.path)
		}
	}
}

func mustCanaryJSON(t *testing.T, ctx context.Context, client *http.Client, method, url string, requestBody []byte, wantStatus int, target any) {
	t.Helper()
	status, responseBody, headers, err := canaryRequest(ctx, client, method, url, canaryAPIToken, requestBody)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	if status != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body = %s", method, url, status, wantStatus, responseBody)
	}
	if headers.Get("X-Request-ID") == "" {
		t.Fatalf("%s %s did not return X-Request-ID", method, url)
	}
	if err := json.Unmarshal(responseBody, target); err != nil {
		t.Fatalf("decode %s %s response: %v", method, url, err)
	}
}

func canaryRequest(ctx context.Context, client *http.Client, method, url, token string, body []byte) (int, []byte, http.Header, error) {
	request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxEventBodyBytes+1))
	if err != nil {
		return 0, nil, nil, err
	}

	return response.StatusCode, responseBody, response.Header.Clone(), nil
}

func waitForCanary(t *testing.T, ctx context.Context, description string, check func() (bool, error)) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		complete, err := check()
		if err != nil {
			lastErr = err
		} else if complete {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s: %v (last error: %v)", description, context.Cause(ctx), lastErr)
		case <-ticker.C:
		}
	}
}
