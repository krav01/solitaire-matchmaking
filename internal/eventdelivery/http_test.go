package eventdelivery_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/eventdelivery"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

func TestHTTPPublisherSendsAuthenticatedIdempotentEvent(t *testing.T) {
	t.Parallel()

	token := strings.Repeat("x", 32)
	received := make(chan worker.OutboxEvent, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("request method = %q, authorization = %q", request.Method, request.Header.Get("Authorization"))
		}
		if request.Header.Get("Idempotency-Key") != "event-a" || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request headers = %v", request.Header)
		}

		var event worker.OutboxEvent
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			t.Errorf("decode event: %v", err)
		}
		received <- event
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	publisher, err := eventdelivery.NewHTTPPublisher(server.URL, token, time.Second)
	if err != nil {
		t.Fatalf("NewHTTPPublisher() error = %v", err)
	}
	event := validOutboxEvent()
	if err := publisher.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if got := <-received; got.EventID != event.EventID || string(got.Payload) != string(event.Payload) {
		t.Fatalf("published event = %+v", got)
	}
}

func TestHTTPPublisherRejectsFailureAndUnsafeEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	token := strings.Repeat("x", 32)
	publisher, err := eventdelivery.NewHTTPPublisher(server.URL, token, time.Second)
	if err != nil {
		t.Fatalf("NewHTTPPublisher() error = %v", err)
	}
	if err := publisher.Publish(context.Background(), validOutboxEvent()); err == nil {
		t.Fatal("Publish() error = nil")
	}
	if _, err := eventdelivery.NewHTTPPublisher("http://backend.example.com/events", token, time.Second); err == nil {
		t.Fatal("NewHTTPPublisher(insecure endpoint) error = nil")
	}
}

func validOutboxEvent() worker.OutboxEvent {
	return worker.OutboxEvent{
		EventID: "event-a", AggregateType: "room", AggregateID: "room-a",
		AggregateVersion: 2, EventType: "room.completed",
		Payload:    json.RawMessage(`{"room_id":"room-a"}`),
		OccurredAt: time.Date(2026, time.September, 5, 6, 0, 0, 0, time.UTC),
	}
}
