// Package simulator generates deterministic matchmaking workloads and outcomes.
package simulator

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

const maxTicketCount = 1_000_000

// TournamentPartition is one queue that can form compatible rooms.
type TournamentPartition struct {
	ID            string `json:"id"`
	ModeID        string `json:"mode_id"`
	Capacity      int    `json:"capacity"`
	EntryFeeMinor int64  `json:"entry_fee_minor"`
	Currency      string `json:"currency"`
	Region        string `json:"region"`
	TrafficWeight uint64 `json:"traffic_weight"`
}

// Config fully describes one reproducible workload. Rating uncertainty and
// performance deviation are intentionally separate simulation dimensions.
type Config struct {
	Seed                 uint64                `json:"seed"`
	StartedAt            time.Time             `json:"started_at"`
	TicketCount          int                   `json:"ticket_count"`
	ArrivalRatePerSecond float64               `json:"arrival_rate_per_second"`
	SkillMean            float64               `json:"skill_mean"`
	SkillDeviation       float64               `json:"skill_deviation"`
	RatingUncertainty    float64               `json:"rating_uncertainty"`
	PerformanceDeviation float64               `json:"performance_deviation"`
	RatingModelVersion   string                `json:"rating_model_version"`
	TournamentPartitions []TournamentPartition `json:"tournament_partitions"`
}

// DefaultConfig provides an explicit, non-production scenario. Start time and
// seed are supplied by the caller so the output never depends on wall time.
func DefaultConfig(startedAt time.Time, seed uint64) Config {
	return Config{
		Seed:                 seed,
		StartedAt:            startedAt,
		TicketCount:          300,
		ArrivalRatePerSecond: 5,
		SkillMean:            25,
		SkillDeviation:       25.0 / 3.0,
		RatingUncertainty:    5,
		PerformanceDeviation: 25.0 / 6.0,
		RatingModelVersion:   "rating-v1",
		TournamentPartitions: []TournamentPartition{
			{ID: "classic-5-free-eu", ModeID: "classic", Capacity: 5, Currency: "EUR", Region: "eu", TrafficWeight: 50},
			{ID: "classic-6-paid-eu", ModeID: "classic", Capacity: 6, EntryFeeMinor: 100, Currency: "EUR", Region: "eu", TrafficWeight: 30},
			{ID: "draw-three-7-paid-eu", ModeID: "draw-three", Capacity: 7, EntryFeeMinor: 500, Currency: "EUR", Region: "eu", TrafficWeight: 20},
		},
	}
}

// Validate rejects invalid or unbounded scenarios before allocating output.
func (c Config) Validate() error {
	if c.StartedAt.IsZero() {
		return errors.New("simulation start time is required")
	}
	if c.TicketCount <= 0 || c.TicketCount > maxTicketCount {
		return fmt.Errorf("ticket count must be between one and %d", maxTicketCount)
	}
	if !positiveFinite(c.ArrivalRatePerSecond) {
		return errors.New("arrival rate must be positive and finite")
	}
	if !finite(c.SkillMean) || !nonNegativeFinite(c.SkillDeviation) {
		return errors.New("skill distribution must be finite with non-negative deviation")
	}
	if !positiveFinite(c.RatingUncertainty) || !nonNegativeFinite(c.PerformanceDeviation) {
		return errors.New("rating uncertainty must be positive and performance deviation non-negative")
	}
	if c.RatingModelVersion == "" {
		return errors.New("rating model version is required")
	}
	if len(c.TournamentPartitions) == 0 {
		return errors.New("at least one tournament partition is required")
	}

	seen := make(map[string]struct{}, len(c.TournamentPartitions))
	totalWeight := uint64(0)
	for index, partition := range c.TournamentPartitions {
		if partition.ID == "" || partition.ModeID == "" || partition.Currency == "" || partition.Region == "" {
			return fmt.Errorf("tournament partition %d requires identity, mode, currency and region", index)
		}
		if partition.Capacity < rating.MinRoomSize || partition.Capacity > rating.MaxRoomSize {
			return fmt.Errorf("tournament partition %q has unsupported capacity", partition.ID)
		}
		if partition.EntryFeeMinor < 0 || partition.TrafficWeight == 0 {
			return fmt.Errorf("tournament partition %q has invalid fee or traffic weight", partition.ID)
		}
		if _, duplicate := seen[partition.ID]; duplicate {
			return fmt.Errorf("duplicate tournament partition %q", partition.ID)
		}
		seen[partition.ID] = struct{}{}
		if partition.TrafficWeight > math.MaxUint64-totalWeight {
			return errors.New("tournament partition traffic weights overflow")
		}
		totalWeight += partition.TrafficWeight
	}
	return nil
}

