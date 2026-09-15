package simulator_test

import (
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/simulator"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func BenchmarkSimulation10000Tickets(b *testing.B) {
	config := simulator.DefaultConfig(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), 17)
	config.TicketCount = 10_000
	config.ArrivalRatePerSecond = 250
	generator, err := simulator.New(config)
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	workload, err := generator.GenerateWorkload()
	if err != nil {
		b.Fatalf("GenerateWorkload() error = %v", err)
	}
	model, err := rating.NewBaseline(rating.DefaultBaselineConfig(config.RatingModelVersion))
	if err != nil {
		b.Fatalf("NewBaseline() error = %v", err)
	}
	runner, err := simulator.NewRunner(model, generator, simulator.DefaultRunConfig(config.RatingModelVersion))
	if err != nil {
		b.Fatalf("NewRunner() error = %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		report, runErr := runner.Run(workload)
		if runErr != nil {
			b.Fatalf("Run() error = %v", runErr)
		}
		if report.Overall.Tickets != config.TicketCount {
			b.Fatalf("processed tickets = %d, want %d", report.Overall.Tickets, config.TicketCount)
		}
	}
	b.ReportMetric(float64(config.TicketCount*b.N)/b.Elapsed().Seconds(), "tickets/s")
}
