// Package eventdelivery publishes transactional outbox events to the game backend.
package eventdelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

const responseDrainLimit = 32 << 10

type HTTPPublisher struct {
	endpoint string
	token    string
	client   *http.Client
}

func NewHTTPPublisher(endpoint, token string, timeout time.Duration) (*HTTPPublisher, error) {
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	if len(token) < 32 || strings.ContainsAny(token, " \r\n\t") {
		return nil, errors.New("outbox delivery token must contain at least 32 characters without whitespace")
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, errors.New("outbox delivery timeout must be positive and at most one minute")
	}

	return &HTTPPublisher{
		endpoint: endpoint,
		token:    token,
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (publisher *HTTPPublisher) Publish(ctx context.Context, event worker.OutboxEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode outbox event: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, publisher.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create outbox delivery request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+publisher.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", event.EventID)
	request.Header.Set("User-Agent", "solitaire-matchmaking-outbox/1")

	response, err := publisher.client.Do(request)
	if err != nil {
		return fmt.Errorf("deliver outbox event: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, responseDrainLimit))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("delivery endpoint returned HTTP %d", response.StatusCode)
	}

	return nil
}

func validateEndpoint(endpoint string) error {
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return errors.New("OUTBOX_DELIVERY_URL must be an absolute HTTP endpoint")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return errors.New("OUTBOX_DELIVERY_URL cannot contain user information or a fragment")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme != "http" || !loopbackHost(parsed.Hostname()) {
		return errors.New("OUTBOX_DELIVERY_URL must use HTTPS except for loopback development")
	}

	return nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}

	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

var _ worker.OutboxPublisher = (*HTTPPublisher)(nil)
