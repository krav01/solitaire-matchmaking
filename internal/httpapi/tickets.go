package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

const maxTicketBodyBytes = 1 << 20

type acceptTicketRequest struct {
	EntryID           string          `json:"entry_id"`
	PlayerID          string          `json:"player_id"`
	TournamentID      string          `json:"tournament_id"`
	TournamentVersion string          `json:"tournament_version"`
	RequestedAt       time.Time       `json:"requested_at"`
	SnapshotAt        time.Time       `json:"snapshot_at"`
	RatingSnapshot    rating.Estimate `json:"rating_snapshot"`
}

type ticketResponse struct {
	TicketID          string                  `json:"ticket_id"`
	EntryID           string                  `json:"entry_id"`
	PlayerID          string                  `json:"player_id"`
	TournamentID      string                  `json:"tournament_id"`
	TournamentVersion string                  `json:"tournament_version"`
	Status            tournament.TicketStatus `json:"status"`
	RequestedAt       time.Time               `json:"requested_at"`
	AssignedAt        *time.Time              `json:"assigned_at,omitempty"`
	CancelledAt       *time.Time              `json:"cancelled_at,omitempty"`
	ExpiredAt         *time.Time              `json:"expired_at,omitempty"`
	SnapshotAt        time.Time               `json:"snapshot_at"`
	RatingSnapshot    rating.Estimate         `json:"rating_snapshot"`
	AggregateVersion  int64                   `json:"aggregate_version"`
}

type ticketMutationResponse struct {
	Ticket  ticketResponse `json:"ticket"`
	Changed bool           `json:"changed"`
	Replay  bool           `json:"replay"`
}

type assignmentResponse struct {
	AssignmentID   string     `json:"assignment_id"`
	RoomID         string     `json:"room_id"`
	SessionID      string     `json:"session_id"`
	Seat           int        `json:"seat"`
	AssignedAt     time.Time  `json:"assigned_at"`
	TicketVersion  int64      `json:"ticket_version"`
	RoomVersion    int64      `json:"room_version"`
	RoomFilled     bool       `json:"room_filled"`
	ResultDeadline *time.Time `json:"result_deadline,omitempty"`
}

type ticketStateResponse struct {
	Ticket     ticketResponse      `json:"ticket"`
	Assignment *assignmentResponse `json:"assignment,omitempty"`
}

func (s *Server) registerTicketRoutes(mux *http.ServeMux, tickets TicketManager, expectedToken [sha256.Size]byte) {
	mux.HandleFunc("POST /v1/tickets", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r, expectedToken) {
			return
		}

		request, ok := s.decodeTicketRequest(w, r)
		if !ok {
			return
		}
		command := tournament.AcceptTicketCommand{
			Ticket: tournament.Ticket{
				ID: rand.Text(), EntryID: request.EntryID, PlayerID: request.PlayerID,
				TournamentID: request.TournamentID, TournamentVersion: request.TournamentVersion,
				Status: tournament.TicketQueued, RequestedAt: request.RequestedAt,
				SnapshotAt: request.SnapshotAt, RatingSnapshot: request.RatingSnapshot,
			},
			EventID: rand.Text(),
		}
		if err := command.Validate(); err != nil {
			s.respond(w, r, http.StatusBadRequest, map[string]string{"error": "invalid_ticket"})
			return
		}

		mutation, err := tickets.Accept(r.Context(), command)
		if err != nil {
			s.respondTicketError(w, r, err)
			return
		}
		status := http.StatusCreated
		if mutation.Replay {
			status = http.StatusOK
		}
		s.respond(w, r, status, newTicketMutationResponse(mutation))
	})

	mux.HandleFunc("GET /v1/tickets/{ticket_id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r, expectedToken) {
			return
		}

		state, err := tickets.Get(r.Context(), r.PathValue("ticket_id"))
		if err != nil {
			s.respondTicketError(w, r, err)
			return
		}
		s.respond(w, r, http.StatusOK, newTicketStateResponse(state))
	})

	mux.HandleFunc("DELETE /v1/tickets/{ticket_id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r, expectedToken) {
			return
		}

		commandID := r.Header.Get("Idempotency-Key")
		command := tournament.CancelTicketCommand{
			TicketID: r.PathValue("ticket_id"), CommandID: commandID,
			EventID: rand.Text(), CancelledAt: time.Now().UTC(),
		}
		if err := command.Validate(); err != nil {
			s.respond(w, r, http.StatusBadRequest, map[string]string{"error": "invalid_cancellation"})
			return
		}

		mutation, err := tickets.Cancel(r.Context(), command)
		if err != nil {
			s.respondTicketError(w, r, err)
			return
		}
		s.respond(w, r, http.StatusOK, newTicketMutationResponse(mutation))
	})
}

