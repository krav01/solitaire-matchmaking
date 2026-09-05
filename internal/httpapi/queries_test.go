package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestServerReadsRoomState(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.September, 5, 10, 0, 0, 0, time.UTC)
	filledAt := createdAt.Add(4 * time.Second)
	reader := &queryReaderStub{room: tournament.RoomState{
		RoomID: "room-a", TournamentID: "daily", TournamentVersion: "v1",
		ModeID: "solitaire", PolicyVersion: "matching-v1", RatingModelVersion: "rating-v1",
		ScoringRulesVersion: "scoring-v1", SettlementVersion: "settlement-v1",
		DeckID: "deck-a", Capacity: 5, Status: tournament.RoomCollecting,
		AggregateVersion: 6, CreatedAt: createdAt, FillDeadline: createdAt.Add(time.Minute),
		FilledAt: &filledAt, ResultDeadline: timePointer(filledAt.Add(time.Minute)),
		Members: []tournament.RoomMember{{
			TicketID: "ticket-a", PlayerID: "player-a", SessionID: "session-a",
			Seat: 1, Status: tournament.SessionAllocated, AssignedAt: createdAt.Add(time.Second),
		}},
	}}
	server := newServerWithQueries(t, reader)
	response := serveAuthenticated(server.Handler(), http.MethodGet, "/v1/rooms/room-a", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if reader.roomID != "room-a" {
		t.Fatalf("room lookup = %q", reader.roomID)
	}

	var body struct {
		RoomID           string `json:"room_id"`
		Status           string `json:"status"`
		AggregateVersion int64  `json:"aggregate_version"`
		Members          []struct {
			PlayerID  string `json:"player_id"`
			SessionID string `json:"session_id"`
			Seat      int    `json:"seat"`
		} `json:"members"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode room response: %v", err)
	}
	if body.RoomID != "room-a" || body.Status != "collecting" || body.AggregateVersion != 6 ||
		len(body.Members) != 1 || body.Members[0].PlayerID != "player-a" ||
		body.Members[0].SessionID != "session-a" || body.Members[0].Seat != 1 {
		t.Fatalf("room response = %+v", body)
	}
}

func TestServerReadsCurrentPlayerRating(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, time.September, 5, 10, 0, 0, 0, time.UTC)
	reader := &queryReaderStub{rating: tournament.PlayerRating{
		PlayerID: "player-a", ModeID: "solitaire",
		Estimate: rating.Estimate{
			Mean: 26.5, Uncertainty: 6, Games: 12,
			ModelVersion: "rating-v1", UpdatedAt: updatedAt,
		},
		Revision: 4,
	}}
	server := newServerWithQueries(t, reader)
	response := serveAuthenticated(server.Handler(), http.MethodGet, "/v1/ratings/player-a?mode_id=solitaire", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if reader.playerID != "player-a" || reader.modeID != "solitaire" {
		t.Fatalf("rating lookup = player %q, mode %q", reader.playerID, reader.modeID)
	}

	var body struct {
		PlayerID string `json:"player_id"`
		ModeID   string `json:"mode_id"`
		Rating   struct {
			Mean         float64 `json:"mean"`
			ModelVersion string  `json:"model_version"`
		} `json:"rating"`
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode rating response: %v", err)
	}
	if body.PlayerID != "player-a" || body.ModeID != "solitaire" ||
		body.Rating.Mean != 26.5 || body.Rating.ModelVersion != "rating-v1" || body.Revision != 4 {
		t.Fatalf("rating response = %+v", body)
	}
}

func TestServerMapsQueryErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		reader     *queryReaderStub
		path       string
		wantStatus int
		wantError  string
	}{
		{name: "room absent", reader: &queryReaderStub{roomErr: tournament.ErrRoomNotFound}, path: "/v1/rooms/missing", wantStatus: http.StatusNotFound, wantError: "room_not_found"},
		{name: "mode absent", reader: &queryReaderStub{}, path: "/v1/ratings/player-a", wantStatus: http.StatusBadRequest, wantError: "invalid_rating_query"},
		{name: "duplicate mode", reader: &queryReaderStub{}, path: "/v1/ratings/player-a?mode_id=a&mode_id=b", wantStatus: http.StatusBadRequest, wantError: "invalid_rating_query"},
		{name: "rating absent", reader: &queryReaderStub{ratingErr: tournament.ErrRatingNotFound}, path: "/v1/ratings/player-a?mode_id=solitaire", wantStatus: http.StatusNotFound, wantError: "rating_not_found"},
		{name: "query failed", reader: &queryReaderStub{roomErr: errors.New("database unavailable")}, path: "/v1/rooms/room-a", wantStatus: http.StatusInternalServerError, wantError: "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := newServerWithQueries(t, test.reader)
			response := serveAuthenticated(server.Handler(), http.MethodGet, test.path, "", "")
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body["error"] != test.wantError {
				t.Fatalf("error = %q, want %q", body["error"], test.wantError)
			}
		})
	}
}

func TestServerQueryRoutesRequireAuthentication(t *testing.T) {
	t.Parallel()

	server := newServerWithQueries(t, &queryReaderStub{})
	for _, path := range []string{"/v1/rooms/room-a", "/v1/ratings/player-a?mode_id=solitaire"} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want %d", path, response.Code, http.StatusUnauthorized)
		}
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}
