package tournament_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestAcceptTicketDigestUsesExternalRequestFields(t *testing.T) {
	t.Parallel()
	first := validAcceptCommand()
	second := first
	second.Ticket.ID = "generated-ticket-2"
	second.EventID = "generated-event-2"
	offset := time.FixedZone("retry-offset", 3*60*60)
	second.Ticket.RequestedAt = second.Ticket.RequestedAt.In(offset)
	second.Ticket.SnapshotAt = second.Ticket.SnapshotAt.In(offset)
	second.Ticket.RatingSnapshot.UpdatedAt = second.Ticket.RatingSnapshot.UpdatedAt.In(offset)

	firstDigest, err := first.RequestDigest()
	if err != nil {
		t.Fatalf("first RequestDigest() error = %v", err)
	}
	secondDigest, err := second.RequestDigest()
	if err != nil {
		t.Fatalf("second RequestDigest() error = %v", err)
	}
	if firstDigest != secondDigest || len(firstDigest) != 64 {
		t.Fatalf("generated identities changed retry digest: %q != %q", firstDigest, secondDigest)
	}

	second.Ticket.PlayerID = "different-player"
	differentDigest, err := second.RequestDigest()
	if err != nil {
		t.Fatalf("changed RequestDigest() error = %v", err)
	}
	if differentDigest == firstDigest {
		t.Fatal("changed request produced the same digest")
	}
}

func TestCommandDigestsIgnoreProcessingTimeAndEventIdentities(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 15, 0, 0, 0, time.UTC)
	cancel := tournament.CancelTicketCommand{
		TicketID: "ticket-1", CommandID: "cancel-1", EventID: "event-1", CancelledAt: now,
	}
	cancelRetry := cancel
	cancelRetry.EventID = "event-2"
	cancelRetry.CancelledAt = now.Add(time.Minute)
	assertSameDigest(t, cancel.RequestDigest, cancelRetry.RequestDigest)

	assign := tournament.AssignTicketCommand{
		AssignmentID: "assignment-1", TicketID: "ticket-1", RoomID: "room-1",
		ExpectedRoomVersion: 1,
		SessionID:           "session-1", TicketEventID: "ticket-event-1",
		RoomFilledEventID: "room-event-1", AssignedAt: now,
	}
	assignRetry := assign
	assignRetry.TicketEventID = "ticket-event-2"
	assignRetry.RoomFilledEventID = "room-event-2"
	assignRetry.AssignedAt = now.Add(time.Minute)
	assertSameDigest(t, assign.RequestDigest, assignRetry.RequestDigest)
}

func TestAcceptTicketRejectsInvalidRatingBeforeRepository(t *testing.T) {
	t.Parallel()
	command := validAcceptCommand()
	command.Ticket.RatingSnapshot.Mean = math.NaN()
	repository := &recordingTicketRepository{}
	service, err := tournament.NewTicketService(repository)
	if err != nil {
		t.Fatalf("NewTicketService() error = %v", err)
	}
	if _, err := service.Accept(context.Background(), command); err == nil {
		t.Fatal("Accept() error = nil for invalid rating")
	}
	if repository.calls != 0 {
		t.Fatal("invalid command reached repository")
	}
}

func validAcceptCommand() tournament.AcceptTicketCommand {
	requestedAt := time.Date(2026, time.September, 4, 15, 0, 0, 0, time.UTC)
	return tournament.AcceptTicketCommand{
		EventID: "ticket-accepted-1",
		Ticket: tournament.Ticket{
			ID: "ticket-1", EntryID: "entry-1", PlayerID: "player-1",
			TournamentID: "daily", TournamentVersion: "v1", Status: tournament.TicketQueued,
			RequestedAt: requestedAt, SnapshotAt: requestedAt,
			RatingSnapshot: rating.Estimate{
				Mean: 25, Uncertainty: 8, Games: 10, ModelVersion: "rating-v1",
				UpdatedAt: requestedAt.Add(-time.Minute),
			},
		},
	}
}

func assertSameDigest(t *testing.T, first, second func() (string, error)) {
	t.Helper()
	firstDigest, err := first()
	if err != nil {
		t.Fatalf("first digest error = %v", err)
	}
	secondDigest, err := second()
	if err != nil {
		t.Fatalf("second digest error = %v", err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("retry digest changed: %q != %q", firstDigest, secondDigest)
	}
}

type recordingTicketRepository struct {
	calls int
}

func (repository *recordingTicketRepository) AcceptTicket(context.Context, tournament.AcceptTicketCommand) (tournament.TicketMutation, error) {
	repository.calls++
	return tournament.TicketMutation{}, nil
}

func (repository *recordingTicketRepository) CancelTicket(context.Context, tournament.CancelTicketCommand) (tournament.TicketMutation, error) {
	repository.calls++
	return tournament.TicketMutation{}, nil
}

func (repository *recordingTicketRepository) AssignTicket(context.Context, tournament.AssignTicketCommand) (tournament.Assignment, error) {
	repository.calls++
	return tournament.Assignment{}, nil
}