// Arrival is one immutable pre-game player snapshot. LatentSkill is available
// only to the simulator and must never be passed into matchmaking.
type Arrival struct {
	TicketID       string          `json:"ticket_id"`
	PlayerID       string          `json:"player_id"`
	PartitionID    string          `json:"partition_id"`
	ArrivedAt      time.Time       `json:"arrived_at"`
	SnapshotAt     time.Time       `json:"snapshot_at"`
	LatentSkill    float64         `json:"latent_skill"`
	RatingSnapshot rating.Estimate `json:"rating_snapshot"`
}

// Workload is the replayable arrival stream produced from one configuration.
type Workload struct {
	Config   Config    `json:"config"`
	Arrivals []Arrival `json:"arrivals"`
}

// OutcomeParticipant supplies simulator-only ground truth after room membership
// has already been decided.
type OutcomeParticipant struct {
	PlayerID    string  `json:"player_id"`
	LatentSkill float64 `json:"latent_skill"`
}

// OutcomeRequest describes a completed synthetic room. The generator derives
// player noise from stable identifiers, making results independent of call and
// participant order.
type OutcomeRequest struct {
	EventID             string               `json:"event_id"`
	RoomID              string               `json:"room_id"`
	PartitionID         string               `json:"partition_id"`
	DeckID              string               `json:"deck_id"`
	ScoringRulesVersion string               `json:"scoring_rules_version"`
	FinishedAt          time.Time            `json:"finished_at"`
	AvailableAt         time.Time            `json:"available_at"`
	Participants        []OutcomeParticipant `json:"participants"`
}

// Generator is immutable and safe for deterministic replay.
type Generator struct {
	config Config
}

// New validates and copies the supplied configuration.
func New(config Config) (*Generator, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	config.TournamentPartitions = slices.Clone(config.TournamentPartitions)
	return &Generator{config: config}, nil
}

// GenerateWorkload returns the same arrival stream for the same configuration.
func (g *Generator) GenerateWorkload() (Workload, error) {
	random := newRandom(g.config.Seed)
	arrivals := make([]Arrival, g.config.TicketCount)
	arrivedAt := g.config.StartedAt
	totalWeight := partitionWeight(g.config.TournamentPartitions)

	for index := range arrivals {
		delay, err := arrivalDelay(random.uniform(), g.config.ArrivalRatePerSecond)
		if err != nil {
			return Workload{}, fmt.Errorf("generate arrival %d: %w", index+1, err)
		}
		arrivedAt = arrivedAt.Add(delay)
		partition := choosePartition(g.config.TournamentPartitions, random.bounded(totalWeight))
		latentSkill := g.config.SkillMean + g.config.SkillDeviation*random.normal()
		ratingMean := latentSkill + g.config.RatingUncertainty*random.normal()
		if !finite(latentSkill) || !finite(ratingMean) {
			return Workload{}, fmt.Errorf("generate arrival %d: skill sample is not finite", index+1)
		}
		performanceDeviation := g.config.PerformanceDeviation
		arrivals[index] = Arrival{
			TicketID:    fmt.Sprintf("ticket-%06d", index+1),
			PlayerID:    fmt.Sprintf("player-%06d", index+1),
			PartitionID: partition.ID,
			ArrivedAt:   arrivedAt,
			SnapshotAt:  arrivedAt,
			LatentSkill: latentSkill,
			RatingSnapshot: rating.Estimate{
				Mean:                 ratingMean,
				Uncertainty:          g.config.RatingUncertainty,
				PerformanceDeviation: &performanceDeviation,
				ModelVersion:         g.config.RatingModelVersion,
				UpdatedAt:            arrivedAt,
			},
		}
	}

	config := g.config
	config.TournamentPartitions = slices.Clone(g.config.TournamentPartitions)
	return Workload{Config: config, Arrivals: arrivals}, nil
}

