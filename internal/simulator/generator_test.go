package simulator_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/simulator"
)

func TestGenerateWorkloadIsReproducible(t *testing.T) {
	t.Parallel()
	config := testConfig()
	generator, err := simulator.New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := generator.GenerateWorkload()
	if err != nil {
		t.Fatalf("first GenerateWorkload() error = %v", err)
	}
	second, err := generator.GenerateWorkload()
	if err != nil {
		t.Fatalf("second GenerateWorkload() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("GenerateWorkload() changed output for the same configuration")
	}
	if len(first.Arrivals) != config.TicketCount {
		t.Fatalf("GenerateWorkload() produced %d arrivals, want %d", len(first.Arrivals), config.TicketCount)
	}
	for index, arrival := range first.Arrivals {
		if index > 0 && !arrival.ArrivedAt.After(first.Arrivals[index-1].ArrivedAt) {
			t.Fatalf("arrival %d is not strictly ordered", index)
		}
		if arrival.SnapshotAt != arrival.ArrivedAt || arrival.RatingSnapshot.ModelVersion != config.RatingModelVersion || arrival.RatingSnapshot.UpdatedAt != arrival.SnapshotAt {
			t.Fatalf("arrival %d has an invalid rating snapshot: %+v", index, arrival.RatingSnapshot)
		}
		if arrival.RatingSnapshot.PerformanceDeviation == nil || *arrival.RatingSnapshot.PerformanceDeviation != config.PerformanceDeviation {
			t.Fatalf("arrival %d lost performance deviation", index)
		}
	}
}

func TestGenerateWorkloadChangesWithSeed(t *testing.T) {
	t.Parallel()
	firstConfig := testConfig()
	secondConfig := firstConfig
	secondConfig.Seed++
	firstGenerator, err := simulator.New(firstConfig)
	if err != nil {
		t.Fatalf("first New() error = %v", err)
	}
	secondGenerator, err := simulator.New(secondConfig)
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}
	first, err := firstGenerator.GenerateWorkload()
	if err != nil {
		t.Fatalf("first GenerateWorkload() error = %v", err)
	}
	second, err := secondGenerator.GenerateWorkload()
	if err != nil {
		t.Fatalf("second GenerateWorkload() error = %v", err)
	}
	if reflect.DeepEqual(first.Arrivals, second.Arrivals) {
		t.Fatal("different seeds produced the same arrival stream")
	}
}

func TestGenerateOutcomeIsStableAndIndependentOfInputOrder(t *testing.T) {
	t.Parallel()
	config := testConfig()
	generator, err := simulator.New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := simulator.OutcomeRequest{
		EventID:             "event-1",
		RoomID:              "room-1",
		PartitionID:         config.TournamentPartitions[0].ID,
		DeckID:              "deck-1",
		ScoringRulesVersion: "scoring-v1",
		FinishedAt:          config.StartedAt.Add(time.Minute),
		AvailableAt:         config.StartedAt.Add(time.Minute + time.Second),
		Participants: []simulator.OutcomeParticipant{
			{PlayerID: "player-3", LatentSkill: 30},
			{PlayerID: "player-1", LatentSkill: 50},
			{PlayerID: "player-5", LatentSkill: 10},
			{PlayerID: "player-2", LatentSkill: 40},
			{PlayerID: "player-4", LatentSkill: 20},
		},
	}

	first, err := generator.GenerateOutcome(request)
	if err != nil {
		t.Fatalf("first GenerateOutcome() error = %v", err)
	}
	slices.Reverse(request.Participants)
	second, err := generator.GenerateOutcome(request)
	if err != nil {
		t.Fatalf("second GenerateOutcome() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("GenerateOutcome() depends on participant input order")
	}
	if first.Participants[0].PlayerID != "player-1" || first.Participants[0].Place != 1 {
		t.Fatalf("GenerateOutcome() winner = %+v, want strongest player", first.Participants[0])
	}
}

func TestGenerateOutcomeRejectsInvalidMembership(t *testing.T) {
	t.Parallel()
	config := testConfig()
	generator, err := simulator.New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := simulator.OutcomeRequest{
		EventID:             "event-1",
		RoomID:              "room-1",
		PartitionID:         config.TournamentPartitions[0].ID,
		DeckID:              "deck-1",
		ScoringRulesVersion: "scoring-v1",
		FinishedAt:          config.StartedAt.Add(time.Minute),
		AvailableAt:         config.StartedAt.Add(time.Minute),
		Participants: []simulator.OutcomeParticipant{
			{PlayerID: "player-1", LatentSkill: 25},
		},
	}
	if _, err := generator.GenerateOutcome(request); err == nil {
		t.Fatal("GenerateOutcome() error = nil for an incomplete room")
	}

	request.Participants = make([]simulator.OutcomeParticipant, 5)
	for index := range request.Participants {
		request.Participants[index] = simulator.OutcomeParticipant{PlayerID: "player", LatentSkill: float64(index)}
	}
	if _, err := generator.GenerateOutcome(request); err == nil {
		t.Fatal("GenerateOutcome() error = nil for duplicate players")
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	valid := testConfig()
	tests := []struct {
		name   string
		change func(*simulator.Config)
	}{
		{name: "zero tickets", change: func(config *simulator.Config) { config.TicketCount = 0 }},
		{name: "invalid arrival rate", change: func(config *simulator.Config) { config.ArrivalRatePerSecond = 0 }},
		{name: "no partitions", change: func(config *simulator.Config) { config.TournamentPartitions = nil }},
		{name: "duplicate partition", change: func(config *simulator.Config) {
			config.TournamentPartitions = append(config.TournamentPartitions, config.TournamentPartitions[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			config.TournamentPartitions = slices.Clone(valid.TournamentPartitions)
			test.change(&config)
			if _, err := simulator.New(config); err == nil {
				t.Fatal("New() error = nil, want invalid configuration error")
			}
		})
	}
}

func testConfig() simulator.Config {
	config := simulator.DefaultConfig(time.Date(2026, time.September, 4, 9, 0, 0, 0, time.UTC), 42)
	config.TicketCount = 20
	config.TournamentPartitions = config.TournamentPartitions[:1]
	return config
}
