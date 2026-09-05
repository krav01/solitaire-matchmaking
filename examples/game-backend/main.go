// Command game-backend demonstrates authenticated, idempotent outbox delivery.
// Its in-memory store is intentionally development-only; production receivers
// must persist the event identity and business side effect in one transaction.
package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const maxEventBodyBytes = 1 << 20

type outboxEvent struct {
	EventID          string          `json:"event_id"`
	AggregateType    string          `json:"aggregate_type"`
	AggregateID      string          `json:"aggregate_id"`
	AggregateVersion int64           `json:"aggregate_version"`
	EventType        string          `json:"event_type"`
	Payload          json.RawMessage `json:"payload"`
	OccurredAt       time.Time       `json:"occurred_at"`
}

func (event outboxEvent) validate() error {
	if event.EventID == "" || event.AggregateType == "" || event.AggregateID == "" || event.EventType == "" {
		return errors.New("event identities are required")
	}
	if event.AggregateVersion <= 0 || event.OccurredAt.IsZero() {
		return errors.New("event version and occurrence time are required")
	}
	if payload := strings.TrimSpace(string(event.Payload)); len(payload) == 0 || payload[0] != '{' || !json.Valid(event.Payload) {
		return errors.New("event payload must be a JSON object")
	}

	return nil
}

type receiver struct {
	tokenHash [sha256.Size]byte
	apply     func(outboxEvent) error

	mu      sync.Mutex
	applied map[string][sha256.Size]byte
}

func newReceiver(token string, apply func(outboxEvent) error) (*receiver, error) {
	if len(token) < 32 || strings.ContainsAny(token, " \r\n\t") {
		return nil, errors.New("delivery token must contain at least 32 characters without whitespace")
	}
	if apply == nil {
		return nil, errors.New("event side effect is required")
	}

	return &receiver{
		tokenHash: sha256.Sum256([]byte(token)),
		apply:     apply,
		applied:   make(map[string][sha256.Size]byte),
	}, nil
}

func (receiver *receiver) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !receiver.authorized(request.Header.Get("Authorization")) {
		response.Header().Set("WWW-Authenticate", "Bearer")
		writeError(response, http.StatusUnauthorized, "unauthorized")
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, maxEventBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var event outboxEvent
	if err := decoder.Decode(&event); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_event")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, "invalid_event")
		return
	}
	if err := event.validate(); err != nil || request.Header.Get("Idempotency-Key") != event.EventID {
		writeError(response, http.StatusBadRequest, "invalid_event")
		return
	}

	body, err := json.Marshal(event)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	digest := sha256.Sum256(body)

	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if stored, exists := receiver.applied[event.EventID]; exists {
		if subtle.ConstantTimeCompare(stored[:], digest[:]) != 1 {
			writeError(response, http.StatusConflict, "event_conflict")
			return
		}
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if err := receiver.apply(event); err != nil {
		writeError(response, http.StatusServiceUnavailable, "apply_failed")
		return
	}
	receiver.applied[event.EventID] = digest
	response.WriteHeader(http.StatusNoContent)
}

func (receiver *receiver) authorized(header string) bool {
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	provided := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))

	return subtle.ConstantTimeCompare(provided[:], receiver.tokenHash[:]) == 1
}

func writeError(response http.ResponseWriter, status int, code string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{"error": code})
}

func main() {
	token := os.Getenv("OUTBOX_DELIVERY_TOKEN")
	handler, err := newReceiver(token, func(event outboxEvent) error {
		log.Printf("apply event id=%q type=%q aggregate=%q/%q version=%d", event.EventID, event.EventType, event.AggregateType, event.AggregateID, event.AggregateVersion)
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	address := os.Getenv("GAME_BACKEND_ADDR")
	if address == "" {
		address = "127.0.0.1:8090"
	}
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Print("game-backend example starting")
	log.Fatal(server.ListenAndServe())
}
