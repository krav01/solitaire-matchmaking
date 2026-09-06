package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

// RatingShadowComparisonRequest keeps production policy thresholds explicit.
// The deployment supplies its immutable training boundary and model versions.
type RatingShadowComparisonRequest struct {
	CandidateVersion string
	BinCount         int
	Policy           rating.ModelComparisonPolicy
}

// RatingShadowReportStore reconstructs paired holdout evidence without reading
// or mutating the active player-rating tables.
type RatingShadowReportStore struct {
	pool *pgxpool.Pool
}

func NewRatingShadowReportStore(pool *pgxpool.Pool) (*RatingShadowReportStore, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}
	return &RatingShadowReportStore{pool: pool}, nil
}

func (store *RatingShadowReportStore) BuildComparison(ctx context.Context, request RatingShadowComparisonRequest) (rating.ModelComparisonReport, error) {
	if request.CandidateVersion == "" {
		return rating.ModelComparisonReport{}, errors.New("rating shadow candidate version is required")
	}
	var trainingCutoff, baselineTrainedThrough, candidateTrainedThrough time.Time
	err := store.pool.QueryRow(ctx, `
SELECT training_cutoff, training_cutoff, trained_through
FROM rating_shadow_deployments
WHERE candidate_version = $1`, request.CandidateVersion).Scan(
		&trainingCutoff, &baselineTrainedThrough, &candidateTrainedThrough,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return rating.ModelComparisonReport{}, errors.New("rating shadow deployment not found")
	}
	if err != nil {
		return rating.ModelComparisonReport{}, fmt.Errorf("load rating shadow comparison boundary: %w", err)
	}

	rows, err := store.pool.Query(ctx, `
SELECT prediction.segment_id, prediction.baseline_prediction,
       prediction.candidate_prediction, result.event_id, result.room_id,
       result.mode_id, result.deck_id, result.scoring_rules_version,
       result.finished_at, result.available_at,
       participant.player_id, participant.place, participant.score,
       participant.elapsed_ms, participant.completed, participant.moves,
       participant.undo_moves, participant.revealed_cards
FROM rating_shadow_predictions AS prediction
JOIN rating_shadow_observations AS observation
  ON observation.room_id = prediction.room_id
 AND observation.candidate_version = prediction.candidate_version
JOIN verified_results AS result ON result.event_id = observation.source_event_id
JOIN verified_result_participants AS participant ON participant.event_id = result.event_id
WHERE prediction.candidate_version = $1
ORDER BY result.available_at, result.event_id, participant.player_id`, request.CandidateVersion)
	if err != nil {
		return rating.ModelComparisonReport{}, fmt.Errorf("load rating shadow comparison observations: %w", err)
	}
	defer rows.Close()

	type pairedResult struct {
		segmentID string
		baseline  rating.RoomPrediction
		candidate rating.RoomPrediction
		result    rating.MatchResult
	}
	var paired []pairedResult
	for rows.Next() {
		var segmentID string
		var baselineJSON, candidateJSON []byte
		var eventID, roomID, modeID, deckID, rulesVersion, playerID string
		var finishedAt, availableAt time.Time
		var participant rating.ParticipantResult
		if err := rows.Scan(
			&segmentID, &baselineJSON, &candidateJSON, &eventID, &roomID,
			&modeID, &deckID, &rulesVersion, &finishedAt, &availableAt,
			&playerID, &participant.Place, &participant.Features.Score,
			&participant.Features.ElapsedMillis, &participant.Features.Completed,
			&participant.Features.Moves, &participant.Features.UndoMoves,
			&participant.Features.RevealedCards,
		); err != nil {
			return rating.ModelComparisonReport{}, fmt.Errorf("scan rating shadow comparison observation: %w", err)
		}
		participant.PlayerID = playerID
		if len(paired) == 0 || paired[len(paired)-1].result.EventID != eventID {
			var baselinePrediction, candidatePrediction rating.RoomPrediction
			if err := json.Unmarshal(baselineJSON, &baselinePrediction); err != nil {
				return rating.ModelComparisonReport{}, fmt.Errorf("decode rating shadow baseline prediction: %w", err)
			}
			if err := json.Unmarshal(candidateJSON, &candidatePrediction); err != nil {
				return rating.ModelComparisonReport{}, fmt.Errorf("decode rating shadow candidate prediction: %w", err)
			}
			paired = append(paired, pairedResult{
				segmentID: segmentID, baseline: baselinePrediction, candidate: candidatePrediction,
				result: rating.MatchResult{
					EventID: eventID, RoomID: roomID, ModeID: modeID, DeckID: deckID,
					ScoringRulesVersion: rulesVersion, FinishedAt: finishedAt, AvailableAt: availableAt,
				},
			})
		}
		paired[len(paired)-1].result.Participants = append(paired[len(paired)-1].result.Participants, participant)
	}
	if err := rows.Err(); err != nil {
		return rating.ModelComparisonReport{}, fmt.Errorf("read rating shadow comparison observations: %w", err)
	}

	observations := make([]rating.PairedCalibrationObservation, len(paired))
	for index, item := range paired {
		observations[index] = rating.PairedCalibrationObservation{
			SegmentID: item.segmentID, BaselinePrediction: item.baseline,
			CandidatePrediction: item.candidate, Result: item.result,
		}
	}
	report, err := rating.CompareHoldoutModels(observations, rating.ModelComparisonConfig{
		TrainingCutoff: trainingCutoff, BaselineTrainedThrough: baselineTrainedThrough,
		CandidateTrainedThrough: candidateTrainedThrough, BinCount: request.BinCount,
		Policy: request.Policy,
	})
	if err != nil {
		return rating.ModelComparisonReport{}, fmt.Errorf("build rating shadow comparison: %w", err)
	}
	return report, nil
}