// GenerateOutcome ranks a known room using latent skill plus independent
// performance noise. It never reads or changes a matchmaking rating snapshot.
func (g *Generator) GenerateOutcome(request OutcomeRequest) (rating.MatchResult, error) {
	partition, found := findPartition(g.config.TournamentPartitions, request.PartitionID)
	if !found {
		return rating.MatchResult{}, fmt.Errorf("unknown tournament partition %q", request.PartitionID)
	}
	if len(request.Participants) != partition.Capacity {
		return rating.MatchResult{}, fmt.Errorf("outcome for partition %q requires %d participants", partition.ID, partition.Capacity)
	}

	type rankedParticipant struct {
		playerID    string
		performance float64
	}
	ranked := make([]rankedParticipant, len(request.Participants))
	seen := make(map[string]struct{}, len(request.Participants))
	for index, participant := range request.Participants {
		if participant.PlayerID == "" || !finite(participant.LatentSkill) {
			return rating.MatchResult{}, fmt.Errorf("outcome participant %d requires a player and finite latent skill", index)
		}
		if _, duplicate := seen[participant.PlayerID]; duplicate {
			return rating.MatchResult{}, fmt.Errorf("duplicate outcome player %q", participant.PlayerID)
		}
		seen[participant.PlayerID] = struct{}{}
		random := newRandom(derivedSeed(g.config.Seed, request.RoomID, request.DeckID, participant.PlayerID))
		performance := participant.LatentSkill + g.config.PerformanceDeviation*random.normal()
		if !finite(performance) {
			return rating.MatchResult{}, fmt.Errorf("outcome performance for player %q is not finite", participant.PlayerID)
		}
		ranked[index] = rankedParticipant{
			playerID:    participant.PlayerID,
			performance: performance,
		}
	}
	slices.SortFunc(ranked, func(left, right rankedParticipant) int {
		switch {
		case left.performance > right.performance:
			return -1
		case left.performance < right.performance:
			return 1
		case left.playerID < right.playerID:
			return -1
		case left.playerID > right.playerID:
			return 1
		default:
			return 0
		}
	})

	result := rating.MatchResult{
		EventID:             request.EventID,
		RoomID:              request.RoomID,
		ModeID:              partition.ModeID,
		DeckID:              request.DeckID,
		ScoringRulesVersion: request.ScoringRulesVersion,
		FinishedAt:          request.FinishedAt,
		AvailableAt:         request.AvailableAt,
		Participants:        make([]rating.ParticipantResult, len(ranked)),
	}
	for index, participant := range ranked {
		result.Participants[index] = rating.ParticipantResult{PlayerID: participant.playerID, Place: index + 1}
	}
	if err := result.Validate(); err != nil {
		return rating.MatchResult{}, fmt.Errorf("generated outcome: %w", err)
	}
	return result, nil
}

func arrivalDelay(uniform, rate float64) (time.Duration, error) {
	seconds := -math.Log1p(-uniform) / rate
	nanoseconds := seconds * float64(time.Second)
	if !finite(nanoseconds) || nanoseconds > float64(math.MaxInt64) {
		return 0, errors.New("arrival delay exceeds time duration bounds")
	}
	delay := time.Duration(math.Round(nanoseconds))
	if delay < time.Nanosecond {
		return time.Nanosecond, nil
	}
	return delay, nil
}

func choosePartition(partitions []TournamentPartition, target uint64) TournamentPartition {
	for _, partition := range partitions {
		if target < partition.TrafficWeight {
			return partition
		}
		target -= partition.TrafficWeight
	}
	return partitions[len(partitions)-1]
}

func partitionWeight(partitions []TournamentPartition) uint64 {
	total := uint64(0)
	for _, partition := range partitions {
		total += partition.TrafficWeight
	}
	return total
}

func findPartition(partitions []TournamentPartition, partitionID string) (TournamentPartition, bool) {
	for _, partition := range partitions {
		if partition.ID == partitionID {
			return partition, true
		}
	}
	return TournamentPartition{}, false
}

func derivedSeed(seed uint64, values ...string) uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	hash := offset
	for shift := 0; shift < 64; shift += 8 {
		hash ^= (seed >> shift) & 0xff
		hash *= prime
	}
	for _, value := range values {
		for index := 0; index < len(value); index++ {
			hash ^= uint64(value[index])
			hash *= prime
		}
		hash ^= 0xff
		hash *= prime
	}
	return hash
}

type randomSource struct {
	state uint64
}

func newRandom(seed uint64) *randomSource {
	return &randomSource{state: seed}
}

func (r *randomSource) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	value := r.state
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func (r *randomSource) uniform() float64 {
	return (float64(r.next()>>11) + 0.5) / (1 << 53)
}

func (r *randomSource) bounded(limit uint64) uint64 {
	threshold := -limit % limit
	for {
		value := r.next()
		if value >= threshold {
			return value % limit
		}
	}
}

func (r *randomSource) normal() float64 {
	radius := math.Sqrt(-2 * math.Log(r.uniform()))
	angle := 2 * math.Pi * r.uniform()
	return radius * math.Cos(angle)
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func positiveFinite(value float64) bool {
	return value > 0 && finite(value)
}

func nonNegativeFinite(value float64) bool {
	return value >= 0 && finite(value)
}
