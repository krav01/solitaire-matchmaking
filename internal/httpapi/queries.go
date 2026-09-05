package httpapi

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

type roomResponse struct {
	RoomID              string                `json:"room_id"`
	TournamentID        string                `json:"tournament_id"`
	TournamentVersion   string                `json:"tournament_version"`
	ModeID              string                `json:"mode_id"`
	PolicyVersion       string                `json:"policy_version"`
	RatingModelVersion  string                `json:"rating_model_version"`
	ScoringRulesVersion string                `json:"scoring_rules_version"`
	SettlementVersion   string                `json:"settlement_version"`
	DeckID              string                `json:"deck_id"`
	Capacity            int                   `json:"capacity"`
	Status              tournament.RoomStatus `json:"status"`
	AggregateVersion    int64                 `json:"aggregate_version"`
	CreatedAt           time.Time             `json:"created_at"`
	FillDeadline        time.Time             `json:"fill_deadline"`
	FilledAt            *time.Time            `json:"filled_at,omitempty"`
	ResultDeadline      *time.Time            `json:"result_deadline,omitempty"`
	CompletedAt         *time.Time            `json:"completed_at,omitempty"`
	ExpiredAt           *time.Time            `json:"expired_at,omitempty"`
	CancelledAt         *time.Time            `json:"cancelled_at,omitempty"`
	Members             []roomMemberResponse  `json:"members"`
}

type roomMemberResponse struct {
	TicketID    string                   `json:"ticket_id"`
	PlayerID    string                   `json:"player_id"`
	SessionID   string                   `json:"session_id"`
	Seat        int                      `json:"seat"`
	Status      tournament.SessionStatus `json:"status"`
	AssignedAt  time.Time                `json:"assigned_at"`
	StartedAt   *time.Time               `json:"started_at,omitempty"`
	SubmittedAt *time.Time               `json:"submitted_at,omitempty"`
	ForfeitedAt *time.Time               `json:"forfeited_at,omitempty"`
}

type ratingResponse struct {
	PlayerID string          `json:"player_id"`
	ModeID   string          `json:"mode_id"`
	Rating   rating.Estimate `json:"rating"`
	Revision int64           `json:"revision"`
}

func (s *Server) registerQueryRoutes(mux *http.ServeMux, queries QueryReader, expectedToken [sha256.Size]byte) {
	mux.HandleFunc("GET /v1/rooms/{room_id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r, expectedToken) {
			return
		}

		state, err := queries.GetRoom(r.Context(), r.PathValue("room_id"))
		if err != nil {
			s.respondQueryError(w, r, err)
			return
		}
		s.respond(w, r, http.StatusOK, newRoomResponse(state))
	})

	mux.HandleFunc("GET /v1/ratings/{player_id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r, expectedToken) {
			return
		}

		modeIDs := r.URL.Query()["mode_id"]
		if len(modeIDs) != 1 || modeIDs[0] == "" {
			s.respond(w, r, http.StatusBadRequest, map[string]string{"error": "invalid_rating_query"})
			return
		}
		current, err := queries.GetRating(r.Context(), r.PathValue("player_id"), modeIDs[0])
		if err != nil {
			s.respondQueryError(w, r, err)
			return
		}
		s.respond(w, r, http.StatusOK, ratingResponse{
			PlayerID: current.PlayerID, ModeID: current.ModeID,
			Rating: current.Estimate, Revision: current.Revision,
		})
	})
}

func (s *Server) respondQueryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tournament.ErrRoomNotFound):
		s.respond(w, r, http.StatusNotFound, map[string]string{"error": "room_not_found"})
	case errors.Is(err, tournament.ErrRatingNotFound):
		s.respond(w, r, http.StatusNotFound, map[string]string{"error": "rating_not_found"})
	default:
		s.logger.ErrorContext(r.Context(), "query failed", "error", err)
		s.respond(w, r, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
	}
}

func newRoomResponse(state tournament.RoomState) roomResponse {
	members := make([]roomMemberResponse, len(state.Members))
	for index, member := range state.Members {
		members[index] = roomMemberResponse{
			TicketID: member.TicketID, PlayerID: member.PlayerID,
			SessionID: member.SessionID, Seat: member.Seat, Status: member.Status,
			AssignedAt: member.AssignedAt, StartedAt: member.StartedAt,
			SubmittedAt: member.SubmittedAt, ForfeitedAt: member.ForfeitedAt,
		}
	}

	return roomResponse{
		RoomID: state.RoomID, TournamentID: state.TournamentID,
		TournamentVersion: state.TournamentVersion, ModeID: state.ModeID,
		PolicyVersion: state.PolicyVersion, RatingModelVersion: state.RatingModelVersion,
		ScoringRulesVersion: state.ScoringRulesVersion, SettlementVersion: state.SettlementVersion,
		DeckID: state.DeckID, Capacity: state.Capacity, Status: state.Status,
		AggregateVersion: state.AggregateVersion, CreatedAt: state.CreatedAt,
		FillDeadline: state.FillDeadline, FilledAt: state.FilledAt,
		ResultDeadline: state.ResultDeadline, CompletedAt: state.CompletedAt,
		ExpiredAt: state.ExpiredAt, CancelledAt: state.CancelledAt, Members: members,
	}
}
