package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestTicketLifecyclePostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, databaseURL, 8)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer pool.Close()
	if _, err := postgres.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}

	prefix := fmt.Sprintf("lifecycle-%d", time.Now().UnixNano())
	modelVersion := prefix + "-model"
	policyVersion := prefix + "-policy"
	tournamentID := prefix + "-tournament"
	tournamentVersion := "v1"
	roomID := prefix + "-room"
	startedAt := time.Date(2026, time.September, 4, 16, 0, 0, 0, time.UTC)
	seedLifecycleConfig(t, ctx, pool, modelVersion, policyVersion, tournamentID, tournamentVersion, roomID, startedAt)

	store, err := postgres.NewTicketStore(pool)
	if err != nil {
		t.Fatalf("NewTicketStore() error = %v", err)
	}
	service, err := tournament.NewTicketService(store)
	if err != nil {
		t.Fatalf("NewTicketService() error = %v", err)
	}
	missingTournament := lifecycleAcceptCommand(prefix+"-missing-tournament", prefix+"-unknown", tournamentVersion, modelVersion, startedAt)
	if _, err := service.Accept(ctx, missingTournament); !errors.Is(err, tournament.ErrTournamentNotFound) {
		t.Fatalf("Accept(missing tournament) error = %v", err)
	}

	cancelCommand := lifecycleAcceptCommand(prefix+"-cancel", tournamentID, tournamentVersion, modelVersion, startedAt)
	accepted, err := service.Accept(ctx, cancelCommand)
	if err != nil || !accepted.Changed || accepted.Replay {
		t.Fatalf("first Accept() = %+v, error = %v", accepted, err)
	}
	queuedState, err := service.Get(ctx, accepted.Ticket.ID)
	if err != nil || queuedState.Ticket.Status != tournament.TicketQueued || queuedState.Assignment != nil {
		t.Fatalf("queued Get() = %+v, error = %v", queuedState, err)
	}
	retry := cancelCommand
	retry.Ticket.ID = prefix + "-ignored-ticket-id"
	retry.EventID = prefix + "-ignored-event-id"
	replayed, err := service.Accept(ctx, retry)
	if err != nil || !replayed.Replay || replayed.Ticket.ID != cancelCommand.Ticket.ID {
		t.Fatalf("replayed Accept() = %+v, error = %v", replayed, err)
	}
	conflict := retry
	conflict.Ticket.PlayerID = prefix + "-different-player"
	if _, err := service.Accept(ctx, conflict); !errors.Is(err, tournament.ErrIdempotencyConflict) {
		t.Fatalf("conflicting Accept() error = %v", err)
	}
	atomicityCommand := lifecycleAcceptCommand(prefix+"-atomicity", tournamentID, tournamentVersion, modelVersion, startedAt)
	atomicityCommand.EventID = cancelCommand.EventID
	if _, err := service.Accept(ctx, atomicityCommand); !errors.Is(err, tournament.ErrIdempotencyConflict) {
		t.Fatalf("outbox-conflicting Accept() error = %v", err)
	}
	var rolledBackTickets int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM matchmaking_tickets WHERE entry_id = $1", atomicityCommand.Ticket.EntryID).Scan(&rolledBackTickets); err != nil {
		t.Fatalf("read rolled-back ticket: %v", err)
	}
	if rolledBackTickets != 0 {
		t.Fatal("ticket persisted without its outbox event")
	}

	cancellation := tournament.CancelTicketCommand{
		TicketID: cancelCommand.Ticket.ID, CommandID: prefix + "-cancel-command",
		EventID: prefix + "-cancel-event", CancelledAt: startedAt.Add(time.Second),
	}
	cancelled, err := service.Cancel(ctx, cancellation)
	if err != nil || !cancelled.Changed || cancelled.Ticket.Status != tournament.TicketCancelled {
		t.Fatalf("Cancel() = %+v, error = %v", cancelled, err)
	}
	cancellation.EventID = prefix + "-ignored-cancel-event"
	cancellation.CancelledAt = cancellation.CancelledAt.Add(time.Second)
	cancelled, err = service.Cancel(ctx, cancellation)
	if err != nil || !cancelled.Replay || cancelled.Changed {
		t.Fatalf("replayed Cancel() = %+v, error = %v", cancelled, err)
	}

	commands := make([]tournament.AssignTicketCommand, 6)
	for index := range commands {
		acceptedCommand := lifecycleAcceptCommand(fmt.Sprintf("%s-player-%d", prefix, index), tournamentID, tournamentVersion, modelVersion, startedAt)
		if _, err := service.Accept(ctx, acceptedCommand); err != nil {
			t.Fatalf("Accept(player %d) error = %v", index, err)
		}
		commands[index] = tournament.AssignTicketCommand{
			AssignmentID: fmt.Sprintf("%s-assignment-%d", prefix, index),
			TicketID:     acceptedCommand.Ticket.ID, RoomID: roomID,
			ExpectedRoomVersion: int64(index + 1),
			SessionID:           fmt.Sprintf("%s-session-%d", prefix, index),
			TicketEventID:       fmt.Sprintf("%s-assigned-event-%d", prefix, index),
			RoomFilledEventID:   fmt.Sprintf("%s-filled-event-%d", prefix, index),
			AssignedAt:          startedAt.Add(time.Duration(index+2) * time.Second),
		}
		if index >= 4 {
			commands[index].ExpectedRoomVersion = 5
		}
	}
	for index := 0; index < 4; index++ {
		assignment, err := service.Assign(ctx, commands[index])
		if err != nil || assignment.RoomFilled || assignment.Seat != index+1 {
			t.Fatalf("Assign(%d) = %+v, error = %v", index, assignment, err)
		}
	}

	type assignmentOutcome struct {
		command    tournament.AssignTicketCommand
		assignment tournament.Assignment
		err        error
	}
	outcomes := make(chan assignmentOutcome, 2)
	var group sync.WaitGroup
	for _, command := range commands[4:] {
		group.Add(1)
		go func(command tournament.AssignTicketCommand) {
			defer group.Done()
			assignment, err := service.Assign(ctx, command)
			outcomes <- assignmentOutcome{command: command, assignment: assignment, err: err}
		}(command)
	}
	group.Wait()
	close(outcomes)

	var successful assignmentOutcome
	successes := 0
	rejections := 0
	for outcome := range outcomes {
		switch {
		case outcome.err == nil:
			successes++
			successful = outcome
			if !outcome.assignment.RoomFilled || outcome.assignment.ResultDeadline == nil {
				t.Fatalf("final assignment = %+v", outcome.assignment)
			}
		case errors.Is(outcome.err, tournament.ErrRoomNotAvailable):
			rejections++
		default:
			t.Fatalf("concurrent Assign() error = %v", outcome.err)
		}
	}
	if successes != 1 || rejections != 1 {
		t.Fatalf("concurrent assignments: successes = %d, rejections = %d", successes, rejections)
	}
	assignedState, err := service.Get(ctx, successful.command.TicketID)
	if err != nil || assignedState.Ticket.Status != tournament.TicketAssigned || assignedState.Assignment == nil ||
		assignedState.Assignment.AssignmentID != successful.assignment.AssignmentID ||
		assignedState.Assignment.SessionID != successful.assignment.SessionID {
		t.Fatalf("assigned Get() = %+v, error = %v", assignedState, err)
	}
	if _, err := service.Get(ctx, prefix+"-missing-ticket"); !errors.Is(err, tournament.ErrTicketNotFound) {
		t.Fatalf("missing Get() error = %v", err)
	}

	queryStore, err := postgres.NewQueryStore(pool)
	if err != nil {
		t.Fatalf("NewQueryStore() error = %v", err)
	}
	room, err := queryStore.GetRoom(ctx, roomID)
	if err != nil || room.Status != tournament.RoomCollecting || room.AggregateVersion != 6 ||
		len(room.Members) != 5 || room.Members[0].Seat != 1 || room.Members[4].Seat != 5 {
		t.Fatalf("GetRoom() = %+v, error = %v", room, err)
	}
	if _, err := queryStore.GetRoom(ctx, prefix+"-missing-room"); !errors.Is(err, tournament.ErrRoomNotFound) {
		t.Fatalf("GetRoom(missing) error = %v", err)
	}

	olderModelVersion := prefix + "-older-model"
	mustExec(t, ctx, pool,
		"INSERT INTO rating_models (model_version, parameters_digest) VALUES ($1, $2)",
		olderModelVersion, strings.Repeat("c", 64),
	)
	ratingPlayerID := prefix + "-rated-player"
	mustExec(t, ctx, pool, `
INSERT INTO player_ratings (
    player_id, mode_id, model_version, mean, uncertainty,
    performance_deviation, games, updated_at, revision
) VALUES
    ($1, 'solitaire', $2, 24, 8, NULL, 3, $3, 7),
    ($1, 'solitaire', $4, 26, 6, 2, 4, $5, 1)`,
		ratingPlayerID, olderModelVersion, startedAt, modelVersion, startedAt.Add(time.Second),
	)
	currentRating, err := queryStore.GetRating(ctx, ratingPlayerID, "solitaire")
	if err != nil || currentRating.Estimate.ModelVersion != modelVersion ||
		currentRating.Estimate.Mean != 26 || currentRating.Revision != 1 {
		t.Fatalf("GetRating() = %+v, error = %v", currentRating, err)
	}
	if _, err := queryStore.GetRating(ctx, prefix+"-missing-player", "solitaire"); !errors.Is(err, tournament.ErrRatingNotFound) {
		t.Fatalf("GetRating(missing) error = %v", err)
	}

	retryAssignment := successful.command
	retryAssignment.AssignedAt = retryAssignment.AssignedAt.Add(time.Minute)
	retryAssignment.TicketEventID = prefix + "-ignored-assignment-event"
	retryAssignment.RoomFilledEventID = prefix + "-ignored-room-event"
	replayedAssignment, err := service.Assign(ctx, retryAssignment)
	if err != nil || !replayedAssignment.Replay || replayedAssignment.Seat != successful.assignment.Seat ||
		!replayedAssignment.AssignedAt.Equal(successful.assignment.AssignedAt) {
		t.Fatalf("replayed Assign() = %+v, error = %v", replayedAssignment, err)
	}

	assertLifecycleRows(t, ctx, pool, roomID, prefix)
}

func TestMatchmakingWorkerPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, databaseURL, 12)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer pool.Close()
	if _, err := postgres.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}
	mustExec(t, ctx, pool, "DELETE FROM matchmaking_tickets WHERE status = 'queued'")

	prefix := fmt.Sprintf("worker-%d", time.Now().UnixNano())
	modelVersion := prefix + "-model"
	policyVersion := prefix + "-policy"
	tournamentID := prefix + "-tournament"
	roomID := prefix + "-room"
	startedAt := time.Now().UTC().Truncate(time.Millisecond)
	seedLifecycleConfig(t, ctx, pool, modelVersion, policyVersion, tournamentID, "v1", roomID, startedAt)

	ticketStore, err := postgres.NewTicketStore(pool)
	if err != nil {
		t.Fatalf("NewTicketStore() error = %v", err)
	}
	ticketService, err := tournament.NewTicketService(ticketStore)
	if err != nil {
		t.Fatalf("NewTicketService() error = %v", err)
	}
	queue, err := postgres.NewMatchmakingQueue(pool)
	if err != nil {
		t.Fatalf("NewMatchmakingQueue() error = %v", err)
	}
	processor, err := worker.NewMatchProcessor(queue, queue, ticketService, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("NewMatchProcessor() error = %v", err)
	}

	for index := range 5 {
		command := lifecycleAcceptCommand(fmt.Sprintf("%s-player-%d", prefix, index), tournamentID, "v1", modelVersion, startedAt)
		if _, err := ticketService.Accept(ctx, command); err != nil {
			t.Fatalf("Accept(player %d) error = %v", index, err)
		}
	}

	evaluatedAt := startedAt
	for round := range 10 {
		claims := claimConcurrently(t, ctx, queue, prefix, round, evaluatedAt)
		if round == 0 {
			if len(claims) != 5 {
				t.Fatalf("first concurrent claim count = %d, want 5", len(claims))
			}
			seen := make(map[string]struct{}, len(claims))
			for _, claim := range claims {
				if _, duplicate := seen[claim.Ticket.ID]; duplicate {
					t.Fatalf("ticket %q was claimed twice", claim.Ticket.ID)
				}
				seen[claim.Ticket.ID] = struct{}{}
			}
		}
		processClaims(t, ctx, processor, claims, evaluatedAt)

		var queued int
		if err := pool.QueryRow(ctx, `
SELECT count(*) FROM matchmaking_tickets
WHERE tournament_id = $1 AND status = 'queued'`, tournamentID).Scan(&queued); err != nil {
			t.Fatalf("count queued tickets: %v", err)
		}
		if queued == 0 {
			break
		}
		if round == 9 {
			t.Fatalf("worker left %d tickets queued", queued)
		}
		evaluatedAt = evaluatedAt.Add(100 * time.Millisecond)
	}
	assertLifecycleRows(t, ctx, pool, roomID, prefix)

	expiring := lifecycleAcceptCommand(prefix+"-expiring", tournamentID, "v1", modelVersion, startedAt)
	if _, err := ticketService.Accept(ctx, expiring); err != nil {
		t.Fatalf("Accept(expiring) error = %v", err)
	}
	deadline := startedAt.Add(time.Minute)
	claims, err := queue.ClaimMatchmakingTickets(ctx, worker.ClaimRequest{
		Token: prefix + "-expiry-claim", Limit: 1,
		ClaimedAt: deadline, LeaseUntil: deadline.Add(10 * time.Second),
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("expiry claim = %+v, error = %v", claims, err)
	}
	if err := processor.Handle(ctx, claims[0], deadline); err != nil {
		t.Fatalf("Handle(expiry) error = %v", err)
	}
	var status string
	var expiryEvents int
	if err := pool.QueryRow(ctx, `
SELECT ticket.status,
       (SELECT count(*) FROM outbox_events WHERE aggregate_id = ticket.ticket_id AND event_type = 'ticket.expired')
FROM matchmaking_tickets AS ticket
WHERE ticket.ticket_id = $1`, expiring.Ticket.ID).Scan(&status, &expiryEvents); err != nil {
		t.Fatalf("read expired ticket: %v", err)
	}
	if status != "expired" || expiryEvents != 1 {
		t.Fatalf("expired ticket status = %q, events = %d", status, expiryEvents)
	}
}

