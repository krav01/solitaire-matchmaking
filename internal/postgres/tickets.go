package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

const (
	outboxAggregateTicket = "ticket"
	outboxAggregateRoom   = "room"
	outboxTicketAccepted  = "ticket.accepted"
	outboxTicketCancelled = "ticket.cancelled"
	outboxTicketAssigned  = "ticket.assigned"
	outboxTicketExpired   = "ticket.expired"
	outboxRoomFilled      = "room.filled"
)

// TicketStore persists ticket lifecycle changes and their outbox events in the
// same PostgreSQL transaction.
type TicketStore struct {
	pool *pgxpool.Pool
}

func NewTicketStore(pool *pgxpool.Pool) (*TicketStore, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}
	return &TicketStore{pool: pool}, nil
}

func (store *TicketStore) AcceptTicket(ctx context.Context, command tournament.AcceptTicketCommand) (tournament.TicketMutation, error) {
	digest, err := command.RequestDigest()
	if err != nil {
		return tournament.TicketMutation{}, err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("begin ticket acceptance: %w", err)
	}
	defer rollback(tx, ctx)()

	ticket := command.Ticket
	ticket.AggregateVersion = 1
	result, err := tx.Exec(ctx, `
INSERT INTO matchmaking_tickets (
    ticket_id, entry_id, request_digest, player_id, tournament_id,
    tournament_version, status, requested_at, snapshot_at, rating_mean,
    rating_uncertainty, rating_performance_deviation, rating_games,
    rating_model_version, rating_updated_at, aggregate_version, next_attempt_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $8
)
ON CONFLICT DO NOTHING`,
		ticket.ID, ticket.EntryID, digest, ticket.PlayerID, ticket.TournamentID,
		ticket.TournamentVersion, string(ticket.Status), ticket.RequestedAt, ticket.SnapshotAt,
		ticket.RatingSnapshot.Mean, ticket.RatingSnapshot.Uncertainty,
		ticket.RatingSnapshot.PerformanceDeviation, ticket.RatingSnapshot.Games,
		ticket.RatingSnapshot.ModelVersion, ticket.RatingSnapshot.UpdatedAt, ticket.AggregateVersion,
	)
	if err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("insert ticket: %w", err)
	}
	if result.RowsAffected() == 0 {
		stored, err := loadTicketByEntry(ctx, tx, ticket.EntryID, false)
		if errors.Is(err, pgx.ErrNoRows) {
			return tournament.TicketMutation{}, tournament.ErrIdempotencyConflict
		}
		if err != nil {
			return tournament.TicketMutation{}, fmt.Errorf("load accepted ticket: %w", err)
		}
		if stored.requestDigest != digest {
			return tournament.TicketMutation{}, tournament.ErrIdempotencyConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return tournament.TicketMutation{}, fmt.Errorf("commit ticket replay: %w", err)
		}
		return tournament.TicketMutation{Ticket: stored.ticket, Replay: true}, nil
	}

	payload, err := json.Marshal(struct {
		TicketID          string                  `json:"ticket_id"`
		EntryID           string                  `json:"entry_id"`
		PlayerID          string                  `json:"player_id"`
		TournamentID      string                  `json:"tournament_id"`
		TournamentVersion string                  `json:"tournament_version"`
		Status            tournament.TicketStatus `json:"status"`
		RequestedAt       time.Time               `json:"requested_at"`
	}{
		TicketID: ticket.ID, EntryID: ticket.EntryID, PlayerID: ticket.PlayerID,
		TournamentID: ticket.TournamentID, TournamentVersion: ticket.TournamentVersion,
		Status: ticket.Status, RequestedAt: ticket.RequestedAt,
	})
	if err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("encode ticket acceptance event: %w", err)
	}
	if err := insertOutbox(ctx, tx, outboxEvent{
		ID: command.EventID, AggregateType: outboxAggregateTicket, AggregateID: ticket.ID,
		AggregateVersion: ticket.AggregateVersion, Type: outboxTicketAccepted,
		Payload: string(payload), OccurredAt: ticket.RequestedAt,
	}); err != nil {
		return tournament.TicketMutation{}, mapConflict(err, tournament.ErrIdempotencyConflict, "insert ticket acceptance event")
	}
	if err := tx.Commit(ctx); err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("commit ticket acceptance: %w", err)
	}
	return tournament.TicketMutation{Ticket: ticket, Changed: true}, nil
}