func (s *Server) decodeTicketRequest(w http.ResponseWriter, r *http.Request) (acceptTicketRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTicketBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request acceptTicketRequest
	if err := decoder.Decode(&request); err != nil {
		s.respond(w, r, http.StatusBadRequest, map[string]string{"error": "invalid_ticket"})
		return acceptTicketRequest{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.respond(w, r, http.StatusBadRequest, map[string]string{"error": "invalid_ticket"})
		return acceptTicketRequest{}, false
	}

	return request, true
}

func (s *Server) respondTicketError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tournament.ErrTournamentNotFound):
		s.respond(w, r, http.StatusNotFound, map[string]string{"error": "tournament_not_found"})
	case errors.Is(err, tournament.ErrTicketNotFound):
		s.respond(w, r, http.StatusNotFound, map[string]string{"error": "ticket_not_found"})
	case errors.Is(err, tournament.ErrIdempotencyConflict):
		s.respond(w, r, http.StatusConflict, map[string]string{"error": "idempotency_conflict"})
	case errors.Is(err, tournament.ErrTicketNotQueued):
		s.respond(w, r, http.StatusConflict, map[string]string{"error": "ticket_not_queued"})
	default:
		s.logger.ErrorContext(r.Context(), "ticket operation failed", "error", err)
		s.respond(w, r, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
	}
}

func newTicketMutationResponse(mutation tournament.TicketMutation) ticketMutationResponse {
	return ticketMutationResponse{
		Ticket: newTicketResponse(mutation.Ticket), Changed: mutation.Changed, Replay: mutation.Replay,
	}
}

func newTicketStateResponse(state tournament.TicketState) ticketStateResponse {
	response := ticketStateResponse{Ticket: newTicketResponse(state.Ticket)}
	if state.Assignment != nil {
		assignment := state.Assignment
		response.Assignment = &assignmentResponse{
			AssignmentID: assignment.AssignmentID, RoomID: assignment.RoomID,
			SessionID: assignment.SessionID, Seat: assignment.Seat,
			AssignedAt: assignment.AssignedAt, TicketVersion: assignment.TicketVersion,
			RoomVersion: assignment.RoomVersion, RoomFilled: assignment.RoomFilled,
			ResultDeadline: assignment.ResultDeadline,
		}
	}

	return response
}

func newTicketResponse(ticket tournament.Ticket) ticketResponse {
	return ticketResponse{
		TicketID: ticket.ID, EntryID: ticket.EntryID, PlayerID: ticket.PlayerID,
		TournamentID: ticket.TournamentID, TournamentVersion: ticket.TournamentVersion,
		Status: ticket.Status, RequestedAt: ticket.RequestedAt, AssignedAt: ticket.AssignedAt,
		CancelledAt: ticket.CancelledAt, ExpiredAt: ticket.ExpiredAt,
		SnapshotAt: ticket.SnapshotAt, RatingSnapshot: ticket.RatingSnapshot,
		AggregateVersion: ticket.AggregateVersion,
	}
}