func claimConcurrently(t *testing.T, ctx context.Context, queue *postgres.MatchmakingQueue, prefix string, round int, claimedAt time.Time) []worker.TicketClaim {
	t.Helper()
	outcomes := make(chan []worker.TicketClaim, 2)
	errorsFound := make(chan error, 2)
	var group sync.WaitGroup
	for index := range 2 {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			claims, err := queue.ClaimMatchmakingTickets(ctx, worker.ClaimRequest{
				Token: fmt.Sprintf("%s-round-%d-worker-%d", prefix, round, index), Limit: 5,
				ClaimedAt: claimedAt, LeaseUntil: claimedAt.Add(10 * time.Second),
			})
			if err != nil {
				errorsFound <- err
				return
			}
			outcomes <- claims
		}(index)
	}
	group.Wait()
	close(outcomes)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent ClaimMatchmakingTickets() error = %v", err)
	}
	var claims []worker.TicketClaim
	for batch := range outcomes {
		claims = append(claims, batch...)
	}
	return claims
}

func processClaims(t *testing.T, ctx context.Context, processor *worker.MatchProcessor, claims []worker.TicketClaim, evaluatedAt time.Time) {
	t.Helper()
	errorsFound := make(chan error, len(claims))
	var group sync.WaitGroup
	for _, claim := range claims {
		group.Add(1)
		go func(claim worker.TicketClaim) {
			defer group.Done()
			if err := processor.Handle(ctx, claim, evaluatedAt); err != nil {
				errorsFound <- err
			}
		}(claim)
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("Handle(claim) error = %v", err)
	}
}

