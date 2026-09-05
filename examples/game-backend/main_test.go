package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/eventdelivery"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

const exampleToken = "0123456789abcdef0123456789abcdef"

func TestReceiverAppliesEventOnceAndAcceptsReplay(t *testing.T) {
	t.Parallel()

	var applied atomic.Int32
	handler, err := newReceiver(exampleToken, func(outboxEvent) error {
		applied.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("newReceiver() error = %v", err)
	}
	body := validEventBody(`{"room_id":"room-a"}`)

	for attempt := range 2 {
		response := deliver(handler, exampleToken, "event-a", body)
		if response.Code != http.StatusNoContent {
			t.Fatalf("attempt %d status = %d, body = %s", attempt+1, response.Code, response.Body.String())
		}
	}
	if got := applied.Load(); got != 1 {
		t.Fatalf("applied side effects = %d, want 1", got)
	}
}

func TestReceiverRejectsConflictingReplay(t *testing.T) {
	t.Parallel()

	var applied atomic.Int32
	handler, err := newReceiver(exampleToken, func(outboxEvent) error {
		applied.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("newReceiver() error = %v", err)
	}
	first := deliver(handler, exampleToken, "event-a", validEventBody(`{"room_id":"room-a"}`))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first status = %d", first.Code)
	}
	conflict := deliver(handler, exampleToken, "event-a", validEventBody(`{"room_id":"room-b"}`))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body = %s", conflict.Code, conflict.Body.String())
	}
	if got := applied.Load(); got != 1 {
		t.Fatalf("applied side effects = %d, want 1", got)
	}
}

func TestReceiverRejectsInvalidAuthenticationAndIdentity(t *testing.T) {
	t.Parallel()

	handler, err := newReceiver(exampleToken, func(outboxEvent) error { return nil })
	if err != nil {
		t.Fatalf("newReceiver() error = %v", err)
	}
	tests := []struct {
		name  string
		token string
		key   string
		want  int
	}{
		{name: "missing token", key: "event-a", want: http.StatusUnauthorized},
		{name: "wrong token", token: strings.Repeat("x", 32), key: "event-a", want: http.StatusUnauthorized},
		{name: "mismatched key", token: exampleToken, key: "event-b", want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := deliver(handler, test.token, test.key, validEventBody(`{"room_id":"room-a"}`))
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func deliver(handler http.Handler, token, key, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func validEventBody(payload string) string {
	return `{
		"event_id":"event-a",
		"aggregate_type":"room",
		"aggregate_id":"room-a",
		"aggregate_version":2,
		"event_type":"room.completed",
		"payload":` + payload + `,
		"occurred_at":"2026-09-05T06:00:00Z"
	}`
}

func TestReceiverConcurrentPublisherRetries(t *testing.T) {
	t.Parallel()

	var applied atomic.Int32
	handler, err := newReceiver(exampleToken, func(outboxEvent) error {
		applied.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	publisher, err := eventdelivery.NewHTTPPublisher(server.URL+"/events", exampleToken, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var event worker.OutboxEvent
	if err := json.Unmarshal([]byte(validEventBody(`{"room_id":"room-a"}`)), &event); err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			if err := publisher.Publish(t.Context(), event); err != nil {
				t.Errorf("Publish() error = %v", err)
			}
		})
	}
	group.Wait()

	if got := applied.Load(); got != 1 {
		t.Fatalf("applied = %d, want 1", got)
	}
}

func TestReceiverRetriesFailedApplication(t *testing.T) {
	t.Parallel()

	attempts := 0
	handler, err := newReceiver(exampleToken, func(outboxEvent) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary failure")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []int{http.StatusServiceUnavailable, http.StatusNoContent, http.StatusNoContent} {
		response := deliver(handler, exampleToken, "event-a", validEventBody(`{}`))
		if response.Code != want {
			t.Fatalf("status = %d, want %d", response.Code, want)
		}
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}
