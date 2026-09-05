package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
)

const maxOutboxDeliveryError = 1024

// OutboxQueue claims due events without holding database locks during network
// delivery. A claim token and its database-time lease fence acknowledgements.
type OutboxQueue struct {
	pool *pgxpool.Pool
}

func NewOutboxQueue(pool *pgxpool.Pool) (*OutboxQueue, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}

	return &OutboxQueue{pool: pool}, nil
}

func (queue *OutboxQueue) ClaimOutboxEvents(ctx context.Context, request worker.OutboxClaimRequest) ([]worker.OutboxClaim, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}

	rows, err := queue.pool.Query(ctx, `
WITH due AS (
    SELECT candidate.event_id
    FROM outbox_events AS candidate
    WHERE candidate.delivered_at IS NULL
      AND candidate.available_at <= $1
      AND (candidate.claimed_until IS NULL OR candidate.claimed_until <= $1)
      AND NOT EXISTS (
          SELECT 1
          FROM outbox_events AS predecessor
          WHERE predecessor.aggregate_type = candidate.aggregate_type
            AND predecessor.aggregate_id = candidate.aggregate_id
            AND predecessor.delivered_at IS NULL
            AND ROW(predecessor.aggregate_version, predecessor.event_id)
                < ROW(candidate.aggregate_version, candidate.event_id)
      )
    ORDER BY candidate.available_at, candidate.occurred_at, candidate.event_id
    FOR UPDATE OF candidate SKIP LOCKED
    LIMIT $2
)
UPDATE outbox_events AS event
SET claimed_by = $3,
    claimed_until = $4,
    attempt_count = event.attempt_count + 1
FROM due
WHERE event.event_id = due.event_id
RETURNING event.event_id, event.aggregate_type, event.aggregate_id,
          event.aggregate_version, event.event_type, event.payload,
          event.occurred_at, event.claimed_by, event.attempt_count,
          event.claimed_until`,
		request.ClaimedAt, request.Limit, request.Token, request.LeaseUntil,
	)
	if err != nil {
		return nil, fmt.Errorf("claim outbox events: %w", err)
	}
	defer rows.Close()

	claims := make([]worker.OutboxClaim, 0, request.Limit)
	for rows.Next() {
		var claim worker.OutboxClaim
		var payload []byte
		if err := rows.Scan(
			&claim.Event.EventID, &claim.Event.AggregateType, &claim.Event.AggregateID,
			&claim.Event.AggregateVersion, &claim.Event.EventType, &payload,
			&claim.Event.OccurredAt, &claim.Token, &claim.Attempt, &claim.LeaseUntil,
		); err != nil {
			return nil, fmt.Errorf("scan outbox claim: %w", err)
		}
		claim.Event.Payload = json.RawMessage(payload)
		if err := claim.Validate(); err != nil {
			return nil, fmt.Errorf("validate outbox claim: %w", err)
		}
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read outbox claims: %w", err)
	}

	return claims, nil
}

func (queue *OutboxQueue) MarkOutboxDelivered(ctx context.Context, eventID, claimToken string, deliveredAt time.Time) error {
	if eventID == "" || claimToken == "" || deliveredAt.IsZero() {
		return errors.New("event, claim and delivery time are required")
	}

	command, err := queue.pool.Exec(ctx, `
UPDATE outbox_events
SET delivered_at = $3,
    claimed_by = NULL,
    claimed_until = NULL,
    last_error = NULL
WHERE event_id = $1
  AND delivered_at IS NULL
  AND claimed_by = $2
  AND claimed_until > clock_timestamp()`, eventID, claimToken, deliveredAt)
	if err != nil {
		return fmt.Errorf("mark outbox event delivered: %w", err)
	}
	if command.RowsAffected() != 1 {
		return worker.ErrOutboxClaimLost
	}

	return nil
}

func (queue *OutboxQueue) ScheduleOutboxRetry(ctx context.Context, eventID, claimToken string, retryAt time.Time, lastError string) error {
	if eventID == "" || claimToken == "" || retryAt.IsZero() || lastError == "" {
		return errors.New("event, claim, retry time and delivery error are required")
	}
	if len([]rune(lastError)) > maxOutboxDeliveryError {
		return errors.New("outbox delivery error exceeds storage limit")
	}

	command, err := queue.pool.Exec(ctx, `
UPDATE outbox_events
SET available_at = $3,
    claimed_by = NULL,
    claimed_until = NULL,
    last_error = $4
WHERE event_id = $1
  AND delivered_at IS NULL
  AND claimed_by = $2
  AND claimed_until > clock_timestamp()`, eventID, claimToken, retryAt, lastError)
	if err != nil {
		return fmt.Errorf("schedule outbox retry: %w", err)
	}
	if command.RowsAffected() != 1 {
		return worker.ErrOutboxClaimLost
	}

	return nil
}

var _ worker.OutboxQueue = (*OutboxQueue)(nil)
