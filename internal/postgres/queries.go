package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

// QueryStore serves read-only integration and reconciliation queries.
type QueryStore struct {
	pool *pgxpool.Pool
}

func NewQueryStore(pool *pgxpool.Pool) (*QueryStore, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}

	return &QueryStore{pool: pool}, nil
}

func (store *QueryStore) GetRoom(ctx context.Context, roomID string) (tournament.RoomState, error) {
	if roomID == "" {
		return tournament.RoomState{}, errors.New("room identity is required")
	}

	var state tournament.RoomState
	rows, err := store.pool.Query(ctx, `
SELECT room.room_id, room.tournament_id, room.tournament_version, room.mode_id,
       room.policy_version, room.rating_model_version, room.scoring_rules_version,
       room.settlement_version, room.deck_id, room.capacity, room.status,
       room.aggregate_version, room.created_at, room.fill_deadline, room.filled_at,
       room.result_deadline, room.completed_at, room.expired_at, room.cancelled_at,
       membership.ticket_id, membership.player_id, session.session_id,
       membership.seat, session.status, membership.assigned_at,
       session.started_at, session.submitted_at, session.forfeited_at
FROM rooms AS room
LEFT JOIN room_memberships AS membership ON membership.room_id = room.room_id
LEFT JOIN sessions AS session ON session.ticket_id = membership.ticket_id
WHERE room.room_id = $1
ORDER BY membership.seat`, roomID)
	if err != nil {
		return tournament.RoomState{}, fmt.Errorf("read room: %w", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var roomStatus string
		var ticketID, playerID, sessionID, sessionStatus *string
		var seat *int16
		var assignedAt, startedAt, submittedAt, forfeitedAt *time.Time
		if err := rows.Scan(
			&state.RoomID, &state.TournamentID, &state.TournamentVersion, &state.ModeID,
			&state.PolicyVersion, &state.RatingModelVersion, &state.ScoringRulesVersion,
			&state.SettlementVersion, &state.DeckID, &state.Capacity, &roomStatus,
			&state.AggregateVersion, &state.CreatedAt, &state.FillDeadline, &state.FilledAt,
			&state.ResultDeadline, &state.CompletedAt, &state.ExpiredAt, &state.CancelledAt,
			&ticketID, &playerID, &sessionID, &seat, &sessionStatus, &assignedAt,
			&startedAt, &submittedAt, &forfeitedAt,
		); err != nil {
			return tournament.RoomState{}, fmt.Errorf("scan room: %w", err)
		}
		if !found {
			found = true
			state.Status = tournament.RoomStatus(roomStatus)
			state.Members = make([]tournament.RoomMember, 0, state.Capacity)
		}
		if ticketID == nil {
			continue
		}
		if playerID == nil || sessionID == nil || seat == nil || sessionStatus == nil || assignedAt == nil {
			return tournament.RoomState{}, errors.New("room membership is missing its session")
		}
		state.Members = append(state.Members, tournament.RoomMember{
			TicketID: *ticketID, PlayerID: *playerID, SessionID: *sessionID,
			Seat: int(*seat), Status: tournament.SessionStatus(*sessionStatus),
			AssignedAt: *assignedAt, StartedAt: startedAt,
			SubmittedAt: submittedAt, ForfeitedAt: forfeitedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return tournament.RoomState{}, fmt.Errorf("read room: %w", err)
	}
	if !found {
		return tournament.RoomState{}, tournament.ErrRoomNotFound
	}

	return state, nil
}

func (store *QueryStore) GetRating(ctx context.Context, playerID, modeID string) (tournament.PlayerRating, error) {
	if playerID == "" || modeID == "" {
		return tournament.PlayerRating{}, errors.New("player and mode identities are required")
	}

	current := tournament.PlayerRating{PlayerID: playerID, ModeID: modeID}
	err := store.pool.QueryRow(ctx, `
SELECT model_version, mean, uncertainty, performance_deviation,
       games, updated_at, revision
FROM player_ratings
WHERE player_id = $1 AND mode_id = $2
ORDER BY updated_at DESC, revision DESC, model_version DESC
LIMIT 1`, playerID, modeID).Scan(
		&current.Estimate.ModelVersion, &current.Estimate.Mean,
		&current.Estimate.Uncertainty, &current.Estimate.PerformanceDeviation,
		&current.Estimate.Games, &current.Estimate.UpdatedAt, &current.Revision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return tournament.PlayerRating{}, tournament.ErrRatingNotFound
	}
	if err != nil {
		return tournament.PlayerRating{}, fmt.Errorf("read player rating: %w", err)
	}

	return current, nil
}
