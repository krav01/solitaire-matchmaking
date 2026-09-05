package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

const (
	outboxRoomCompleted = "room.completed"
	outboxRoomExpired   = "room.expired"
)

// ResultStore atomically persists authoritative standings and room terminal
// transitions. Rating processing consumes the stored result separately.
type ResultStore struct {
	pool *pgxpool.Pool
}

func NewResultStore(pool *pgxpool.Pool) (*ResultStore, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}
	return &ResultStore{pool: pool}, nil
}

func (store *ResultStore) FinalizeResult(ctx context.Context, command tournament.FinalizeResultCommand) (tournament.FinalizedResult, error) {
	digest, err := command.RequestDigest()
	if err != nil {
		return tournament.FinalizedResult{}, err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return tournament.FinalizedResult{}, fmt.Errorf("begin result finalization: %w", err)
	}
	defer rollback(tx, ctx)()

	stored, storedDigest, err := loadFinalizedResult(ctx, tx, "event", command.EventID)
	if err == nil {
		return commitResultReplay(ctx, tx, stored, storedDigest, digest)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return tournament.FinalizedResult{}, fmt.Errorf("read result identity: %w", err)
	}

	room, err := lockResultRoom(ctx, tx, command.RoomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tournament.FinalizedResult{}, tournament.ErrResultRoomNotFound
	}
	if err != nil {
		return tournament.FinalizedResult{}, fmt.Errorf("lock result room: %w", err)
	}
	if room.status != tournament.RoomCollecting {
		stored, storedDigest, loadErr := loadFinalizedResult(ctx, tx, "room", command.RoomID)
		if loadErr == nil {
			return commitResultReplay(ctx, tx, stored, storedDigest, digest)
		}
		if !errors.Is(loadErr, pgx.ErrNoRows) {
			return tournament.FinalizedResult{}, fmt.Errorf("read room result: %w", loadErr)
		}
		return tournament.FinalizedResult{}, tournament.ErrResultRoomNotCollecting
	}
	if room.modeID != command.ModeID || room.deckID != command.DeckID || room.scoringRulesVersion != command.ScoringRulesVersion {
		return tournament.FinalizedResult{}, tournament.ErrResultConflict
	}
	if room.resultDeadline == nil || command.AcceptedAt.After(*room.resultDeadline) || command.FinishedAt.After(*room.resultDeadline) {
		return tournament.FinalizedResult{}, tournament.ErrResultDeadlinePassed
	}

	sessions, err := lockRoomSessions(ctx, tx, room.id)
	if err != nil {
		return tournament.FinalizedResult{}, err
	}
	if err := verifyResultParticipants(command.Participants, sessions, room.capacity, command.FinishedAt); err != nil {
		return tournament.FinalizedResult{}, err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO verified_results (
    event_id, request_digest, room_id, room_capacity, mode_id, deck_id,
    scoring_rules_version, finished_at, available_at, accepted_at, next_attempt_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $9)`,
		command.EventID, digest, command.RoomID, room.capacity, command.ModeID, command.DeckID,
		command.ScoringRulesVersion, command.FinishedAt, command.AvailableAt, command.AcceptedAt,
	); err != nil {
		return tournament.FinalizedResult{}, mapConflict(err, tournament.ErrResultConflict, "insert verified result")
	}
	for _, participant := range command.Participants {
		features := participant.Features
		if _, err := tx.Exec(ctx, `
INSERT INTO verified_result_participants (
    event_id, room_id, room_capacity, session_id, player_id, place,
    score, elapsed_ms, completed, moves, undo_moves, revealed_cards
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			command.EventID, command.RoomID, room.capacity, participant.SessionID, participant.PlayerID,
			participant.Place, features.Score, features.ElapsedMillis, features.Completed,
			features.Moves, features.UndoMoves, features.RevealedCards,
		); err != nil {
			return tournament.FinalizedResult{}, mapConflict(err, tournament.ErrResultParticipantsMismatch, "insert result participant")
		}
		if err := finalizeSession(ctx, tx, participant, command.FinishedAt); err != nil {
			return tournament.FinalizedResult{}, err
		}
	}

	room.version++
	if _, err := tx.Exec(ctx, `
UPDATE rooms
SET status = 'completed', aggregate_version = $2, completed_at = $3
WHERE room_id = $1`, room.id, room.version, command.AcceptedAt); err != nil {
		return tournament.FinalizedResult{}, fmt.Errorf("complete result room: %w", err)
	}
	result := tournament.FinalizedResult{
		Result: command.MatchResult(), RoomVersion: room.version,
		CompletedAt: command.AcceptedAt, RatingPending: true,
	}
	if err := insertRoomCompletedEvent(ctx, tx, result); err != nil {
		return tournament.FinalizedResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return tournament.FinalizedResult{}, mapConflict(err, tournament.ErrResultConflict, "commit result finalization")
	}
	return result, nil
}

