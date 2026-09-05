package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

const outboxResultRated = "result.rated"

// RatingQueue serializes verified results by availability time. A claimed head
// result blocks later results until it is processed or its retry becomes due.
type RatingQueue struct {
	pool *pgxpool.Pool
}

func NewRatingQueue(pool *pgxpool.Pool) (*RatingQueue, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}
	return &RatingQueue{pool: pool}, nil
}

func (queue *RatingQueue) ClaimNextRatingResult(ctx context.Context, request worker.RatingClaimRequest) (*worker.RatingClaim, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	tx, err := queue.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin rating claim: %w", err)
	}
	defer rollback(tx, ctx)()

	var eventID string
	var nextAttemptAt time.Time
	var claimedUntil *time.Time
	err = tx.QueryRow(ctx, `
SELECT event_id, next_attempt_at, claimed_until
FROM verified_results
WHERE processed_at IS NULL
ORDER BY available_at, event_id
LIMIT 1
FOR UPDATE`).Scan(&eventID, &nextAttemptAt, &claimedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit empty rating claim: %w", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock rating queue head: %w", err)
	}
	if nextAttemptAt.After(request.ClaimedAt) || (claimedUntil != nil && claimedUntil.After(request.ClaimedAt)) {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit blocked rating claim: %w", err)
		}
		return nil, nil
	}

	var claim worker.RatingClaim
	err = tx.QueryRow(ctx, `
UPDATE verified_results AS result
SET claim_token = $2,
    claimed_until = $3,
    attempt_count = result.attempt_count + 1
WHERE result.event_id = $1
RETURNING result.event_id, result.claim_token, result.attempt_count, result.claimed_until`,
		eventID, request.Token, request.LeaseUntil,
	).Scan(&claim.EventID, &claim.Token, &claim.Attempt, &claim.LeaseUntil)
	if err != nil {
		return nil, fmt.Errorf("claim rating result: %w", err)
	}
	if err := claim.Validate(); err != nil {
		return nil, fmt.Errorf("validate rating claim: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit rating claim: %w", err)
	}
	return &claim, nil
}

func (queue *RatingQueue) ProcessRatingResult(ctx context.Context, claim worker.RatingClaim, processedAt time.Time) (int, error) {
	if err := claim.Validate(); err != nil {
		return 0, err
	}
	if processedAt.IsZero() {
		return 0, errors.New("rating processing time is required")
	}
	tx, err := queue.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin rating processing: %w", err)
	}
	defer rollback(tx, ctx)()

	result, modelVersion, err := lockClaimedRatingResult(ctx, tx, claim)
	if err != nil {
		return 0, err
	}
	snapshots, err := loadRatingParticipants(ctx, tx, &result)
	if err != nil {
		return 0, err
	}
	model, err := rating.NewBaseline(rating.DefaultBaselineConfig(modelVersion))
	if err != nil {
		return 0, fmt.Errorf("construct rating model: %w", err)
	}
	estimates := make(map[string]rating.Estimate, len(result.Participants))
	for _, participant := range result.Participants {
		current, err := lockCurrentRating(ctx, tx, participant.PlayerID, result.ModeID, modelVersion)
		switch {
		case err == nil:
			estimates[participant.PlayerID] = current
		case errors.Is(err, pgx.ErrNoRows):
			estimates[participant.PlayerID] = snapshots[participant.PlayerID]
		default:
			return 0, fmt.Errorf("lock current rating for player %q: %w", participant.PlayerID, err)
		}
	}
	updates, err := model.Update(result, estimates, processedAt)
	if err != nil {
		return 0, fmt.Errorf("calculate rating update: %w", err)
	}
	for _, update := range updates {
		if update.After.Games > math.MaxInt64 {
			return 0, fmt.Errorf("rating games for player %q exceed PostgreSQL bigint", update.PlayerID)
		}
		if err := persistRatingUpdate(ctx, tx, result.ModeID, update); err != nil {
			return 0, err
		}
	}
	command, err := tx.Exec(ctx, `
UPDATE verified_results
SET processed_at = $3, next_attempt_at = NULL, claim_token = NULL, claimed_until = NULL
WHERE event_id = $1 AND claim_token = $2 AND processed_at IS NULL`, claim.EventID, claim.Token, processedAt)
	if err != nil {
		return 0, fmt.Errorf("mark rating result processed: %w", err)
	}
	if command.RowsAffected() != 1 {
		return 0, worker.ErrRatingClaimLost
	}
	payload, err := json.Marshal(struct {
		ResultEventID string                `json:"result_event_id"`
		ModeID        string                `json:"mode_id"`
		ModelVersion  string                `json:"model_version"`
		ProcessedAt   time.Time             `json:"processed_at"`
		Updates       []rating.RatingUpdate `json:"updates"`
	}{result.EventID, result.ModeID, modelVersion, processedAt, updates})
	if err != nil {
		return 0, fmt.Errorf("encode rated result event: %w", err)
	}
	if err := insertOutbox(ctx, tx, outboxEvent{
		ID: derivedEventID("result-rated", result.EventID, 1), AggregateType: "result",
		AggregateID: result.EventID, AggregateVersion: 1, Type: outboxResultRated,
		Payload: string(payload), OccurredAt: processedAt,
	}); err != nil {
		return 0, fmt.Errorf("insert rated result event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit rating processing: %w", err)
	}
	return len(updates), nil
}

