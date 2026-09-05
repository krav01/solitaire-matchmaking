package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestServerAcceptsAndReplaysTicket(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		replay     bool
		wantStatus int
	}{
		{name: "created", wantStatus: http.StatusCreated},
		{name: "replayed", replay: true, wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			requestedAt := time.Date(2026, time.September, 5, 10, 0, 0, 0, time.UTC)
			stored := ticketFixture("stored-ticket", requestedAt)
			manager := &ticketManagerStub{acceptResult: tournament.TicketMutation{
				Ticket: stored, Changed: !test.replay, Replay: test.replay,
			}}
			server := newServerWithTickets(t, manager)
			body := `{
				"entry_id":"entry-a","player_id":"player-a",
				"tournament_id":"daily","tournament_version":"v1",
				"requested_at":"2026-09-05T10:00:00Z",
				"snapshot_at":"2026-09-05T10:00:00Z",
				"rating_snapshot":{
					"mean":25,"uncertainty":8,"games":3,
					"model_version":"rating-v1","updated_at":"2026-09-05T09:59:00Z"
				}
			}`
			response := serveAuthenticated(server.Handler(), http.MethodPost, "/v1/tickets", body, "")
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if manager.accepted.Ticket.ID == "" || manager.accepted.EventID == "" {
				t.Fatalf("generated identities are missing: %+v", manager.accepted)
			}
			if manager.accepted.Ticket.EntryID != "entry-a" || manager.accepted.Ticket.Status != tournament.TicketQueued {
				t.Fatalf("accepted ticket = %+v", manager.accepted.Ticket)
			}

			var result struct {
				Ticket struct {
					TicketID string `json:"ticket_id"`
				} `json:"ticket"`
				Replay bool `json:"replay"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if result.Ticket.TicketID != stored.ID || result.Replay != test.replay {
				t.Fatalf("response = %+v", result)
			}
		})
	}
}

func TestServerReadsAssignedTicket(t *testing.T) {
	t.Parallel()

	assignedAt := time.Date(2026, time.September, 5, 10, 0, 2, 0, time.UTC)
	ticket := ticketFixture("ticket-a", assignedAt.Add(-2*time.Second))
	ticket.Status = tournament.TicketAssigned
	ticket.AssignedAt = &assignedAt
	manager := &ticketManagerStub{state: tournament.TicketState{
		Ticket: ticket,
		Assignment: &tournament.Assignment{
			AssignmentID: "assignment-a", TicketID: ticket.ID, RoomID: "room-a",
			SessionID: "session-a", PlayerID: ticket.PlayerID, Seat: 3,
			AssignedAt: assignedAt, TicketVersion: 2, RoomVersion: 4,
		},
	}}
	server := newServerWithTickets(t, manager)
	response := serveAuthenticated(server.Handler(), http.MethodGet, "/v1/tickets/ticket-a", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if manager.got != "ticket-a" {
		t.Fatalf("ticket lookup = %q", manager.got)
	}
	var result struct {
		Ticket struct {
			Status string `json:"status"`
		} `json:"ticket"`
		Assignment struct {
			RoomID    string `json:"room_id"`
			SessionID string `json:"session_id"`
			Seat      int    `json:"seat"`
		} `json:"assignment"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Ticket.Status != "assigned" || result.Assignment.RoomID != "room-a" ||
		result.Assignment.SessionID != "session-a" || result.Assignment.Seat != 3 {
		t.Fatalf("ticket state = %+v", result)
	}
}

func TestServerCancelsTicketWithIdempotencyKey(t *testing.T) {
	t.Parallel()

	manager := &ticketManagerStub{cancelResult: tournament.TicketMutation{
		Ticket:  ticketFixture("ticket-a", time.Date(2026, time.September, 5, 10, 0, 0, 0, time.UTC)),
		Changed: true,
	}}
	server := newServerWithTickets(t, manager)
	response := serveAuthenticated(server.Handler(), http.MethodDelete, "/v1/tickets/ticket-a", "", "cancel-a")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if manager.cancelled.TicketID != "ticket-a" || manager.cancelled.CommandID != "cancel-a" ||
		manager.cancelled.EventID == "" || manager.cancelled.CancelledAt.IsZero() {
		t.Fatalf("cancellation = %+v", manager.cancelled)
	}
}

func TestServerMapsTicketValidationAndDomainErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		manager    *ticketManagerStub
		method     string
		path       string
		body       string
		key        string
		wantStatus int
		wantError  string
	}{
		{name: "invalid ticket", manager: &ticketManagerStub{}, method: http.MethodPost, path: "/v1/tickets", body: `{"unknown":true}`, wantStatus: http.StatusBadRequest, wantError: "invalid_ticket"},
		{name: "missing cancellation identity", manager: &ticketManagerStub{}, method: http.MethodDelete, path: "/v1/tickets/ticket-a", wantStatus: http.StatusBadRequest, wantError: "invalid_cancellation"},
		{name: "ticket absent", manager: &ticketManagerStub{getErr: tournament.ErrTicketNotFound}, method: http.MethodGet, path: "/v1/tickets/missing", wantStatus: http.StatusNotFound, wantError: "ticket_not_found"},
		{name: "tournament absent", manager: &ticketManagerStub{acceptErr: tournament.ErrTournamentNotFound}, method: http.MethodPost, path: "/v1/tickets", body: validTicketBody(), wantStatus: http.StatusNotFound, wantError: "tournament_not_found"},
		{name: "identity conflict", manager: &ticketManagerStub{acceptErr: tournament.ErrIdempotencyConflict}, method: http.MethodPost, path: "/v1/tickets", body: validTicketBody(), wantStatus: http.StatusConflict, wantError: "idempotency_conflict"},
		{name: "already assigned", manager: &ticketManagerStub{cancelErr: tournament.ErrTicketNotQueued}, method: http.MethodDelete, path: "/v1/tickets/ticket-a", key: "cancel-a", wantStatus: http.StatusConflict, wantError: "ticket_not_queued"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := newServerWithTickets(t, test.manager)
			response := serveAuthenticated(server.Handler(), test.method, test.path, test.body, test.key)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			var result map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if result["error"] != test.wantError {
				t.Fatalf("error = %q, want %q", result["error"], test.wantError)
			}
		})
	}
}

func TestServerTicketRoutesRequireAuthentication(t *testing.T) {
	t.Parallel()

	server := newServerWithTickets(t, &ticketManagerStub{})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/tickets/ticket-a", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func serveAuthenticated(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testToken)
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func ticketFixture(ticketID string, requestedAt time.Time) tournament.Ticket {
	return tournament.Ticket{
		ID: ticketID, EntryID: "entry-a", PlayerID: "player-a",
		TournamentID: "daily", TournamentVersion: "v1", Status: tournament.TicketQueued,
		RequestedAt: requestedAt, SnapshotAt: requestedAt,
		RatingSnapshot: rating.Estimate{
			Mean: 25, Uncertainty: 8, Games: 3, ModelVersion: "rating-v1",
			UpdatedAt: requestedAt.Add(-time.Minute),
		},
		AggregateVersion: 1,
	}
}

func validTicketBody() string {
	return `{
		"entry_id":"entry-a","player_id":"player-a",
		"tournament_id":"daily","tournament_version":"v1",
		"requested_at":"2026-09-05T10:00:00Z",
		"snapshot_at":"2026-09-05T10:00:00Z",
		"rating_snapshot":{
			"mean":25,"uncertainty":8,"games":3,
			"model_version":"rating-v1","updated_at":"2026-09-05T09:59:00Z"
		}
	}`
}