func (store *ResultStore) ExpireResultRooms(ctx context.Context, batch tournament.ResultDeadlineBatch) ([]tournament.ExpiredRoom, error) {
	if err := batch.Validate(); err != nil {
		return nil, err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin result deadline expiry: %w", err)
	}
	defer rollback(tx, ctx)()

	rows, err := tx.Query(ctx, `
WITH due AS (
    SELECT room_id
    FROM rooms
    WHERE status = 'collecting' AND result_deadline <= $1
    ORDER BY result_deadline, room_id
    FOR UPDATE SKIP LOCKED
    LIMIT $2
)
UPDATE rooms AS room
SET status = 'expired', aggregate_version = room.aggregate_version + 1, expired_at = $1
FROM due
WHERE room.room_id = due.room_id
RETURNING room.room_id, room.aggregate_version, room.result_deadline`, batch.ExpiredAt, batch.Limit)
	if err != nil {
		return nil, fmt.Errorf("expire overdue result rooms: %w", err)
	}
	expired := make([]tournament.ExpiredRoom, 0, batch.Limit)
	for rows.Next() {
		var room tournament.ExpiredRoom
		if err := rows.Scan(&room.RoomID, &room.RoomVersion, &room.ResultDeadline); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan expired result room: %w", err)
		}
		room.ExpiredAt = batch.ExpiredAt
		expired = append(expired, room)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read expired result rooms: %w", err)
	}
	rows.Close()

	for _, room := range expired {
		if _, err := tx.Exec(ctx, `
UPDATE sessions
SET status = 'forfeited', forfeited_at = $2
WHERE room_id = $1 AND status IN ('allocated', 'playing')`, room.RoomID, batch.ExpiredAt); err != nil {
			return nil, fmt.Errorf("forfeit sessions for expired room %q: %w", room.RoomID, err)
		}
		payload, err := json.Marshal(room)
		if err != nil {
			return nil, fmt.Errorf("encode expired room event: %w", err)
		}
		if err := insertOutbox(ctx, tx, outboxEvent{
			ID:            derivedEventID("room-expired", room.RoomID, room.RoomVersion),
			AggregateType: outboxAggregateRoom, AggregateID: room.RoomID,
			AggregateVersion: room.RoomVersion, Type: outboxRoomExpired,
			Payload: string(payload), OccurredAt: room.ExpiredAt,
		}); err != nil {
			return nil, mapConflict(err, tournament.ErrResultConflict, "insert expired room event")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit result deadline expiry: %w", err)
	}
	return expired, nil
}

type resultRoom struct {
	id                  string
	modeID              string
	deckID              string
	scoringRulesVersion string
	status              tournament.RoomStatus
	capacity            int
	version             int64
	resultDeadline      *time.Time
}

func lockResultRoom(ctx context.Context, tx pgx.Tx, roomID string) (resultRoom, error) {
	var room resultRoom
	var status string
	err := tx.QueryRow(ctx, `
SELECT room_id, mode_id, deck_id, scoring_rules_version, status,
       capacity, aggregate_version, result_deadline
FROM rooms
WHERE room_id = $1
FOR UPDATE`, roomID).Scan(
		&room.id, &room.modeID, &room.deckID, &room.scoringRulesVersion, &status,
		&room.capacity, &room.version, &room.resultDeadline,
	)
	room.status = tournament.RoomStatus(status)
	return room, err
}

type resultSession struct {
	id          string
	playerID    string
	allocatedAt time.Time
}