func (queue *RatingQueue) ScheduleRatingRetry(ctx context.Context, eventID, claimToken string, retryAt time.Time) error {
	if eventID == "" || claimToken == "" || retryAt.IsZero() {
		return errors.New("result, claim and retry time are required")
	}
	command, err := queue.pool.Exec(ctx, `
UPDATE verified_results
SET next_attempt_at = $3, claim_token = NULL, claimed_until = NULL
WHERE event_id = $1
  AND processed_at IS NULL
  AND claim_token = $2
  AND claimed_until > clock_timestamp()`, eventID, claimToken, retryAt)
	if err != nil {
		return fmt.Errorf("schedule rating retry: %w", err)
	}
	if command.RowsAffected() != 1 {
		return worker.ErrRatingClaimLost
	}
	return nil
}

func lockClaimedRatingResult(ctx context.Context, tx pgx.Tx, claim worker.RatingClaim) (rating.MatchResult, string, error) {
	var result rating.MatchResult
	var modelVersion string
	err := tx.QueryRow(ctx, `
SELECT result.event_id, result.room_id, result.mode_id, result.deck_id,
       result.scoring_rules_version, result.finished_at, result.available_at,
       room.rating_model_version
FROM verified_results AS result
JOIN rooms AS room ON room.room_id = result.room_id
WHERE result.event_id = $1
  AND result.processed_at IS NULL
  AND result.claim_token = $2
  AND result.claimed_until > clock_timestamp()
FOR UPDATE OF result`, claim.EventID, claim.Token).Scan(
		&result.EventID, &result.RoomID, &result.ModeID, &result.DeckID,
		&result.ScoringRulesVersion, &result.FinishedAt, &result.AvailableAt, &modelVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return rating.MatchResult{}, "", worker.ErrRatingClaimLost
	}
	if err != nil {
		return rating.MatchResult{}, "", fmt.Errorf("lock claimed rating result: %w", err)
	}
	return result, modelVersion, nil
}

