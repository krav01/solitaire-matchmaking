package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krav01/solitaire-matchmaking/internal/postgres"
	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

func TestResultFinalizationPostgreSQL(t *testing.T) {
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

	prefix := fmt.Sprintf("result-%d", time.Now().UnixNano())
	startedAt := time.Now().UTC().Truncate(time.Millisecond)
	roomID, participants, _ := seedFilledResultRoom(t, ctx, pool, prefix, startedAt)
	store, err := postgres.NewResultStore(pool)
	if err != nil {
		t.Fatalf("NewResultStore() error = %v", err)
	}
	service, err := tournament.NewResultService(store)
	if err != nil {
		t.Fatalf("NewResultService() error = %v", err)
	}

	completed := false
	participants[len(participants)-1].Features.Completed = &completed
	command := tournament.FinalizeResultCommand{
		EventID: prefix + "-event", RoomID: roomID, ModeID: "solitaire", DeckID: roomID + "-deck",
		ScoringRulesVersion: "scoring-v1", FinishedAt: startedAt.Add(10 * time.Second),
		AvailableAt: startedAt.Add(11 * time.Second), AcceptedAt: startedAt.Add(12 * time.Second),
		Participants: participants,
	}
	result, err := service.Finalize(ctx, command)
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if result.Replay || !result.RatingPending || result.RoomVersion != 7 {
		t.Fatalf("Finalize() = %+v", result)
	}

	retry := command
	retry.AcceptedAt = retry.AcceptedAt.Add(time.Second)
	replayed, err := service.Finalize(ctx, retry)
	if err != nil || !replayed.Replay || !replayed.CompletedAt.Equal(command.AcceptedAt) {
		t.Fatalf("Finalize(replay) = %+v, error = %v", replayed, err)
	}
	conflict := retry
	conflict.Participants = append([]tournament.VerifiedParticipant(nil), retry.Participants...)
	conflict.Participants[0].Place, conflict.Participants[1].Place = conflict.Participants[1].Place, conflict.Participants[0].Place
	if _, err := service.Finalize(ctx, conflict); !errors.Is(err, tournament.ErrResultConflict) {
		t.Fatalf("Finalize(conflict) error = %v", err)
	}

	assertFinalizedResultRows(t, ctx, pool, command)

	latePrefix := prefix + "-late"
	lateRoomID, lateParticipants, deadline := seedFilledResultRoom(t, ctx, pool, latePrefix, startedAt)
	late := tournament.FinalizeResultCommand{
		EventID: latePrefix + "-event", RoomID: lateRoomID, ModeID: "solitaire", DeckID: lateRoomID + "-deck",
		ScoringRulesVersion: "scoring-v1", FinishedAt: deadline.Add(-time.Second),
		AvailableAt: deadline, AcceptedAt: deadline.Add(time.Nanosecond), Participants: lateParticipants,
	}
	if _, err := service.Finalize(ctx, late); !errors.Is(err, tournament.ErrResultDeadlinePassed) {
		t.Fatalf("Finalize(late) error = %v", err)
	}
	expired, err := service.ExpireDue(ctx, tournament.ResultDeadlineBatch{Limit: 16, ExpiredAt: deadline.Add(time.Second)})
	if err != nil {
		t.Fatalf("ExpireDue() error = %v", err)
	}
	found := false
	for _, room := range expired {
		found = found || room.RoomID == lateRoomID
	}
	if !found {
		t.Fatalf("ExpireDue() = %+v, missing room %q", expired, lateRoomID)
	}
	var roomStatus string
	var expiryEvents int
	if err := pool.QueryRow(ctx, `
SELECT room.status,
       (SELECT count(*) FROM outbox_events WHERE aggregate_id = room.room_id AND event_type = 'room.expired')
FROM rooms AS room
WHERE room.room_id = $1`, lateRoomID).Scan(&roomStatus, &expiryEvents); err != nil {
		t.Fatalf("read expired room: %v", err)
	}
	if roomStatus != "expired" || expiryEvents != 1 {
		t.Fatalf("expired room status = %q, events = %d", roomStatus, expiryEvents)
	}
}