func lockRoomSessions(ctx context.Context, tx pgx.Tx, roomID string) ([]resultSession, error) {
	rows, err := tx.Query(ctx, `
SELECT session_id, player_id, allocated_at
FROM sessions
WHERE room_id = $1
ORDER BY player_id
FOR UPDATE`, roomID)
	if err != nil {
		return nil, fmt.Errorf("lock result room sessions: %w", err)
	}
	defer rows.Close()
	var sessions []resultSession
	for rows.Next() {
		var session resultSession
		if err := rows.Scan(&session.id, &session.playerID, &session.allocatedAt); err != nil {
			return nil, fmt.Errorf("scan result room session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read result room sessions: %w", err)
	}
	return sessions, nil
}

func verifyResultParticipants(participants []tournament.VerifiedParticipant, sessions []resultSession, capacity int, finishedAt time.Time) error {
	if len(participants) != capacity || len(sessions) != capacity {
		return tournament.ErrResultParticipantsMismatch
	}
	byPlayer := make(map[string]resultSession, len(sessions))
	for _, session := range sessions {
		byPlayer[session.playerID] = session
	}
	for _, participant := range participants {
		session, exists := byPlayer[participant.PlayerID]
		if !exists || session.id != participant.SessionID || finishedAt.Before(session.allocatedAt) {
			return tournament.ErrResultParticipantsMismatch
		}
	}
	return nil
}

func finalizeSession(ctx context.Context, tx pgx.Tx, participant tournament.VerifiedParticipant, finishedAt time.Time) error {
	completed := participant.Features.Completed == nil || *participant.Features.Completed
	if completed {
		if _, err := tx.Exec(ctx, `
UPDATE sessions
SET status = 'submitted', started_at = COALESCE(started_at, allocated_at),
    submitted_at = $2, forfeited_at = NULL
WHERE session_id = $1`, participant.SessionID, finishedAt); err != nil {
			return fmt.Errorf("submit result session %q: %w", participant.SessionID, err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `
UPDATE sessions
SET status = 'forfeited', submitted_at = NULL, forfeited_at = $2
WHERE session_id = $1`, participant.SessionID, finishedAt); err != nil {
		return fmt.Errorf("forfeit result session %q: %w", participant.SessionID, err)
	}
	return nil
}

func insertRoomCompletedEvent(ctx context.Context, tx pgx.Tx, result tournament.FinalizedResult) error {
	payload, err := json.Marshal(struct {
		ResultEventID string                     `json:"result_event_id"`
		RoomID        string                     `json:"room_id"`
		CompletedAt   time.Time                  `json:"completed_at"`
		RatingPending bool                       `json:"rating_pending"`
		Standings     []rating.ParticipantResult `json:"standings"`
	}{
		ResultEventID: result.Result.EventID, RoomID: result.Result.RoomID,
		CompletedAt: result.CompletedAt, RatingPending: result.RatingPending,
		Standings: result.Result.Participants,
	})
	if err != nil {
		return fmt.Errorf("encode room completed event: %w", err)
	}
	if err := insertOutbox(ctx, tx, outboxEvent{
		ID:            derivedEventID("room-completed", result.Result.EventID, result.RoomVersion),
		AggregateType: outboxAggregateRoom, AggregateID: result.Result.RoomID,
		AggregateVersion: result.RoomVersion, Type: outboxRoomCompleted,
		Payload: string(payload), OccurredAt: result.CompletedAt,
	}); err != nil {
		return mapConflict(err, tournament.ErrResultConflict, "insert room completed event")
	}
	return nil
}

func loadFinalizedResult(ctx context.Context, tx pgx.Tx, lookup, identity string) (tournament.FinalizedResult, string, error) {
	if lookup != "event" && lookup != "room" {
		return tournament.FinalizedResult{}, "", errors.New("unsupported result lookup")
	}
	const query = `
SELECT result.event_id, result.request_digest, result.room_id, result.mode_id,
       result.deck_id, result.scoring_rules_version, result.finished_at,
       result.available_at, room.aggregate_version, room.completed_at,
       result.processed_at IS NULL
FROM verified_results AS result
JOIN rooms AS room ON room.room_id = result.room_id
WHERE ($1 = 'event' AND result.event_id = $2)
   OR ($1 = 'room' AND result.room_id = $2)`
	var result tournament.FinalizedResult
	var digest string
	err := tx.QueryRow(ctx, query, lookup, identity).Scan(
		&result.Result.EventID, &digest, &result.Result.RoomID, &result.Result.ModeID,
		&result.Result.DeckID, &result.Result.ScoringRulesVersion,
		&result.Result.FinishedAt, &result.Result.AvailableAt,
		&result.RoomVersion, &result.CompletedAt, &result.RatingPending,
	)
	if err != nil {
		return tournament.FinalizedResult{}, "", err
	}
	rows, err := tx.Query(ctx, `
SELECT session_id, player_id, place, score, elapsed_ms, completed,
       moves, undo_moves, revealed_cards
FROM verified_result_participants
WHERE event_id = $1
ORDER BY player_id`, result.Result.EventID)
	if err != nil {
		return tournament.FinalizedResult{}, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var participant tournament.VerifiedParticipant
		if err := rows.Scan(
			&participant.SessionID, &participant.PlayerID, &participant.Place,
			&participant.Features.Score, &participant.Features.ElapsedMillis,
			&participant.Features.Completed, &participant.Features.Moves,
			&participant.Features.UndoMoves, &participant.Features.RevealedCards,
		); err != nil {
			return tournament.FinalizedResult{}, "", err
		}
		result.Result.Participants = append(result.Result.Participants, rating.ParticipantResult{
			PlayerID: participant.PlayerID, Place: participant.Place, Features: participant.Features,
		})
	}
	return result, digest, rows.Err()
}

func commitResultReplay(ctx context.Context, tx pgx.Tx, result tournament.FinalizedResult, storedDigest, requestDigest string) (tournament.FinalizedResult, error) {
	if storedDigest != requestDigest {
		return tournament.FinalizedResult{}, tournament.ErrResultConflict
	}
	result.Replay = true
	if err := tx.Commit(ctx); err != nil {
		return tournament.FinalizedResult{}, fmt.Errorf("commit result replay: %w", err)
	}
	return result, nil
}

func derivedEventID(kind, identity string, version int64) string {
	payload := fmt.Sprintf("%s\x00%s\x00%d", kind, identity, version)
	digest := sha256.Sum256([]byte(payload))
	return kind + "-" + hex.EncodeToString(digest[:16])
}

var _ tournament.ResultRepository = (*ResultStore)(nil)