func loadRatingParticipants(ctx context.Context, tx pgx.Tx, result *rating.MatchResult) (map[string]rating.Estimate, error) {
	rows, err := tx.Query(ctx, `
SELECT participant.player_id, participant.place, participant.score,
       participant.elapsed_ms, participant.completed, participant.moves,
       participant.undo_moves, participant.revealed_cards,
       ticket.rating_mean, ticket.rating_uncertainty,
       ticket.rating_performance_deviation, ticket.rating_games,
       ticket.rating_model_version, ticket.rating_updated_at
FROM verified_result_participants AS participant
JOIN room_memberships AS membership
  ON membership.room_id = participant.room_id AND membership.player_id = participant.player_id
JOIN matchmaking_tickets AS ticket ON ticket.ticket_id = membership.ticket_id
WHERE participant.event_id = $1
ORDER BY participant.player_id`, result.EventID)
	if err != nil {
		return nil, fmt.Errorf("load rating participants: %w", err)
	}
	defer rows.Close()
	snapshots := make(map[string]rating.Estimate)
	for rows.Next() {
		var participant rating.ParticipantResult
		var snapshot rating.Estimate
		if err := rows.Scan(
			&participant.PlayerID, &participant.Place, &participant.Features.Score,
			&participant.Features.ElapsedMillis, &participant.Features.Completed, &participant.Features.Moves,
			&participant.Features.UndoMoves, &participant.Features.RevealedCards,
			&snapshot.Mean, &snapshot.Uncertainty, &snapshot.PerformanceDeviation, &snapshot.Games,
			&snapshot.ModelVersion, &snapshot.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan rating participant: %w", err)
		}
		result.Participants = append(result.Participants, participant)
		snapshots[participant.PlayerID] = snapshot
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read rating participants: %w", err)
	}
	return snapshots, nil
}

func lockCurrentRating(ctx context.Context, tx pgx.Tx, playerID, modeID, modelVersion string) (rating.Estimate, error) {
	var estimate rating.Estimate
	estimate.ModelVersion = modelVersion
	err := tx.QueryRow(ctx, `
SELECT mean, uncertainty, performance_deviation, games, updated_at
FROM player_ratings
WHERE player_id = $1 AND mode_id = $2 AND model_version = $3
FOR UPDATE`, playerID, modeID, modelVersion).Scan(
		&estimate.Mean, &estimate.Uncertainty, &estimate.PerformanceDeviation, &estimate.Games, &estimate.UpdatedAt,
	)
	return estimate, err
}

func persistRatingUpdate(ctx context.Context, tx pgx.Tx, modeID string, update rating.RatingUpdate) error {
	_, err := tx.Exec(ctx, `
INSERT INTO rating_updates (
    player_id, mode_id, source_event_id, model_version,
    before_mean, before_uncertainty, before_performance_deviation,
    before_games, before_updated_at, after_mean, after_uncertainty,
    after_performance_deviation, after_games, after_updated_at, processed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		update.PlayerID, modeID, update.SourceEventID, update.ModelVersion,
		update.Before.Mean, update.Before.Uncertainty, update.Before.PerformanceDeviation,
		update.Before.Games, update.Before.UpdatedAt, update.After.Mean, update.After.Uncertainty,
		update.After.PerformanceDeviation, update.After.Games, update.After.UpdatedAt, update.ProcessedAt,
	)
	if err != nil {
		return fmt.Errorf("insert rating history for player %q: %w", update.PlayerID, err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO player_ratings (
    player_id, mode_id, model_version, mean, uncertainty,
    performance_deviation, games, updated_at, revision
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1)
ON CONFLICT (player_id, mode_id, model_version) DO UPDATE
SET mean = EXCLUDED.mean,
    uncertainty = EXCLUDED.uncertainty,
    performance_deviation = EXCLUDED.performance_deviation,
    games = EXCLUDED.games,
    updated_at = EXCLUDED.updated_at,
    revision = player_ratings.revision + 1`,
		update.PlayerID, modeID, update.ModelVersion, update.After.Mean, update.After.Uncertainty,
		update.After.PerformanceDeviation, update.After.Games, update.After.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store current rating for player %q: %w", update.PlayerID, err)
	}
	return nil
}

var _ worker.RatingQueue = (*RatingQueue)(nil)
