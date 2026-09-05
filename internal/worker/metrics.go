package worker

import (
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
)

const (
	WorkerMatchmaking    = "matchmaking"
	WorkerResultDeadline = "result_deadline"
	WorkerRating         = "rating"
	WorkerOutbox         = "outbox"
)

type WorkerCycleObservation struct {
	Worker    string
	Claimed   int
	Succeeded int
	Failed    int
	Errored   bool
}

type WorkerObserver interface {
	ObserveWorkerCycle(WorkerCycleObservation)
}

type MatchObservation struct {
	Outcome                  matchmaking.AttemptOutcome
	ModeID                   string
	Capacity                 int
	PolicyVersion            string
	RatingModelVersion       string
	AssignmentLatency        time.Duration
	RoomFilled               bool
	RoomFillDuration         time.Duration
	FillTimeout              time.Duration
	SkillGap                 float64
	MaximumSkillGap          float64
	WinProbabilitySpread     *float64
	MaximumProbabilitySpread float64
}

type MatchObserver interface {
	ObserveMatch(MatchObservation)
}

type noopWorkerObserver struct{}

func (noopWorkerObserver) ObserveWorkerCycle(WorkerCycleObservation) {}

type noopMatchObserver struct{}

func (noopMatchObserver) ObserveMatch(MatchObservation) {}

func configuredWorkerObserver(observer WorkerObserver) WorkerObserver {
	if observer == nil {
		return noopWorkerObserver{}
	}

	return observer
}