func seedLifecycleConfig(t *testing.T, ctx context.Context, pool *pgxpool.Pool, modelVersion, policyVersion, tournamentID, tournamentVersion, roomID string, startedAt time.Time) {
	t.Helper()
	mustExec(t, ctx, pool,
		"INSERT INTO rating_models (model_version, parameters_digest) VALUES ($1, $2)",
		modelVersion, strings.Repeat("a", 64),
	)
	mustExec(t, ctx, pool, `
INSERT INTO matching_policies (policy_version, rating_model_version, definition, definition_digest)
VALUES (
    $1,
    $2,
    '{
        "initial_skill_gap": 100,
        "max_skill_gap": 100,
        "max_win_probability_spread": 1,
        "expansion_interval_ms": 1000,
        "fill_timeout_ms": 60000,
        "age_priority_after_ms": 30000,
        "candidate_limit": 100,
        "room_limit": 100,
        "prefer_nearly_full": true
    }'::jsonb,
    $3
)`, policyVersion, modelVersion, strings.Repeat("b", 64))
	mustExec(t, ctx, pool, `
INSERT INTO tournament_configs (
    tournament_id, version, mode_id, capacity, entry_fee_minor, currency,
    scoring_rules_version, settlement_version, policy_version,
    rating_model_version, result_timeout_ms, active_from
) VALUES ($1, $2, $3, 5, 100, 'USD', $4, $5, $6, $7, 60000, $8)`,
		tournamentID, tournamentVersion, "solitaire", "scoring-v1", "settlement-v1",
		policyVersion, modelVersion, startedAt,
	)
	mustExec(t, ctx, pool, `
INSERT INTO rooms (
    room_id, tournament_id, tournament_version, mode_id, policy_version,
    rating_model_version, scoring_rules_version, settlement_version,
    deck_id, capacity, status, created_at, fill_deadline
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 5, 'forming', $10, $11)`,
		roomID, tournamentID, tournamentVersion, "solitaire", policyVersion,
		modelVersion, "scoring-v1", "settlement-v1", roomID+"-deck", startedAt,
		startedAt.Add(time.Minute),
	)
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, arguments ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, arguments...); err != nil {
		t.Fatalf("execute lifecycle fixture: %v", err)
	}
}

