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

// RatingShadowQueue owns an isolated, globally ordered timeline of room
// predictions and result observations. It never writes active player ratings.
type RatingShadowQueue struct {
	pool *pgxpool.Pool
}

func NewRatingShadowQueue(pool *pgxpool.Pool) (*RatingShadowQueue, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}
	return &RatingShadowQueue{pool: pool}, nil
}

func (queue *RatingShadowQueue) ClaimNextRatingShadowWork(ctx context.Context, request worker.RatingClaimRequest) (*worker.RatingShadowClaim, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	tx, err := queue.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin rating shadow claim: %w", err)
	}
	defer rollback(tx, ctx)()

	var kind, sourceID string
	var nextAttemptAt time.Time
	var claimedUntil *time.Time
	err = tx.QueryRow(ctx, `
SELECT work_kind, source_id, next_attempt_at, claimed_until
FROM rating_shadow_work
WHERE processed_at IS NULL
ORDER BY timeline_position, ordering_priority, source_id
LIMIT 1
FOR UPDATE`).Scan(&kind, &sourceID, &nextAttemptAt, &claimedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit empty rating shadow claim: %w", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock rating shadow queue head: %w", err)
	}
	if nextAttemptAt.After(request.ClaimedAt) || (claimedUntil != nil && claimedUntil.After(request.ClaimedAt)) {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit blocked rating shadow claim: %w", err)
		}
		return nil, nil
	}

	var claim worker.RatingShadowClaim
	err = tx.QueryRow(ctx, `
UPDATE rating_shadow_work AS work
SET claim_token = $3, claimed_until = $4, attempt_count = work.attempt_count + 1
WHERE work_kind = $1 AND source_id = $2
RETURNING work_kind, source_id, claim_token, attempt_count, claimed_until`,
		kind, sourceID, request.Token, request.LeaseUntil,
	).Scan(&claim.Kind, &claim.SourceID, &claim.Token, &claim.Attempt, &claim.LeaseUntil)
	if err != nil {
		return nil, fmt.Errorf("claim rating shadow work: %w", err)
	}
	if err := claim.Validate(); err != nil {
		return nil, fmt.Errorf("validate rating shadow claim: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit rating shadow claim: %w", err)
	}
	return &claim, nil
}

func (queue *RatingShadowQueue) ProcessRatingShadowWork(ctx context.Context, claim worker.RatingShadowClaim, processedAt time.Time) (int, error) {
	if err := claim.Validate(); err != nil {
		return 0, err
	}
	if processedAt.IsZero() {
		return 0, errors.New("rating shadow processing time is required")
	}
	tx, err := queue.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin rating shadow processing: %w", err)
	}
	defer rollback(tx, ctx)()
	if err := lockRatingShadowClaim(ctx, tx, claim); err != nil {
		return 0, err
	}

	var persisted int
	var skipReason string
	switch claim.Kind {
	case "room":
		persisted, skipReason, err = processShadowRoom(ctx, tx, claim.SourceID)
	case "result":
		persisted, skipReason, err = processShadowResult(ctx, tx, claim.SourceID, processedAt)
	default:
		err = errors.New("unsupported rating shadow work kind")
	}
	if err != nil {
		return 0, err
	}
	command, err := tx.Exec(ctx, `
UPDATE rating_shadow_work
SET processed_at = $4, skip_reason = NULLIF($5, ''),
    claim_token = NULL, claimed_until = NULL
WHERE work_kind = $1 AND source_id = $2 AND claim_token = $3 AND processed_at IS NULL`,
		claim.Kind, claim.SourceID, claim.Token, processedAt, skipReason)
	if err != nil {
		return 0, fmt.Errorf("mark rating shadow work processed: %w", err)
	}
	if command.RowsAffected() != 1 {
		return 0, worker.ErrRatingShadowClaimLost
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit rating shadow processing: %w", err)
	}
	return persisted, nil
}

