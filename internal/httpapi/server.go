// Package httpapi exposes implemented service endpoints. Business endpoints are
// documented as planned and are deliberately not registered by the foundation.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

// Readiness is implemented by the PostgreSQL pool and extended at later stages.
type Readiness interface{ Ping(context.Context) error }

type ResultFinalizer interface {
	Finalize(context.Context, tournament.FinalizeResultCommand) (tournament.FinalizedResult, error)
}

type TicketManager interface {
	Accept(context.Context, tournament.AcceptTicketCommand) (tournament.TicketMutation, error)
	Cancel(context.Context, tournament.CancelTicketCommand) (tournament.TicketMutation, error)
	Get(context.Context, string) (tournament.TicketState, error)
}

type QueryReader interface {
	GetRoom(context.Context, string) (tournament.RoomState, error)
	GetRating(context.Context, string, string) (tournament.PlayerRating, error)
}

type HTTPMetrics interface {
	Handler() http.Handler
	ObserveHTTPRequest(method, route string, status int, duration time.Duration)
}

type Options struct {
	APIToken         string
	ReadinessTimeout time.Duration
	ShutdownTimeout  time.Duration
	Metrics          HTTPMetrics
}

type Server struct {
	http     *http.Server
	logger   *slog.Logger
	shutdown time.Duration
	draining atomic.Bool
}

func New(check Readiness, tickets TicketManager, results ResultFinalizer, queries QueryReader, logger *slog.Logger, opts Options) (*Server, error) {
	if check == nil || tickets == nil || results == nil || queries == nil || logger == nil {
		return nil, errors.New("readiness checker, ticket manager, result finalizer, query reader and logger are required")
	}
	if len(opts.APIToken) < 32 || opts.ReadinessTimeout <= 0 || opts.ReadinessTimeout > 5*time.Second ||
		opts.ShutdownTimeout <= 0 || opts.Metrics == nil {
		return nil, errors.New("invalid HTTP server options")
	}
	metricsHandler := opts.Metrics.Handler()
	if metricsHandler == nil {
		return nil, errors.New("invalid HTTP server options")
	}
	s := &Server{logger: logger, shutdown: opts.ShutdownTimeout}
	expectedToken := sha256.Sum256([]byte(opts.APIToken))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		s.respond(w, r, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.draining.Load() {
			s.respond(w, r, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), opts.ReadinessTimeout)
		defer cancel()
		if err := check.Ping(ctx); err != nil {
			s.respond(w, r, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		s.respond(w, r, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r, expectedToken) {
			return
		}
		s.respond(w, r, http.StatusOK, map[string]any{
			"service": "solitaire-matchmaking", "stage": "operational_observability",
			"rating_enabled": true, "matchmaking_enabled": true, "ticket_lifecycle_enabled": true,
			"result_ingestion_enabled": true,
			"outbox_delivery_enabled":  true,
			"metrics_enabled":          true,
			"planned_room_sizes":       []int{5, 6, 7},
		})
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r, expectedToken) {
			return
		}
		metricsHandler.ServeHTTP(w, r)
	})
	s.registerTicketRoutes(mux, tickets, expectedToken)
	s.registerResultRoutes(mux, results, expectedToken)
	s.registerQueryRoutes(mux, queries, expectedToken)
	baseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", rand.Text())
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
	s.http = &http.Server{
		Handler:           observeHTTPRequests(baseHandler, opts.Metrics),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 << 10,
		ErrorLog:       slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	return s, nil
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}

	return writer.ResponseWriter.Write(body)
}

func (writer *statusWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func observeHTTPRequests(next http.Handler, metrics HTTPMetrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		writer := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r)
		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}
		route := r.Pattern
		if _, pattern, found := strings.Cut(route, " "); found {
			route = pattern
		}
		if route == "" {
			route = "unmatched"
		}
		metrics.ObserveHTTPRequest(r.Method, route, status, time.Since(startedAt))
	})
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, expectedToken [sha256.Size]byte) bool {
	header := r.Header.Get("Authorization")
	providedToken := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
	if strings.HasPrefix(header, "Bearer ") && subtle.ConstantTimeCompare(providedToken[:], expectedToken[:]) == 1 {
		return true
	}
	w.Header().Set("WWW-Authenticate", "Bearer")
	s.respond(w, r, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	return false
}

func (s *Server) Handler() http.Handler { return s.http.Handler }

// Serve owns the listener and may be called once. Cancellation stops acceptance
// and drains in-flight requests before the database pool is closed by the caller.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	result := make(chan error, 1)
	go func() { result <- s.http.Serve(listener) }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		s.draining.Store(true)
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdown)
		defer cancel()
		if err := s.http.Shutdown(shutdownCtx); err != nil {
			if closeErr := s.http.Close(); closeErr != nil {
				s.logger.Error("force close HTTP server failed")
			}
			<-result
			return fmt.Errorf("drain HTTP: %w", err)
		}
		<-result
		return nil
	}
}

func (s *Server) respond(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.logger.WarnContext(r.Context(), "write response failed", "request_id", w.Header().Get("X-Request-ID"))
	}
}