func seedFilledResultRoom(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prefix string, startedAt time.Time) (string, []tournament.VerifiedParticipant, time.Time) {
	t.Helper()
	modelVersion := prefix + "-model"
	policyVersion := prefix + "-policy"
	tournamentID := prefix + "-tournament"
	roomID := prefix + "-room"
	seedLifecycleConfig(t, ctx, pool, modelVersion, policyVersion, tournamentID, "v1", roomID, startedAt)
	ticketStore, err := postgres.NewTicketStore(pool)
	if err != nil {
		t.Fatalf("NewTicketStore() error = %v", err)
	}
	ticketService, err := tournament.NewTicketService(ticketStore)
	if err != nil {
		t.Fatalf("NewTicketService() error = %v", err)
	}
	participants := make([]tournament.VerifiedParticipant, 5)
	var resultDeadline time.Time
	for index := range participants {
		identity := fmt.Sprintf("%s-player-%d", prefix, index)
		accepted := lifecycleAcceptCommand(identity, tournamentID, "v1", modelVersion, startedAt)
		if _, err := ticketService.Accept(ctx, accepted); err != nil {
			t.Fatalf("Accept(player %d) error = %v", index, err)
		}
		sessionID := fmt.Sprintf("%s-session-%d", prefix, index)
		assignment, err := ticketService.Assign(ctx, tournament.AssignTicketCommand{
			AssignmentID: fmt.Sprintf("%s-assignment-%d", prefix, index),
			TicketID:     accepted.Ticket.ID, RoomID: roomID, ExpectedRoomVersion: int64(index + 1),
			SessionID: sessionID, TicketEventID: fmt.Sprintf("%s-ticket-event-%d", prefix, index),
			RoomFilledEventID: fmt.Sprintf("%s-filled-event-%d", prefix, index),
			AssignedAt:        startedAt.Add(time.Duration(index+1) * time.Second),
		})
		if err != nil {
			t.Fatalf("Assign(player %d) error = %v", index, err)
		}
		if assignment.ResultDeadline != nil {
			resultDeadline = *assignment.ResultDeadline
		}
		participants[index] = tournament.VerifiedParticipant{
			SessionID: sessionID, PlayerID: identity, Place: index + 1,
		}
	}
	if resultDeadline.IsZero() {
		t.Fatal("filled result room has no result deadline")
	}
	return roomID, participants, resultDeadline
}

func assertFinalizedResultRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, command tournament.FinalizeResultCommand) {
	t.Helper()
	var status string
	var resultRows, participantRows, submittedSessions, forfeitedSessions, completedEvents int
	if err := pool.QueryRow(ctx, `
SELECT room.status,
       (SELECT count(*) FROM verified_results WHERE event_id = $2),
       (SELECT count(*) FROM verified_result_participants WHERE event_id = $2),
       (SELECT count(*) FROM sessions WHERE room_id = room.room_id AND status = 'submitted'),
       (SELECT count(*) FROM sessions WHERE room_id = room.room_id AND status = 'forfeited'),
       (SELECT count(*) FROM outbox_events WHERE aggregate_id = room.room_id AND event_type = 'room.completed')
FROM rooms AS room
WHERE room.room_id = $1`, command.RoomID, command.EventID).Scan(
		&status, &resultRows, &participantRows, &submittedSessions, &forfeitedSessions, &completedEvents,
	); err != nil {
		t.Fatalf("read finalized result rows: %v", err)
	}
	if status != "completed" || resultRows != 1 || participantRows != len(command.Participants) ||
		submittedSessions != len(command.Participants)-1 || forfeitedSessions != 1 || completedEvents != 1 {
		t.Fatalf("finalized rows: status=%q result=%d participants=%d submitted=%d forfeited=%d events=%d",
			status, resultRows, participantRows, submittedSessions, forfeitedSessions, completedEvents)
	}
}
