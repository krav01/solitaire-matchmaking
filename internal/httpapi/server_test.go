package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/httpapi"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

const testToken = "0123456789abcdef0123456789abcdef"

type readinessStub struct{ err error }

func (s readinessStub) Ping(context.Context) error { return s.err }

func TestServerHealthAndReadiness(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		path       string
		checkError error
		wantStatus int
		wantValue  string
	}{
		{name: "live", path: "/healthz", wantStatus: http.StatusOK, wantValue: "ok"},
		{name: "ready", path: "/readyz", wantStatus: http.StatusOK, wantValue: "ready"},
		{name: "database unavailable", path: "/readyz", checkError: errors.New("unavailable"), wantStatus: http.StatusServiceUnavailable, wantValue: "not_ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := newServer(t, readinessStub{err: tt.checkError})
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if response.Header().Get("X-Request-ID") == "" {
				t.Fatal("response is missing X-Request-ID")
			}
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["status"] != tt.wantValue {
				t.Fatalf("status body = %q, want %q", body["status"], tt.wantValue)
			}
		})
	}
}

func TestServerCapabilitiesRequireAuthentication(t *testing.T) {
	t.Parallel()
	server := newServer(t, readinessStub{})
	tests := []struct {
		name, token string
		want        int
	}{
		{name: "missing token", want: http.StatusUnauthorized},
		{name: "invalid token", token: "invalid-token", want: http.StatusUnauthorized},
		{name: "valid token", token: testToken, want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
			if tt.token != "" {
				request.Header.Set("Authorization", "Bearer "+tt.token)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d", response.Code, tt.want)
			}
			if tt.want == http.StatusOK {
				var body struct {
					Stage                  string `json:"stage"`
					RatingEnabled          bool   `json:"rating_enabled"`
					TicketLifecycleEnabled bool   `json:"ticket_lifecycle_enabled"`
					OutboxDeliveryEnabled  bool   `json:"outbox_delivery_enabled"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode capabilities: %v", err)
				}
				if body.Stage != "transactional_event_delivery" || !body.RatingEnabled ||
					!body.TicketLifecycleEnabled || !body.OutboxDeliveryEnabled {
					t.Fatalf("capabilities = %+v", body)
				}
			}
		})
	}
}

func TestServerFinalizesAuthenticatedResult(t *testing.T) {
	t.Parallel()
	finalizer := &resultFinalizerStub{}
	server := newServerWithResults(t, readinessStub{}, finalizer)
	finishedAt := time.Now().UTC().Add(-2 * time.Second)
	body := `{
		"event_id":"result-a","room_id":"room-a","mode_id":"mode-a",
		"deck_id":"deck-a","scoring_rules_version":"rules-v1",
		"finished_at":"` + finishedAt.Format(time.RFC3339Nano) + `",
		"available_at":"` + finishedAt.Add(time.Second).Format(time.RFC3339Nano) + `",
		"participants":[
			{"session_id":"session-a","player_id":"player-a","place":1,"features":{}},
			{"session_id":"session-b","player_id":"player-b","place":2,"features":{}},
			{"session_id":"session-c","player_id":"player-c","place":3,"features":{}},
			{"session_id":"session-d","player_id":"player-d","place":4,"features":{}},
			{"session_id":"session-e","player_id":"player-e","place":5,"features":{}}
		]
	}`
	request := httptest.NewRequest(http.MethodPost, "/v1/results", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if finalizer.command.EventID != "result-a" || finalizer.command.AcceptedAt.IsZero() {
		t.Fatalf("finalizer command = %+v", finalizer.command)
	}
}

func TestServerRejectsUnauthenticatedResult(t *testing.T) {
	t.Parallel()
	server := newServer(t, readinessStub{})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/results", strings.NewReader("{}")))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

type resultFinalizerStub struct {
	command tournament.FinalizeResultCommand
	result  tournament.FinalizedResult
	err     error
}

func (finalizer *resultFinalizerStub) Finalize(_ context.Context, command tournament.FinalizeResultCommand) (tournament.FinalizedResult, error) {
	finalizer.command = command
	return finalizer.result, finalizer.err
}

func newServer(t *testing.T, check httpapi.Readiness) *httpapi.Server {
	t.Helper()
	return newServerWithResults(t, check, &resultFinalizerStub{})
}

func newServerWithResults(t *testing.T, check httpapi.Readiness, results httpapi.ResultFinalizer) *httpapi.Server {
	t.Helper()
	return newServerWithTicketsAndResults(t, check, &ticketManagerStub{}, results)
}

func newServerWithTickets(t *testing.T, tickets httpapi.TicketManager) *httpapi.Server {
	t.Helper()
	return newServerWithTicketsAndResults(t, readinessStub{}, tickets, &resultFinalizerStub{})
}

func newServerWithTicketsAndResults(t *testing.T, check httpapi.Readiness, tickets httpapi.TicketManager, results httpapi.ResultFinalizer) *httpapi.Server {
	t.Helper()
	return newServerWithDependencies(t, check, tickets, results, &queryReaderStub{})
}

func newServerWithQueries(t *testing.T, queries httpapi.QueryReader) *httpapi.Server {
	t.Helper()
	return newServerWithDependencies(t, readinessStub{}, &ticketManagerStub{}, &resultFinalizerStub{}, queries)
}

func newServerWithDependencies(t *testing.T, check httpapi.Readiness, tickets httpapi.TicketManager, results httpapi.ResultFinalizer, queries httpapi.QueryReader) *httpapi.Server {
	t.Helper()
	server, err := httpapi.New(check, tickets, results, queries, slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Options{
		APIToken: testToken, ReadinessTimeout: time.Second, ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return server
}

type queryReaderStub struct {
	room      tournament.RoomState
	roomErr   error
	rating    tournament.PlayerRating
	ratingErr error

	roomID   string
	playerID string
	modeID   string
}

func (reader *queryReaderStub) GetRoom(_ context.Context, roomID string) (tournament.RoomState, error) {
	reader.roomID = roomID
	return reader.room, reader.roomErr
}

func (reader *queryReaderStub) GetRating(_ context.Context, playerID, modeID string) (tournament.PlayerRating, error) {
	reader.playerID = playerID
	reader.modeID = modeID
	return reader.rating, reader.ratingErr
}

type ticketManagerStub struct {
	acceptResult tournament.TicketMutation
	acceptErr    error
	cancelResult tournament.TicketMutation
	cancelErr    error
	state        tournament.TicketState
	getErr       error

	accepted  tournament.AcceptTicketCommand
	cancelled tournament.CancelTicketCommand
	got       string
}

func (manager *ticketManagerStub) Accept(_ context.Context, command tournament.AcceptTicketCommand) (tournament.TicketMutation, error) {
	manager.accepted = command
	return manager.acceptResult, manager.acceptErr
}

func (manager *ticketManagerStub) Cancel(_ context.Context, command tournament.CancelTicketCommand) (tournament.TicketMutation, error) {
	manager.cancelled = command
	return manager.cancelResult, manager.cancelErr
}

func (manager *ticketManagerStub) Get(_ context.Context, ticketID string) (tournament.TicketState, error) {
	manager.got = ticketID
	return manager.state, manager.getErr
}
