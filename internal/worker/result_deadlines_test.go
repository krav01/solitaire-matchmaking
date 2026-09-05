package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

func TestResultDeadlineRunnerUsesBoundedBatch(t *testing.T) {
	t.Parallel()
	service := &resultDeadlineServiceStub{rooms: []tournament.ExpiredRoom{{RoomID: "room-a"}, {RoomID: "room-b"}}}
	observer := &workerObserverStub{}
	runner, err := NewResultDeadlineRunner(service, slog.New(slog.NewTextHandler(io.Discard, nil)), ResultDeadlineOptions{
		BatchSize: 7, PollInterval: time.Second, Observer: observer,
	})
	if err != nil {
		t.Fatalf("NewResultDeadlineRunner() error = %v", err)
	}
	now := time.Date(2026, time.September, 5, 2, 0, 0, 0, time.UTC)
	runner.now = func() time.Time { return now }
	count, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if count != 2 || service.batch.Limit != 7 || !service.batch.ExpiredAt.Equal(now) {
		t.Fatalf("RunOnce() count = %d, batch = %+v", count, service.batch)
	}
	if observer.observation != (WorkerCycleObservation{
		Worker: WorkerResultDeadline, Claimed: 2, Succeeded: 2,
	}) {
		t.Fatalf("worker observation = %+v", observer.observation)
	}
}

type resultDeadlineServiceStub struct {
	batch tournament.ResultDeadlineBatch
	rooms []tournament.ExpiredRoom
}

func (service *resultDeadlineServiceStub) ExpireDue(_ context.Context, batch tournament.ResultDeadlineBatch) ([]tournament.ExpiredRoom, error) {
	service.batch = batch
	return service.rooms, nil
}
