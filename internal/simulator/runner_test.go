package simulator_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/simulator"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestRunnerProducesReproducibleBoundedReport(t *testing.T) {
	t.Parallel()
	config := simulator.DefaultConfig(time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC), 71)
	config.TicketCount = 300
	generator, workload := generateWorkload(t, config)
	runConfig := simulator.DefaultRunConfig(config.RatingModelVersion)
	runner := newRunner(t, generator, runConfig)

	first, err := runner.Run(workload)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	unchanged, err := generator.GenerateWorkload()
	if err != nil {
		t.Fatalf("replay GenerateWorkload() error = %v", err)
	}
	if !reflect.DeepEqual(workload, unchanged) {
		t.Fatal("Run() mutated the workload")
	}
	second, err := runner.Run(workload)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Run() changed report for the same workload and policy")
	}
	if first.Overall.Tickets != config.TicketCount {
		t.Fatalf("report tickets = %d, want %d", first.Overall.Tickets, config.TicketCount)
	}
	if first.Overall.RoomsOpened != first.Overall.RoomsFilled+first.Overall.RoomsTimedOut {
		t.Fatalf("room accounting is incomplete: %+v", first.Overall)
	}
	if first.Overall.Tickets != first.Overall.TicketsInFilledRooms+first.Overall.TicketsInTimedOutRooms {
		t.Fatalf("ticket accounting is incomplete: %+v", first.Overall)
	}
	if first.Overall.RoomsFilled == 0 || first.Overall.RoomsTimedOut == 0 {
		t.Fatalf("test scenario did not exercise both outcomes: %+v", first.Overall)
	}
	if first.Overall.Fairness.SkillGap.Max > runConfig.Policy.MaxSkillGap {
		t.Fatalf("reported skill gap %f exceeds hard maximum %f", first.Overall.Fairness.SkillGap.Max, runConfig.Policy.MaxSkillGap)
	}
	if first.Overall.Fairness.WinProbabilitySpread.Max > runConfig.Policy.MaxWinProbabilitySpread {
		t.Fatalf("reported win spread %f exceeds hard maximum %f", first.Overall.Fairness.WinProbabilitySpread.Max, runConfig.Policy.MaxWinProbabilitySpread)
	}

	partitionTickets := 0
	partitionRooms := 0
	for _, partition := range first.Partitions {
		partitionTickets += partition.Metrics.Tickets
		partitionRooms += partition.Metrics.RoomsOpened
	}
	if partitionTickets != first.Overall.Tickets || partitionRooms != first.Overall.RoomsOpened {
		t.Fatalf("partition totals tickets=%d rooms=%d do not match overall report", partitionTickets, partitionRooms)
	}
}

func TestRunnerKeepsTimedOutTicketsInLatencyDistributions(t *testing.T) {
	t.Parallel()
	config := testConfig()
	config.TicketCount = 1
	generator, workload := generateWorkload(t, config)
	runConfig := simulator.DefaultRunConfig(config.RatingModelVersion)
	runConfig.Policy.FillTimeout = 12 * time.Second
	runConfig.Policy.ExpansionInterval = 3 * time.Second
	runConfig.Policy.AgePriorityAfter = 6 * time.Second
	report, err := newRunner(t, generator, runConfig).Run(workload)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if report.Overall.RoomsOpened != 1 || report.Overall.RoomsFilled != 0 || report.Overall.RoomsTimedOut != 1 || report.Overall.FillTimeoutRate != 1 {
		t.Fatalf("timeout metrics = %+v, want one timed-out room", report.Overall)
	}
	wantMilliseconds := float64(runConfig.Policy.FillTimeout) / float64(time.Millisecond)
	if report.Overall.PlayerWaitMilliseconds.Count != 1 || report.Overall.PlayerWaitMilliseconds.P50 != wantMilliseconds {
		t.Fatalf("player wait = %+v, want %f ms", report.Overall.PlayerWaitMilliseconds, wantMilliseconds)
	}
	if report.Overall.RoomResolutionMilliseconds.Count != 1 || report.Overall.RoomResolutionMilliseconds.P50 != wantMilliseconds {
		t.Fatalf("room resolution = %+v, want %f ms", report.Overall.RoomResolutionMilliseconds, wantMilliseconds)
	}
	if report.Overall.FilledRoomDurationMilliseconds.Count != 0 || report.Overall.Fairness.SkillGap.Count != 0 {
		t.Fatal("timed-out room was included in completed-room metrics")
	}
}

func TestRunnerReportsCompletedRoomFairnessAndCalibration(t *testing.T) {
	t.Parallel()
	config := testConfig()
	config.TicketCount = 5
	config.ArrivalRatePerSecond = 100
	config.SkillDeviation = 0
	config.RatingUncertainty = 1
	config.PerformanceDeviation = 0
	generator, workload := generateWorkload(t, config)
	runConfig := simulator.DefaultRunConfig(config.RatingModelVersion)
	runConfig.Policy.InitialSkillGap = 100
	runConfig.Policy.MaxSkillGap = 100
	runConfig.Policy.MaxWinProbabilitySpread = 1
	report, err := newRunner(t, generator, runConfig).Run(workload)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if report.Overall.RoomsFilled != 1 || report.Overall.RoomsTimedOut != 0 {
		t.Fatalf("completed-room metrics = %+v, want one filled room", report.Overall)
	}
	if report.Overall.PlayerWaitMilliseconds.Count != 5 || report.Overall.Fairness.SkillGap.Count != 1 || report.Overall.Fairness.WinProbabilitySpread.Count != 1 {
		t.Fatalf("completed-room distributions are incomplete: %+v", report.Overall)
	}
	if report.Partitions[0].Calibration == nil || report.Partitions[0].Calibration.Rooms != 1 {
		t.Fatalf("calibration = %+v, want one synthetic observation", report.Partitions[0].Calibration)
	}
}

func TestRunnerRejectsUnorderedArrivals(t *testing.T) {
	t.Parallel()
	config := testConfig()
	config.TicketCount = 2
	generator, workload := generateWorkload(t, config)
	workload.Arrivals[1].ArrivedAt = workload.Arrivals[0].ArrivedAt
	workload.Arrivals[1].SnapshotAt = workload.Arrivals[1].ArrivedAt
	workload.Arrivals[1].RatingSnapshot.UpdatedAt = workload.Arrivals[1].SnapshotAt
	runConfig := simulator.DefaultRunConfig(config.RatingModelVersion)
	if _, err := newRunner(t, generator, runConfig).Run(workload); err == nil {
		t.Fatal("Run() error = nil for arrivals outside strict event-time order")
	}
}

func generateWorkload(t *testing.T, config simulator.Config) (*simulator.Generator, simulator.Workload) {
	t.Helper()
	generator, err := simulator.New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	workload, err := generator.GenerateWorkload()
	if err != nil {
		t.Fatalf("GenerateWorkload() error = %v", err)
	}
	return generator, workload
}

func newRunner(t *testing.T, generator *simulator.Generator, config simulator.RunConfig) *simulator.Runner {
	t.Helper()
	model, err := rating.NewBaseline(rating.DefaultBaselineConfig(config.Policy.RatingModelVersion))
	if err != nil {
		t.Fatalf("NewBaseline() error = %v", err)
	}
	runner, err := simulator.NewRunner(model, generator, config)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	return runner
}
