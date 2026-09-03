package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/httpapi"
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
		})
	}
}

func newServer(t *testing.T, check httpapi.Readiness) *httpapi.Server {
	t.Helper()
	server, err := httpapi.New(check, slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Options{
		APIToken: testToken, ReadinessTimeout: time.Second, ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return server
}