func (queue *RatingShadowQueue) ScheduleRatingShadowRetry(ctx context.Context, claim worker.RatingShadowClaim, retryAt time.Time) error {
	if err := claim.Validate(); err != nil {
		return err
	}
	if retryAt.IsZero() {
		return errors.New("rating shadow retry time is required")
	}
	command, err := queue.pool.Exec(ctx, `
UPDATE rating_shadow_work
SET next_attempt_at = $4, claim_token = NULL, claimed_until = NULL
WHERE work_kind = $1 AND source_id = $2
  AND processed_at IS NULL AND claim_token = $3
  AND claimed_until > clock_timestamp()`, claim.Kind, claim.SourceID, claim.Token, retryAt)
	if err != nil {
		return fmt.Errorf("schedule rating shadow retry: %w", err)
	}
	if command.RowsAffected() != 1 {
		return worker.ErrRatingShadowClaimLost
	}
	return nil
}

func lockRatingShadowClaim(ctx context.Context, tx pgx.Tx, claim worker.RatingShadowClaim) error {
	var exists bool
	err := tx.QueryRow(ctx, `
SELECT true
FROM rating_shadow_work
WHERE work_kind = $1 AND source_id = $2 AND processed_at IS NULL
  AND claim_token = $3 AND claimed_until > clock_timestamp()
FOR UPDATE`, claim.Kind, claim.SourceID, claim.Token).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return worker.ErrRatingShadowClaimLost
	}
	if err != nil {
		return fmt.Errorf("lock claimed rating shadow work: %w", err)
	}
	return nil
}

type shadowDefinition struct {
	Candidate rating.ExtendedConfig `json:"candidate"`
}

type shadowDeployment struct {
	candidateVersion string
	baselineVersion  string
	trainingCutoff   time.Time
	trainedThrough   time.Time
	definition       shadowDefinition
}