func (store *TicketStore) CancelTicket(ctx context.Context, command tournament.CancelTicketCommand) (tournament.TicketMutation, error) {
	digest, err := command.RequestDigest()
	if err != nil {
		return tournament.TicketMutation{}, err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("begin ticket cancellation: %w", err)
	}
	defer rollback(tx, ctx)()

	stored, err := loadTicketByID(ctx, tx, command.TicketID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return tournament.TicketMutation{}, tournament.ErrTicketNotFound
	}
	if err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("lock ticket for cancellation: %w", err)
	}

	var storedDigest string
	err = tx.QueryRow(ctx, `
SELECT request_digest
FROM ticket_commands
WHERE ticket_id = $1 AND command_id = $2`, command.TicketID, command.CommandID).Scan(&storedDigest)
	if err == nil {
		if storedDigest != digest {
			return tournament.TicketMutation{}, tournament.ErrIdempotencyConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return tournament.TicketMutation{}, fmt.Errorf("commit cancellation replay: %w", err)
		}
		return tournament.TicketMutation{Ticket: stored.ticket, Replay: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return tournament.TicketMutation{}, fmt.Errorf("read cancellation command: %w", err)
	}

	changed := false
	switch stored.ticket.Status {
	case tournament.TicketQueued:
		stored.ticket.Status = tournament.TicketCancelled
		stored.ticket.CancelledAt = timePointer(command.CancelledAt)
		stored.ticket.AggregateVersion++
		if _, err := tx.Exec(ctx, `
UPDATE matchmaking_tickets
SET status = 'cancelled', cancelled_at = $2, aggregate_version = $3,
    next_attempt_at = NULL, claim_token = NULL, claimed_until = NULL
WHERE ticket_id = $1`, command.TicketID, command.CancelledAt, stored.ticket.AggregateVersion); err != nil {
			return tournament.TicketMutation{}, fmt.Errorf("cancel ticket: %w", err)
		}
		changed = true
	case tournament.TicketCancelled:
		// A new cancellation command observes the already terminal state.
	case tournament.TicketAssigned, tournament.TicketExpired:
		return tournament.TicketMutation{}, tournament.ErrTicketNotQueued
	default:
		return tournament.TicketMutation{}, errors.New("ticket has an unknown status")
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO ticket_commands (
    ticket_id, command_id, command_type, request_digest, resulting_status, occurred_at
) VALUES ($1, $2, 'cancel', $3, 'cancelled', $4)`,
		command.TicketID, command.CommandID, digest, command.CancelledAt,
	); err != nil {
		return tournament.TicketMutation{}, mapConflict(err, tournament.ErrIdempotencyConflict, "record cancellation command")
	}
	if changed {
		payload, err := json.Marshal(struct {
			TicketID    string                  `json:"ticket_id"`
			Status      tournament.TicketStatus `json:"status"`
			CancelledAt time.Time               `json:"cancelled_at"`
		}{TicketID: stored.ticket.ID, Status: stored.ticket.Status, CancelledAt: command.CancelledAt})
		if err != nil {
			return tournament.TicketMutation{}, fmt.Errorf("encode ticket cancellation event: %w", err)
		}
		if err := insertOutbox(ctx, tx, outboxEvent{
			ID: command.EventID, AggregateType: outboxAggregateTicket, AggregateID: stored.ticket.ID,
			AggregateVersion: stored.ticket.AggregateVersion, Type: outboxTicketCancelled,
			Payload: string(payload), OccurredAt: command.CancelledAt,
		}); err != nil {
			return tournament.TicketMutation{}, mapConflict(err, tournament.ErrIdempotencyConflict, "insert ticket cancellation event")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("commit ticket cancellation: %w", err)
	}
	return tournament.TicketMutation{Ticket: stored.ticket, Changed: changed}, nil
}

func (store *TicketStore) ExpireTicket(ctx context.Context, command tournament.ExpireTicketCommand) (tournament.TicketMutation, error) {
	digest, err := command.RequestDigest()
	if err != nil {
		return tournament.TicketMutation{}, err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("begin ticket expiry: %w", err)
	}
	defer rollback(tx, ctx)()

	stored, err := loadTicketByID(ctx, tx, command.TicketID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return tournament.TicketMutation{}, tournament.ErrTicketNotFound
	}
	if err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("lock ticket for expiry: %w", err)
	}

	var storedDigest string
	err = tx.QueryRow(ctx, `
SELECT request_digest
FROM ticket_commands
WHERE ticket_id = $1 AND command_id = $2`, command.TicketID, command.CommandID).Scan(&storedDigest)
	if err == nil {
		if storedDigest != digest {
			return tournament.TicketMutation{}, tournament.ErrIdempotencyConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return tournament.TicketMutation{}, fmt.Errorf("commit expiry replay: %w", err)
		}
		return tournament.TicketMutation{Ticket: stored.ticket, Replay: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return tournament.TicketMutation{}, fmt.Errorf("read expiry command: %w", err)
	}
	if stored.ticket.Status != tournament.TicketQueued {
		return tournament.TicketMutation{}, tournament.ErrTicketNotQueued
	}
	claimOwned, err := ownsActiveClaim(ctx, tx, stored, command.ClaimToken)
	if err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("verify ticket expiry claim: %w", err)
	}
	if !claimOwned {
		return tournament.TicketMutation{}, tournament.ErrTicketClaimLost
	}

	stored.ticket.Status = tournament.TicketExpired
	stored.ticket.ExpiredAt = timePointer(command.ExpiredAt)
	stored.ticket.AggregateVersion++
	if _, err := tx.Exec(ctx, `
UPDATE matchmaking_tickets
SET status = 'expired', expired_at = $2, aggregate_version = $3,
    next_attempt_at = NULL, claim_token = NULL, claimed_until = NULL
WHERE ticket_id = $1`, command.TicketID, command.ExpiredAt, stored.ticket.AggregateVersion); err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("expire ticket: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ticket_commands (
    ticket_id, command_id, command_type, request_digest, resulting_status, occurred_at
) VALUES ($1, $2, 'expire', $3, 'expired', $4)`,
		command.TicketID, command.CommandID, digest, command.ExpiredAt,
	); err != nil {
		return tournament.TicketMutation{}, mapConflict(err, tournament.ErrIdempotencyConflict, "record expiry command")
	}

	payload, err := json.Marshal(struct {
		TicketID  string                  `json:"ticket_id"`
		Status    tournament.TicketStatus `json:"status"`
		Deadline  time.Time               `json:"deadline"`
		ExpiredAt time.Time               `json:"expired_at"`
	}{
		TicketID: stored.ticket.ID, Status: stored.ticket.Status,
		Deadline: command.Deadline, ExpiredAt: command.ExpiredAt,
	})
	if err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("encode ticket expiry event: %w", err)
	}
	if err := insertOutbox(ctx, tx, outboxEvent{
		ID: command.EventID, AggregateType: outboxAggregateTicket, AggregateID: stored.ticket.ID,
		AggregateVersion: stored.ticket.AggregateVersion, Type: outboxTicketExpired,
		Payload: string(payload), OccurredAt: command.ExpiredAt,
	}); err != nil {
		return tournament.TicketMutation{}, mapConflict(err, tournament.ErrIdempotencyConflict, "insert ticket expiry event")
	}
	if err := tx.Commit(ctx); err != nil {
		return tournament.TicketMutation{}, fmt.Errorf("commit ticket expiry: %w", err)
	}
	return tournament.TicketMutation{Ticket: stored.ticket, Changed: true}, nil
}

