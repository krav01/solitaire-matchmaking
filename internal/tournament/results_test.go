package tournament_test

import (
	"context"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

func TestFinalizeResultCommandDigestIsCanonical(t *testing.T) {
	t.Parallel()
	command := validResultCommand()
	first, err := command.RequestDigest()
	if err != nil {
		t.Fatalf("RequestDigest() error = %v", err)
	}
	command.AcceptedAt = command.AcceptedAt.Add(time.Minute)
	command.Participants[0], command.Participants[1] = command.Participants[1], command.Participants[0]
	second, err := command.RequestDigest()
	if err != nil {
		t.Fatalf("reordered RequestDigest() error = %v", err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("digests = %q and %q, want equal SHA-256 values", first, second)
	}
}

func TestFinalizeResultCommandRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	negative := int64(-1)
	tests := []struct {
		name   string
		mutate func(*tournament.FinalizeResultCommand)
	}{
		{name: "acceptance before availability", mutate: func(command *tournament.FinalizeResultCommand) {
			command.AcceptedAt = command.AvailableAt.Add(-time.Nanosecond)
		}},
		{name: "duplicate session", mutate: func(command *tournament.FinalizeResultCommand) {
			command.Participants[1].SessionID = command.Participants[0].SessionID
		}},
		{name: "negative feature", mutate: func(command *tournament.FinalizeResultCommand) { command.Participants[0].Features.Moves = &negative }},
		{name: "missing winner", mutate: func(command *tournament.FinalizeResultCommand) {
			for index := range command.Participants {
				command.Participants[index].Place = index + 2
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validResultCommand()
			test.mutate(&command)
			if err := command.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestResultServiceValidatesBeforeRepository(t *testing.T) {
	t.Parallel()
	repository := &resultRepositoryStub{}
	service, err := tournament.NewResultService(repository)
	if err != nil {
		t.Fatalf("NewResultService() error = %v", err)
	}
	command := validResultCommand()
	command.EventID = ""
	if _, err := service.Finalize(context.Background(), command); err == nil {
		t.Fatal("Finalize() error = nil")
	}
	if repository.finalizeCalls != 0 {
		t.Fatalf("repository calls = %d, want 0", repository.finalizeCalls)
	}
}

type resultRepositoryStub struct{ finalizeCalls int }

func (repository *resultRepositoryStub) FinalizeResult(context.Context, tournament.FinalizeResultCommand) (tournament.FinalizedResult, error) {
	repository.finalizeCalls++
	return tournament.FinalizedResult{}, nil
}

func (repository *resultRepositoryStub) ExpireResultRooms(context.Context, tournament.ResultDeadlineBatch) ([]tournament.ExpiredRoom, error) {
	return nil, nil
}

func validResultCommand() tournament.FinalizeResultCommand {
	finishedAt := time.Date(2026, time.September, 5, 1, 0, 0, 0, time.UTC)
	participants := make([]tournament.VerifiedParticipant, 5)
	for index := range participants {
		participants[index] = tournament.VerifiedParticipant{
			SessionID: "session-" + string(rune('a'+index)),
			PlayerID:  "player-" + string(rune('a'+index)),
			Place:     index + 1,
		}
	}
	return tournament.FinalizeResultCommand{
		EventID: "result-event", RoomID: "room-a", ModeID: "mode-a", DeckID: "deck-a",
		ScoringRulesVersion: "rules-v1", FinishedAt: finishedAt,
		AvailableAt: finishedAt.Add(time.Second), AcceptedAt: finishedAt.Add(2 * time.Second),
		Participants: participants,
	}
}