func processShadowRoom(ctx context.Context, tx pgx.Tx, roomID string) (int, string, error) {
	var modeID, scoringRulesVersion, baselineVersion, deckVersion, currency string
	var filledAt time.Time
	var capacity int
	var entryFee int64
	err := tx.QueryRow(ctx, `
SELECT room.mode_id, room.scoring_rules_version, room.rating_model_version,
       COALESCE(room.deck_version, ''), room.filled_at, room.capacity,
       config.entry_fee_minor, config.currency
FROM rooms AS room
JOIN tournament_configs AS config
  ON config.tournament_id = room.tournament_id AND config.version = room.tournament_version
WHERE room.room_id = $1 AND room.filled_at IS NOT NULL`, roomID).Scan(
		&modeID, &scoringRulesVersion, &baselineVersion, &deckVersion,
		&filledAt, &capacity, &entryFee, &currency,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "room_unavailable", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("load rating shadow room: %w", err)
	}
	if deckVersion == "" {
		return 0, "deck_version_unavailable", nil
	}
	deployment, err := loadShadowDeployment(ctx, tx, modeID, scoringRulesVersion, deckVersion, filledAt, baselineVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "no_active_deployment", nil
	}
	if err != nil {
		return 0, "", err
	}
	model, err := rating.NewExtended(deployment.definition.Candidate)
	if err != nil {
		return 0, "", fmt.Errorf("construct extended rating model %q: %w", deployment.candidateVersion, err)
	}

	rows, err := tx.Query(ctx, `
SELECT membership.player_id, ticket.rating_mean, ticket.rating_uncertainty,
       ticket.rating_performance_deviation, ticket.rating_games,
       ticket.rating_model_version, ticket.rating_updated_at
FROM room_memberships AS membership
JOIN matchmaking_tickets AS ticket ON ticket.ticket_id = membership.ticket_id
WHERE membership.room_id = $1
ORDER BY membership.player_id`, roomID)
	if err != nil {
		return 0, "", fmt.Errorf("load rating shadow room participants: %w", err)
	}
	defer rows.Close()
	baselineEstimates := make(map[string]rating.Estimate, capacity)
	for rows.Next() {
		var playerID string
		var estimate rating.Estimate
		if err := rows.Scan(
			&playerID, &estimate.Mean, &estimate.Uncertainty,
			&estimate.PerformanceDeviation, &estimate.Games,
			&estimate.ModelVersion, &estimate.UpdatedAt,
		); err != nil {
			return 0, "", fmt.Errorf("scan rating shadow room participant: %w", err)
		}
		baselineEstimates[playerID] = estimate
	}
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("read rating shadow room participants: %w", err)
	}
	if len(baselineEstimates) != capacity {
		return 0, "", errors.New("rating shadow room is missing participants")
	}

	baselineModel, err := rating.NewBaseline(rating.DefaultBaselineConfig(baselineVersion))
	if err != nil {
		return 0, "", err
	}
	baselinePrediction, err := baselineModel.Predict(rating.PredictionRequest{
		RoomID: roomID, ModeID: modeID, GeneratedAt: filledAt, Estimates: baselineEstimates,
	})
	if err != nil {
		return 0, "", fmt.Errorf("predict rating shadow baseline: %w", err)
	}
	candidateEstimates, profiles, err := loadShadowStates(ctx, tx, model, deployment, modeID, baselineEstimates, filledAt)
	if err != nil {
		return 0, "", err
	}
	candidatePrediction, err := model.Predict(rating.PredictionRequest{
		RoomID: roomID, ModeID: modeID, GeneratedAt: filledAt, Estimates: candidateEstimates,
	}, profiles)
	if err != nil {
		return 0, "", fmt.Errorf("predict extended rating candidate: %w", err)
	}
	baselineJSON, err := json.Marshal(baselinePrediction)
	if err != nil {
		return 0, "", err
	}
	candidateJSON, err := json.Marshal(candidatePrediction)
	if err != nil {
		return 0, "", err
	}
	segmentID := fmt.Sprintf("%s|%s|%d|%s|%d", modeID, scoringRulesVersion, capacity, currency, entryFee)
	_, err = tx.Exec(ctx, `
INSERT INTO rating_shadow_predictions (
    room_id, candidate_version, baseline_version, segment_id, generated_at,
    baseline_prediction, candidate_prediction
) VALUES ($1, $2, $3, $4, $5, $6, $7)`, roomID, deployment.candidateVersion,
		baselineVersion, segmentID, filledAt, baselineJSON, candidateJSON)
	if err != nil {
		return 0, "", fmt.Errorf("persist rating shadow prediction: %w", err)
	}
	return 1, "", nil
}