func (store *TicketStore) AssignTicket(ctx context.Context, command tournament.AssignTicketCommand) (tournament.Assignment, error) {
	digest, err := command.RequestDigest()
	if err != nil {
		return tournament.Assignment{}, err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return tournament.Assignment{}, fmt.Errorf("begin ticket assignment: %w", err)
	}
	defer rollback(tx, ctx)()

	replay, err := loadAssignmentByIdentity(ctx, tx, command.AssignmentID)
	if err == nil {
		if replay.requestDigest != digest || replay.assignment.TicketID != command.TicketID ||
			replay.assignment.RoomID != command.RoomID || replay.assignment.SessionID != command.SessionID {
			return tournament.Assignment{}, tournament.ErrAssignmentConflict
		}
		replay.assignment.Replay = true
		if err := tx.Commit(ctx); err != nil {
			return tournament.Assignment{}, fmt.Errorf("commit assignment replay: %w", err)
		}
		return replay.assignment, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return tournament.Assignment{}, fmt.Errorf("read assignment identity: %w", err)
	}

	stored, err := loadTicketByID(ctx, tx, command.TicketID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return tournament.Assignment{}, tournament.ErrTicketNotFound
	}
	if err != nil {
		return tournament.Assignment{}, fmt.Errorf("lock ticket for assignment: %w", err)
	}
	if stored.ticket.Status != tournament.TicketQueued {
		if stored.ticket.Status == tournament.TicketAssigned {
			return tournament.Assignment{}, tournament.ErrAssignmentConflict
		}
		return tournament.Assignment{}, tournament.ErrTicketNotQueued
	}
	if command.AssignedAt.Before(stored.ticket.RequestedAt) {
		return tournament.Assignment{}, errors.New("assignment cannot precede ticket acceptance")
	}
	if command.ClaimToken != "" {
		claimOwned, err := ownsActiveClaim(ctx, tx, stored, command.ClaimToken)
		if err != nil {
			return tournament.Assignment{}, fmt.Errorf("verify ticket assignment claim: %w", err)
		}
		if !claimOwned {
			return tournament.Assignment{}, tournament.ErrTicketClaimLost
		}
	}

	room, err := lockAssignmentRoom(ctx, tx, command.RoomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tournament.Assignment{}, tournament.ErrRoomNotAvailable
	}
	if err != nil {
		return tournament.Assignment{}, fmt.Errorf("lock room for assignment: %w", err)
	}
	if room.status != tournament.RoomForming || room.version != command.ExpectedRoomVersion || command.AssignedAt.After(room.fillDeadline) ||
		room.tournamentID != stored.ticket.TournamentID || room.tournamentVersion != stored.ticket.TournamentVersion ||
		room.ratingModelVersion != stored.ticket.RatingSnapshot.ModelVersion {
		return tournament.Assignment{}, tournament.ErrRoomNotAvailable
	}

	var members int
	var duplicatePlayer bool
	if err := tx.QueryRow(ctx, `
SELECT count(*), count(*) FILTER (WHERE player_id = $2) > 0
FROM room_memberships
WHERE room_id = $1`, room.id, stored.ticket.PlayerID).Scan(&members, &duplicatePlayer); err != nil {
		return tournament.Assignment{}, fmt.Errorf("inspect room memberships: %w", err)
	}
	if members >= room.capacity || duplicatePlayer {
		return tournament.Assignment{}, tournament.ErrRoomNotAvailable
	}

	var seat int
	if err := tx.QueryRow(ctx, `
SELECT candidate
FROM generate_series(1, $2) AS candidate
WHERE NOT EXISTS (
    SELECT 1 FROM room_memberships WHERE room_id = $1 AND seat = candidate
)
ORDER BY candidate
LIMIT 1`, room.id, room.capacity).Scan(&seat); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tournament.Assignment{}, tournament.ErrRoomNotAvailable
		}
		return tournament.Assignment{}, fmt.Errorf("select room seat: %w", err)
	}

	stored.ticket.Status = tournament.TicketAssigned
	stored.ticket.AssignedAt = timePointer(command.AssignedAt)
	stored.ticket.AggregateVersion++
	if _, err := tx.Exec(ctx, `
UPDATE matchmaking_tickets
SET status = 'assigned', assigned_at = $2, aggregate_version = $3,
    next_attempt_at = NULL, claim_token = NULL, claimed_until = NULL
WHERE ticket_id = $1`, stored.ticket.ID, command.AssignedAt, stored.ticket.AggregateVersion); err != nil {
		return tournament.Assignment{}, fmt.Errorf("assign ticket: %w", err)
	}

	roomFilled := members+1 == room.capacity
	room.version++
	var resultDeadline *time.Time
	if roomFilled {
		var deadline time.Time
		if err := tx.QueryRow(ctx, `
UPDATE rooms
SET status = 'collecting', aggregate_version = $2, filled_at = $3::timestamptz,
    result_deadline = $3::timestamptz + result_timeout_ms * interval '1 millisecond'
FROM tournament_configs
WHERE rooms.room_id = $1
  AND tournament_configs.tournament_id = rooms.tournament_id
  AND tournament_configs.version = rooms.tournament_version
RETURNING rooms.result_deadline`, room.id, room.version, command.AssignedAt).Scan(&deadline); err != nil {
			return tournament.Assignment{}, fmt.Errorf("fill assigned room: %w", err)
		}
		resultDeadline = &deadline
	} else if _, err := tx.Exec(ctx, `
UPDATE rooms SET aggregate_version = $2 WHERE room_id = $1`, room.id, room.version); err != nil {
		return tournament.Assignment{}, fmt.Errorf("advance room version: %w", err)
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO room_memberships (
    room_id, room_capacity, ticket_id, player_id, seat, assigned_at,
    assignment_id, request_digest, ticket_version, room_version,
    room_filled, result_deadline
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		room.id, room.capacity, stored.ticket.ID, stored.ticket.PlayerID, seat, command.AssignedAt,
		command.AssignmentID, digest, stored.ticket.AggregateVersion, room.version,
		roomFilled, resultDeadline,
	); err != nil {
		return tournament.Assignment{}, mapConflict(err, tournament.ErrAssignmentConflict, "insert room membership")
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO sessions (
    session_id, ticket_id, room_id, player_id, seat, status, allocated_at
) VALUES ($1, $2, $3, $4, $5, 'allocated', $6)`,
		command.SessionID, stored.ticket.ID, room.id, stored.ticket.PlayerID, seat, command.AssignedAt,
	); err != nil {
		return tournament.Assignment{}, mapConflict(err, tournament.ErrAssignmentConflict, "insert allocated session")
	}

	assignment := tournament.Assignment{
		AssignmentID: command.AssignmentID, TicketID: stored.ticket.ID, RoomID: room.id,
		SessionID: command.SessionID, PlayerID: stored.ticket.PlayerID, Seat: seat,
		AssignedAt: command.AssignedAt, TicketVersion: stored.ticket.AggregateVersion,
		RoomVersion: room.version, RoomFilled: roomFilled, ResultDeadline: resultDeadline,
	}
	if err := insertAssignmentEvents(ctx, tx, command, assignment); err != nil {
		return tournament.Assignment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return tournament.Assignment{}, mapConflict(err, tournament.ErrAssignmentConflict, "commit ticket assignment")
	}
	return assignment, nil
}

type storedTicket struct {
	ticket        tournament.Ticket
	requestDigest string
	claimToken    *string
	claimedUntil  *time.Time
	attemptCount  int64
}

func loadTicketByEntry(ctx context.Context, tx pgx.Tx, entryID string, forUpdate bool) (storedTicket, error) {
	query := ticketSelectSQL + " WHERE entry_id = $1"
	if forUpdate {
		query += " FOR UPDATE"
	}
	return scanStoredTicket(tx.QueryRow(ctx, query, entryID))
}

func loadTicketByID(ctx context.Context, tx pgx.Tx, ticketID string, forUpdate bool) (storedTicket, error) {
	query := ticketSelectSQL + " WHERE ticket_id = $1"
	if forUpdate {
		query += " FOR UPDATE"
	}
	return scanStoredTicket(tx.QueryRow(ctx, query, ticketID))
}

const ticketSelectSQL = `
SELECT ticket_id, entry_id, player_id, tournament_id, tournament_version,
       status, requested_at, assigned_at, cancelled_at, expired_at,
       snapshot_at, rating_mean, rating_uncertainty,
       rating_performance_deviation, rating_games, rating_model_version,
       rating_updated_at, aggregate_version, request_digest,
       claim_token, claimed_until, attempt_count
FROM matchmaking_tickets`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanStoredTicket(row rowScanner) (storedTicket, error) {
	var result storedTicket
	var status string
	err := row.Scan(
		&result.ticket.ID, &result.ticket.EntryID, &result.ticket.PlayerID,
		&result.ticket.TournamentID, &result.ticket.TournamentVersion,
		&status, &result.ticket.RequestedAt, &result.ticket.AssignedAt,
		&result.ticket.CancelledAt, &result.ticket.ExpiredAt, &result.ticket.SnapshotAt,
		&result.ticket.RatingSnapshot.Mean, &result.ticket.RatingSnapshot.Uncertainty,
		&result.ticket.RatingSnapshot.PerformanceDeviation, &result.ticket.RatingSnapshot.Games,
		&result.ticket.RatingSnapshot.ModelVersion, &result.ticket.RatingSnapshot.UpdatedAt,
		&result.ticket.AggregateVersion, &result.requestDigest,
		&result.claimToken, &result.claimedUntil, &result.attemptCount,
	)
	if err != nil {
		return storedTicket{}, err
	}
	result.ticket.Status = tournament.TicketStatus(status)
	return result, nil
}

type assignmentRoom struct {
	id                 string
	tournamentID       string
	tournamentVersion  string
	ratingModelVersion string
	status             tournament.RoomStatus
	capacity           int
	version            int64
	fillDeadline       time.Time
}

func lockAssignmentRoom(ctx context.Context, tx pgx.Tx, roomID string) (assignmentRoom, error) {
	var room assignmentRoom
	var status string
	err := tx.QueryRow(ctx, `
SELECT room_id, tournament_id, tournament_version, rating_model_version,
       status, capacity, aggregate_version, fill_deadline
FROM rooms
WHERE room_id = $1
FOR UPDATE`, roomID).Scan(
		&room.id, &room.tournamentID, &room.tournamentVersion, &room.ratingModelVersion,
		&status, &room.capacity, &room.version, &room.fillDeadline,
	)
	room.status = tournament.RoomStatus(status)
	return room, err
}

type storedAssignment struct {
	assignment    tournament.Assignment
	requestDigest string
}

func loadAssignmentByIdentity(ctx context.Context, tx pgx.Tx, assignmentID string) (storedAssignment, error) {
	var result storedAssignment
	err := tx.QueryRow(ctx, `
SELECT rm.assignment_id, rm.ticket_id, rm.room_id, s.session_id, rm.player_id,
       rm.seat, rm.assigned_at, rm.ticket_version, rm.room_version,
       rm.room_filled, rm.result_deadline, rm.request_digest
FROM room_memberships AS rm
JOIN sessions AS s ON s.ticket_id = rm.ticket_id
WHERE rm.assignment_id = $1`, assignmentID).Scan(
		&result.assignment.AssignmentID, &result.assignment.TicketID,
		&result.assignment.RoomID, &result.assignment.SessionID,
		&result.assignment.PlayerID, &result.assignment.Seat,
		&result.assignment.AssignedAt, &result.assignment.TicketVersion,
		&result.assignment.RoomVersion, &result.assignment.RoomFilled,
		&result.assignment.ResultDeadline, &result.requestDigest,
	)
	return result, err
}

type outboxEvent struct {
	ID               string
	AggregateType    string
	AggregateID      string
	AggregateVersion int64
	Type             string
	Payload          string
	OccurredAt       time.Time
}

func insertOutbox(ctx context.Context, tx pgx.Tx, event outboxEvent) error {
	_, err := tx.Exec(ctx, `
INSERT INTO outbox_events (
    event_id, aggregate_type, aggregate_id, aggregate_version,
    event_type, payload, occurred_at, available_at
) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $7)`,
		event.ID, event.AggregateType, event.AggregateID, event.AggregateVersion,
		event.Type, event.Payload, event.OccurredAt,
	)
	return err
}

func insertAssignmentEvents(ctx context.Context, tx pgx.Tx, command tournament.AssignTicketCommand, assignment tournament.Assignment) error {
	payload, err := json.Marshal(struct {
		TicketID   string    `json:"ticket_id"`
		RoomID     string    `json:"room_id"`
		SessionID  string    `json:"session_id"`
		PlayerID   string    `json:"player_id"`
		Seat       int       `json:"seat"`
		AssignedAt time.Time `json:"assigned_at"`
	}{
		TicketID: assignment.TicketID, RoomID: assignment.RoomID,
		SessionID: assignment.SessionID, PlayerID: assignment.PlayerID,
		Seat: assignment.Seat, AssignedAt: assignment.AssignedAt,
	})
	if err != nil {
		return fmt.Errorf("encode ticket assignment event: %w", err)
	}
	if err := insertOutbox(ctx, tx, outboxEvent{
		ID: command.TicketEventID, AggregateType: outboxAggregateTicket,
		AggregateID: assignment.TicketID, AggregateVersion: assignment.TicketVersion,
		Type: outboxTicketAssigned, Payload: string(payload), OccurredAt: assignment.AssignedAt,
	}); err != nil {
		return mapConflict(err, tournament.ErrAssignmentConflict, "insert ticket assignment event")
	}
	if !assignment.RoomFilled {
		return nil
	}
	roomPayload, err := json.Marshal(struct {
		RoomID         string    `json:"room_id"`
		FilledAt       time.Time `json:"filled_at"`
		ResultDeadline time.Time `json:"result_deadline"`
	}{RoomID: assignment.RoomID, FilledAt: assignment.AssignedAt, ResultDeadline: *assignment.ResultDeadline})
	if err != nil {
		return fmt.Errorf("encode room filled event: %w", err)
	}
	if err := insertOutbox(ctx, tx, outboxEvent{
		ID: command.RoomFilledEventID, AggregateType: outboxAggregateRoom,
		AggregateID: assignment.RoomID, AggregateVersion: assignment.RoomVersion,
		Type: outboxRoomFilled, Payload: string(roomPayload), OccurredAt: assignment.AssignedAt,
	}); err != nil {
		return mapConflict(err, tournament.ErrAssignmentConflict, "insert room filled event")
	}
	return nil
}

func rollback(tx pgx.Tx, ctx context.Context) func() {
	return func() {
		_ = tx.Rollback(ctx)
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func ownsActiveClaim(ctx context.Context, tx pgx.Tx, stored storedTicket, claimToken string) (bool, error) {
	if stored.claimToken == nil || *stored.claimToken != claimToken || stored.claimedUntil == nil {
		return false, nil
	}
	var active bool
	if err := tx.QueryRow(ctx, "SELECT $1::timestamptz > clock_timestamp()", *stored.claimedUntil).Scan(&active); err != nil {
		return false, err
	}
	return active, nil
}

func mapConflict(err error, conflict error, operation string) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && (postgresError.Code == "23505" || postgresError.Code == "23503" || postgresError.Code == "23514") {
		return fmt.Errorf("%s: %w", operation, conflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ tournament.TicketLifecycleRepository = (*TicketStore)(nil)