func lifecycleAcceptCommand(identity, tournamentID, tournamentVersion, modelVersion string, requestedAt time.Time) tournament.AcceptTicketCommand {
	return tournament.AcceptTicketCommand{
		EventID: identity + "-accepted-event",
		Ticket: tournament.Ticket{
			ID: identity + "-ticket", EntryID: identity + "-entry", PlayerID: identity,
			TournamentID: tournamentID, TournamentVersion: tournamentVersion,
			Status: tournament.TicketQueued, RequestedAt: requestedAt, SnapshotAt: requestedAt,
			RatingSnapshot: rating.Estimate{
				Mean: 25, Uncertainty: 8, Games: 3, ModelVersion: modelVersion,
				UpdatedAt: requestedAt.Add(-time.Minute),
			},
		},
	}
}

func assertLifecycleRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, roomID, prefix string) {
	t.Helper()
	var status string
	var members int
	var sessions int
	if err := pool.QueryRow(ctx, `
SELECT r.status, count(DISTINCT rm.ticket_id), count(DISTINCT s.session_id)
FROM rooms AS r
LEFT JOIN room_memberships AS rm ON rm.room_id = r.room_id
LEFT JOIN sessions AS s ON s.room_id = r.room_id
WHERE r.room_id = $1
GROUP BY r.status`, roomID).Scan(&status, &members, &sessions); err != nil {
		t.Fatalf("read assigned room: %v", err)
	}
	if status != "collecting" || members != 5 || sessions != 5 {
		t.Fatalf("room status = %q, members = %d, sessions = %d", status, members, sessions)
	}

	var assignmentEvents int
	var filledEvents int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE event_type = 'ticket.assigned'),
       count(*) FILTER (WHERE event_type = 'room.filled')
FROM outbox_events
WHERE aggregate_id LIKE $1`, prefix+"%").Scan(&assignmentEvents, &filledEvents); err != nil {
		t.Fatalf("read lifecycle outbox: %v", err)
	}
	if assignmentEvents != 5 || filledEvents != 1 {
		t.Fatalf("assignment events = %d, room filled events = %d", assignmentEvents, filledEvents)
	}
}