func processShadowResult(ctx context.Context, tx pgx.Tx, eventID string, processedAt time.Time) (int, string, error) {
	result, deckVersion, err := loadShadowResult(ctx, tx, eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "result_unavailable", nil
	}
	if err != nil {
		return 0, "", err
	}
	if deckVersion == "" {
		return 0, "deck_version_unavailable", nil
	}
	var candidateVersion, baselineVersion, segmentID string
	var baselineJSON, candidateJSON []byte
	err = tx.QueryRow(ctx, `
SELECT candidate_version, baseline_version, segment_id,
       baseline_prediction, candidate_prediction
FROM rating_shadow_predictions
WHERE room_id = $1`, result.RoomID).Scan(
		&candidateVersion, &baselineVersion, &segmentID, &baselineJSON, &candidateJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "no_prediction", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("load rating shadow prediction: %w", err)
	}
	deployment, err := loadShadowDeploymentByVersion(ctx, tx, candidateVersion)
	if err != nil {
		return 0, "", err
	}
	model, err := rating.NewExtended(deployment.definition.Candidate)
	if err != nil {
		return 0, "", fmt.Errorf("construct extended rating model %q: %w", candidateVersion, err)
	}

	playerIDs := make(map[string]rating.Estimate, len(result.Participants))
	for _, participant := range result.Participants {
		playerIDs[participant.PlayerID] = rating.Estimate{}
	}
	estimates, profiles, err := loadShadowStates(ctx, tx, model, deployment, result.ModeID, playerIDs, result.AvailableAt)
	if err != nil {
		return 0, "", err
	}
	// Candidate state advances on the logical availability timeline. Wall-clock
	// worker delay is retained separately as evaluated_at and never changes what
	// a later historical room prediction is allowed to observe.
	updates, err := model.Update(result, estimates, result.AvailableAt)
	if err != nil {
		return 0, "", fmt.Errorf("update extended rating candidate: %w", err)
	}
	updatedProfiles, err := model.Observe(result, deckVersion, profiles)
	if err != nil {
		return 0, "", fmt.Errorf("observe extended rating features: %w", err)
	}
	for _, update := range updates {
		if update.After.Games > math.MaxInt64 {
			return 0, "", fmt.Errorf("rating shadow games for player %q exceed PostgreSQL bigint", update.PlayerID)
		}
		if err := persistShadowUpdate(ctx, tx, result.ModeID, update, updatedProfiles[update.PlayerID]); err != nil {
			return 0, "", err
		}
	}

	var baselinePrediction, candidatePrediction rating.RoomPrediction
	if err := json.Unmarshal(baselineJSON, &baselinePrediction); err != nil {
		return 0, "", fmt.Errorf("decode rating shadow baseline prediction: %w", err)
	}
	if err := json.Unmarshal(candidateJSON, &candidatePrediction); err != nil {
		return 0, "", fmt.Errorf("decode rating shadow candidate prediction: %w", err)
	}
	baselineReport, err := rating.EvaluateHoldoutCalibration([]rating.CalibrationObservation{{
		Prediction: baselinePrediction, Result: result,
	}}, rating.HoldoutCalibrationConfig{
		TrainingCutoff: deployment.trainingCutoff, ModelTrainedThrough: deployment.trainedThrough, BinCount: 10,
	})
	if err != nil {
		return 0, "", fmt.Errorf("score rating shadow baseline: %w", err)
	}
	candidateReport, err := rating.EvaluateHoldoutCalibration([]rating.CalibrationObservation{{
		Prediction: candidatePrediction, Result: result,
	}}, rating.HoldoutCalibrationConfig{
		TrainingCutoff: deployment.trainingCutoff, ModelTrainedThrough: deployment.trainedThrough, BinCount: 10,
	})
	if err != nil {
		return 0, "", fmt.Errorf("score rating shadow candidate: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO rating_shadow_observations (
    source_event_id, room_id, candidate_version, baseline_version, segment_id,
    result_available_at, baseline_brier, baseline_log_loss,
    candidate_brier, candidate_log_loss, evaluated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		result.EventID, result.RoomID, candidateVersion, baselineVersion, segmentID,
		result.AvailableAt, baselineReport.Calibration.MulticlassBrierScore,
		baselineReport.Calibration.MeanLogLoss, candidateReport.Calibration.MulticlassBrierScore,
		candidateReport.Calibration.MeanLogLoss, processedAt)
	if err != nil {
		return 0, "", fmt.Errorf("persist rating shadow observation: %w", err)
	}
	return len(updates) + 1, "", nil
}

func loadShadowDeployment(ctx context.Context, tx pgx.Tx, modeID, rulesVersion, deckVersion string, at time.Time, baselineVersion string) (shadowDeployment, error) {
	var deployment shadowDeployment
	var encoded []byte
	err := tx.QueryRow(ctx, `
SELECT candidate_version, baseline_version, training_cutoff, trained_through, definition
FROM rating_shadow_deployments
WHERE mode_id = $1 AND scoring_rules_version = $2 AND deck_version = $3
  AND baseline_version = $4 AND activated_at <= $5
  AND (ended_at IS NULL OR ended_at > $5)
ORDER BY activated_at DESC
LIMIT 1`, modeID, rulesVersion, deckVersion, baselineVersion, at).Scan(
		&deployment.candidateVersion, &deployment.baselineVersion,
		&deployment.trainingCutoff, &deployment.trainedThrough, &encoded,
	)
	if err != nil {
		return shadowDeployment{}, err
	}
	if err := json.Unmarshal(encoded, &deployment.definition); err != nil {
		return shadowDeployment{}, fmt.Errorf("decode rating shadow deployment %q: %w", deployment.candidateVersion, err)
	}
	if err := validateShadowDeployment(deployment, modeID, rulesVersion, deckVersion); err != nil {
		return shadowDeployment{}, err
	}
	return deployment, nil
}

func loadShadowDeploymentByVersion(ctx context.Context, tx pgx.Tx, candidateVersion string) (shadowDeployment, error) {
	var deployment shadowDeployment
	var modeID, rulesVersion, deckVersion string
	var encoded []byte
	err := tx.QueryRow(ctx, `
SELECT candidate_version, baseline_version, mode_id, scoring_rules_version,
       deck_version, training_cutoff, trained_through, definition
FROM rating_shadow_deployments
WHERE candidate_version = $1`, candidateVersion).Scan(
		&deployment.candidateVersion, &deployment.baselineVersion, &modeID, &rulesVersion,
		&deckVersion, &deployment.trainingCutoff, &deployment.trainedThrough, &encoded,
	)
	if err != nil {
		return shadowDeployment{}, fmt.Errorf("load rating shadow deployment %q: %w", candidateVersion, err)
	}
	if err := json.Unmarshal(encoded, &deployment.definition); err != nil {
		return shadowDeployment{}, fmt.Errorf("decode rating shadow deployment %q: %w", candidateVersion, err)
	}
	if err := validateShadowDeployment(deployment, modeID, rulesVersion, deckVersion); err != nil {
		return shadowDeployment{}, err
	}
	return deployment, nil
}

func validateShadowDeployment(deployment shadowDeployment, modeID, rulesVersion, deckVersion string) error {
	config := deployment.definition.Candidate
	if config.Baseline.Version != deployment.candidateVersion || !config.TrainedThrough.Equal(deployment.trainedThrough) ||
		config.FeatureSchema.ModeID != modeID || config.FeatureSchema.ScoringRulesVersion != rulesVersion ||
		config.FeatureSchema.DeckVersion != deckVersion {
		return fmt.Errorf("rating shadow deployment %q definition does not match its immutable metadata", deployment.candidateVersion)
	}
	return nil
}

func loadShadowStates(
	ctx context.Context,
	tx pgx.Tx,
	model *rating.Extended,
	deployment shadowDeployment,
	modeID string,
	players map[string]rating.Estimate,
	at time.Time,
) (map[string]rating.Estimate, map[string]rating.FeatureProfile, error) {
	estimates := make(map[string]rating.Estimate, len(players))
	profiles := make(map[string]rating.FeatureProfile, len(players))
	for playerID := range players {
		var estimate rating.Estimate
		estimate.ModelVersion = deployment.candidateVersion
		var encodedProfile []byte
		err := tx.QueryRow(ctx, `
SELECT mean, uncertainty, performance_deviation, games, updated_at, feature_profile
FROM rating_shadow_player_states
WHERE player_id = $1 AND mode_id = $2 AND candidate_version = $3
FOR UPDATE`, playerID, modeID, deployment.candidateVersion).Scan(
			&estimate.Mean, &estimate.Uncertainty, &estimate.PerformanceDeviation,
			&estimate.Games, &estimate.UpdatedAt, &encodedProfile,
		)
		switch {
		case err == nil:
			if estimate.UpdatedAt.After(at) {
				return nil, nil, fmt.Errorf("rating shadow state for player %q is newer than timeline position", playerID)
			}
			var profile rating.FeatureProfile
			if err := json.Unmarshal(encodedProfile, &profile); err != nil {
				return nil, nil, fmt.Errorf("decode rating shadow profile for player %q: %w", playerID, err)
			}
			estimates[playerID] = estimate
			profiles[playerID] = profile
		case errors.Is(err, pgx.ErrNoRows):
			initial, initialErr := model.InitialEstimate(deployment.trainedThrough)
			if initialErr != nil {
				return nil, nil, initialErr
			}
			estimates[playerID] = initial
		default:
			return nil, nil, fmt.Errorf("load rating shadow state for player %q: %w", playerID, err)
		}
	}
	return estimates, profiles, nil
}

func loadShadowResult(ctx context.Context, tx pgx.Tx, eventID string) (rating.MatchResult, string, error) {
	var result rating.MatchResult
	var deckVersion string
	err := tx.QueryRow(ctx, `
SELECT result.event_id, result.room_id, result.mode_id, result.deck_id,
       result.scoring_rules_version, result.finished_at, result.available_at,
       COALESCE(room.deck_version, '')
FROM verified_results AS result
JOIN rooms AS room ON room.room_id = result.room_id
WHERE result.event_id = $1`, eventID).Scan(
		&result.EventID, &result.RoomID, &result.ModeID, &result.DeckID,
		&result.ScoringRulesVersion, &result.FinishedAt, &result.AvailableAt, &deckVersion,
	)
	if err != nil {
		return rating.MatchResult{}, "", err
	}
	rows, err := tx.Query(ctx, `
SELECT player_id, place, score, elapsed_ms, completed, moves, undo_moves, revealed_cards
FROM verified_result_participants
WHERE event_id = $1
ORDER BY player_id`, eventID)
	if err != nil {
		return rating.MatchResult{}, "", fmt.Errorf("load rating shadow result participants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var participant rating.ParticipantResult
		if err := rows.Scan(
			&participant.PlayerID, &participant.Place, &participant.Features.Score,
			&participant.Features.ElapsedMillis, &participant.Features.Completed,
			&participant.Features.Moves, &participant.Features.UndoMoves,
			&participant.Features.RevealedCards,
		); err != nil {
			return rating.MatchResult{}, "", fmt.Errorf("scan rating shadow result participant: %w", err)
		}
		result.Participants = append(result.Participants, participant)
	}
	if err := rows.Err(); err != nil {
		return rating.MatchResult{}, "", fmt.Errorf("read rating shadow result participants: %w", err)
	}
	return result, deckVersion, nil
}

func persistShadowUpdate(ctx context.Context, tx pgx.Tx, modeID string, update rating.RatingUpdate, profile rating.FeatureProfile) error {
	beforeJSON, err := json.Marshal(update.Before)
	if err != nil {
		return err
	}
	afterJSON, err := json.Marshal(update.After)
	if err != nil {
		return err
	}
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO rating_shadow_updates (
    player_id, mode_id, source_event_id, candidate_version,
    before_estimate, after_estimate, feature_profile, processed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, update.PlayerID, modeID,
		update.SourceEventID, update.ModelVersion, beforeJSON, afterJSON, profileJSON, update.ProcessedAt)
	if err != nil {
		return fmt.Errorf("persist rating shadow update for player %q: %w", update.PlayerID, err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO rating_shadow_player_states (
    player_id, mode_id, candidate_version, mean, uncertainty,
    performance_deviation, games, updated_at, revision, feature_profile
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9)
ON CONFLICT (player_id, mode_id, candidate_version) DO UPDATE
SET mean = EXCLUDED.mean,
    uncertainty = EXCLUDED.uncertainty,
    performance_deviation = EXCLUDED.performance_deviation,
    games = EXCLUDED.games,
    updated_at = EXCLUDED.updated_at,
    revision = rating_shadow_player_states.revision + 1,
    feature_profile = EXCLUDED.feature_profile`, update.PlayerID, modeID, update.ModelVersion,
		update.After.Mean, update.After.Uncertainty, update.After.PerformanceDeviation,
		update.After.Games, update.After.UpdatedAt, profileJSON)
	if err != nil {
		return fmt.Errorf("store current rating shadow state for player %q: %w", update.PlayerID, err)
	}
	return nil
}

var _ worker.RatingShadowQueue = (*RatingShadowQueue)(nil)
