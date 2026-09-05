package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

const maxResultBodyBytes = 1 << 20

func (s *Server) registerResultRoutes(mux *http.ServeMux, results ResultFinalizer, expectedToken [sha256.Size]byte) {
	mux.HandleFunc("POST /v1/results", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r, expectedToken) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxResultBodyBytes)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var command tournament.FinalizeResultCommand
		if err := decoder.Decode(&command); err != nil {
			s.respond(w, r, http.StatusBadRequest, map[string]string{"error": "invalid_result"})
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			s.respond(w, r, http.StatusBadRequest, map[string]string{"error": "invalid_result"})
			return
		}
		command.AcceptedAt = time.Now().UTC()
		if err := command.Validate(); err != nil {
			s.respond(w, r, http.StatusBadRequest, map[string]string{"error": "invalid_result"})
			return
		}
		result, err := results.Finalize(r.Context(), command)
		if err != nil {
			s.respondResultError(w, r, err)
			return
		}
		status := http.StatusCreated
		if result.Replay {
			status = http.StatusOK
		}
		s.respond(w, r, status, result)
	})
}

func (s *Server) respondResultError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tournament.ErrResultRoomNotFound):
		s.respond(w, r, http.StatusNotFound, map[string]string{"error": "room_not_found"})
	case errors.Is(err, tournament.ErrResultConflict):
		s.respond(w, r, http.StatusConflict, map[string]string{"error": "result_conflict"})
	case errors.Is(err, tournament.ErrResultRoomNotCollecting):
		s.respond(w, r, http.StatusConflict, map[string]string{"error": "room_not_collecting"})
	case errors.Is(err, tournament.ErrResultDeadlinePassed):
		s.respond(w, r, http.StatusConflict, map[string]string{"error": "result_deadline_passed"})
	case errors.Is(err, tournament.ErrResultParticipantsMismatch):
		s.respond(w, r, http.StatusConflict, map[string]string{"error": "participants_mismatch"})
	default:
		s.logger.ErrorContext(r.Context(), "finalize result failed", "error", err)
		s.respond(w, r, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
	}
}
