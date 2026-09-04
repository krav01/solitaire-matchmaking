package simulator

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

// PlacementPredictor is the pre-game prediction capability used by the runner.
type PlacementPredictor interface {
	Predict(rating.PredictionRequest) (rating.RoomPrediction, error)
}

// OutcomeGenerator produces a result only after the runner fixes room members.
type OutcomeGenerator interface {
	GenerateOutcome(OutcomeRequest) (rating.MatchResult, error)
}

// RunConfig defines one matching-policy experiment over a generated workload.
type RunConfig struct {
	Policy                  matchmaking.Policy `json:"policy"`
	ScoringRulesVersion     string             `json:"scoring_rules_version"`
	ResultDelay             time.Duration      `json:"result_delay"`
	ResultAvailabilityDelay time.Duration      `json:"result_availability_delay"`
	CalibrationBinCount     int                `json:"calibration_bin_count"`
}

// DefaultRunConfig provides explicit synthetic thresholds, not production SLOs.
func DefaultRunConfig(ratingModelVersion string) RunConfig {
	return RunConfig{
		Policy: matchmaking.Policy{
			Version:                 "matching-simulation-v1",
			RatingModelVersion:      ratingModelVersion,
			InitialSkillGap:         4,
			MaxSkillGap:             12,
			MaxWinProbabilitySpread: 0.35,
			ExpansionInterval:       5 * time.Second,
			FillTimeout:             30 * time.Second,
			AgePriorityAfter:        15 * time.Second,
			CandidateLimit:          100,
			RoomLimit:               100,
			PreferNearlyFull:        true,
		},
		ScoringRulesVersion:     "scoring-simulation-v1",
		ResultDelay:             time.Minute,
		ResultAvailabilityDelay: time.Second,
		CalibrationBinCount:     10,
	}
}

// Validate checks runner bounds independently from workload generation.
func (c RunConfig) Validate() error {
	if err := c.Policy.Validate(); err != nil {
		return fmt.Errorf("simulation matching policy: %w", err)
	}
	if c.ScoringRulesVersion == "" {
		return errors.New("simulation scoring rules version is required")
	}
	if c.ResultDelay <= 0 || c.ResultAvailabilityDelay < 0 {
		return errors.New("result delay must be positive and availability delay non-negative")
	}
	if c.CalibrationBinCount < 2 || c.CalibrationBinCount > 100 {
		return errors.New("calibration bin count must be between two and one hundred")
	}
	return nil
}

// Runner applies the production matching package to one synthetic workload.
type Runner struct {
	predictor PlacementPredictor
	outcomes  OutcomeGenerator
	config    RunConfig
}

// NewRunner validates and wires the side-effect-free simulation dependencies.
func NewRunner(predictor PlacementPredictor, outcomes OutcomeGenerator, config RunConfig) (*Runner, error) {
	if predictor == nil || outcomes == nil {
		return nil, errors.New("simulation predictor and outcome generator are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Runner{predictor: predictor, outcomes: outcomes, config: config}, nil
}

type simulatedRoom struct {
	view        matchmaking.RoomView
	partitionID string
	arrivals    []Arrival
	filledAt    *time.Time
	timedOut    bool
	prediction  rating.RoomPrediction
	result      rating.MatchResult
}

// Run forms rooms in event-time order and reports resolved rooms after the last
// deadline. Submitted outcomes are never visible to later matching decisions.
func (r *Runner) Run(workload Workload) (Report, error) {
	partitions, err := validateWorkload(workload, r.config.Policy.RatingModelVersion)
	if err != nil {
		return Report{}, err
	}
	evaluator, err := matchmaking.NewEvaluator(r.predictor)
	if err != nil {
		return Report{}, err
	}

	rooms := make([]*simulatedRoom, 0)
	roomsByID := make(map[string]*simulatedRoom)
	activeByPartition := make(map[string][]*simulatedRoom, len(partitions))
	for _, arrival := range workload.Arrivals {
		partition := partitions[arrival.PartitionID]
		active := activeRoomsAt(activeByPartition[partition.ID], arrival.ArrivedAt)
		activeByPartition[partition.ID] = active
		candidate := candidateFromArrival(arrival)
		views, err := r.candidateRooms(active, arrival.ArrivedAt)
		if err != nil {
			return Report{}, fmt.Errorf("select candidate rooms for ticket %q: %w", arrival.TicketID, err)
		}
		attempt, err := evaluator.AttemptMatch(matchmaking.MatchAttempt{
			Trigger:     matchmaking.AttemptTriggerTicketAccepted,
			EvaluatedAt: arrival.ArrivedAt,
			Candidate:   candidate,
			Policy:      r.config.Policy,
			Rooms:       views,
		})
		if err != nil {
			return Report{}, fmt.Errorf("match ticket %q: %w", arrival.TicketID, err)
		}

		switch attempt.Outcome {
		case matchmaking.AttemptOutcomeRetryScheduled:
			room := r.openRoom(len(rooms)+1, partition, candidate, arrival)
			rooms = append(rooms, room)
			roomsByID[room.view.RoomID] = room
			activeByPartition[partition.ID] = append(active, room)
			continue
		case matchmaking.AttemptOutcomeMatched:
			// Continue with the selected room below.
		case matchmaking.AttemptOutcomeTimedOut:
			return Report{}, fmt.Errorf("new ticket %q timed out at arrival", arrival.TicketID)
		default:
			return Report{}, fmt.Errorf("match ticket %q returned unexpected outcome %q", arrival.TicketID, attempt.Outcome)
		}
		if attempt.Selection == nil {
			return Report{}, fmt.Errorf("match ticket %q returned no selected room", arrival.TicketID)
		}
		room, found := roomsByID[attempt.Selection.RoomID]
		if !found || room.filledAt != nil || room.timedOut {
			return Report{}, fmt.Errorf("match ticket %q selected an inactive room", arrival.TicketID)
		}
		room.view.Members = append(room.view.Members, candidate)
		room.arrivals = append(room.arrivals, arrival)
		if len(room.view.Members) == room.view.Capacity {
			if err := r.completeRoom(room, arrival.ArrivedAt); err != nil {
				return Report{}, err
			}
		}
	}

	for _, room := range rooms {
		if room.filledAt == nil {
			room.timedOut = true
		}
	}
	return buildReport(workload, r.config, rooms)
}

func validateWorkload(workload Workload, ratingModelVersion string) (map[string]TournamentPartition, error) {
	if err := workload.Config.Validate(); err != nil {
		return nil, fmt.Errorf("simulation workload config: %w", err)
	}
	if workload.Config.RatingModelVersion != ratingModelVersion {
		return nil, errors.New("workload and matching policy use different rating models")
	}
	if len(workload.Arrivals) != workload.Config.TicketCount {
		return nil, errors.New("workload arrival count does not match its configuration")
	}
	partitions := make(map[string]TournamentPartition, len(workload.Config.TournamentPartitions))
	for _, partition := range workload.Config.TournamentPartitions {
		partitions[partition.ID] = partition
	}
	tickets := make(map[string]struct{}, len(workload.Arrivals))
	players := make(map[string]struct{}, len(workload.Arrivals))
	previous := workload.Config.StartedAt
	for index, arrival := range workload.Arrivals {
		if arrival.TicketID == "" || arrival.PlayerID == "" {
			return nil, fmt.Errorf("workload arrival %d requires ticket and player ids", index)
		}
		if (index > 0 && !arrival.ArrivedAt.After(previous)) || (index == 0 && arrival.ArrivedAt.Before(previous)) {
			return nil, fmt.Errorf("workload arrival %d is outside strict event-time order", index)
		}
		previous = arrival.ArrivedAt
		if !arrival.SnapshotAt.Equal(arrival.ArrivedAt) {
			return nil, fmt.Errorf("workload arrival %d requires a pre-game snapshot at arrival", index)
		}
		if !finite(arrival.LatentSkill) {
			return nil, fmt.Errorf("workload arrival %d has non-finite latent skill", index)
		}
		if err := arrival.RatingSnapshot.Validate(); err != nil {
			return nil, fmt.Errorf("workload arrival %d rating: %w", index, err)
		}
		if arrival.RatingSnapshot.ModelVersion != ratingModelVersion || arrival.RatingSnapshot.UpdatedAt.After(arrival.SnapshotAt) {
			return nil, fmt.Errorf("workload arrival %d has an incompatible rating snapshot", index)
		}
		if _, found := partitions[arrival.PartitionID]; !found {
			return nil, fmt.Errorf("workload arrival %d uses unknown partition %q", index, arrival.PartitionID)
		}
		if _, duplicate := tickets[arrival.TicketID]; duplicate {
			return nil, fmt.Errorf("workload contains duplicate ticket %q", arrival.TicketID)
		}
		if _, duplicate := players[arrival.PlayerID]; duplicate {
			return nil, fmt.Errorf("workload contains duplicate player %q", arrival.PlayerID)
		}
		tickets[arrival.TicketID] = struct{}{}
		players[arrival.PlayerID] = struct{}{}
	}
	return partitions, nil
}

func activeRoomsAt(rooms []*simulatedRoom, evaluatedAt time.Time) []*simulatedRoom {
	active := make([]*simulatedRoom, 0, len(rooms))
	for _, room := range rooms {
		if room.filledAt != nil {
			continue
		}
		if evaluatedAt.After(room.view.Deadline) {
			room.timedOut = true
			continue
		}
		active = append(active, room)
	}
	return active
}

func candidateFromArrival(arrival Arrival) matchmaking.Candidate {
	return matchmaking.Candidate{
		TicketID:   arrival.TicketID,
		PlayerID:   arrival.PlayerID,
		JoinedAt:   arrival.ArrivedAt,
		SnapshotAt: arrival.SnapshotAt,
		Rating:     arrival.RatingSnapshot,
	}
}

func (r *Runner) candidateRooms(rooms []*simulatedRoom, evaluatedAt time.Time) ([]matchmaking.RoomView, error) {
	views := make([]matchmaking.RoomView, 0)
	for _, room := range rooms {
		views = append(views, room.view)
	}
	ordered, err := matchmaking.PrioritizeWaitingRooms(views, evaluatedAt)
	if err != nil {
		return nil, err
	}
	return slices.Clone(ordered[:min(len(ordered), r.config.Policy.RoomLimit)]), nil
}

func (r *Runner) openRoom(sequence int, partition TournamentPartition, candidate matchmaking.Candidate, arrival Arrival) *simulatedRoom {
	return &simulatedRoom{
		view: matchmaking.RoomView{
			RoomID:    fmt.Sprintf("room-%06d", sequence),
			ModeID:    partition.ModeID,
			Capacity:  partition.Capacity,
			CreatedAt: arrival.ArrivedAt,
			Deadline:  arrival.ArrivedAt.Add(r.config.Policy.FillTimeout),
			Policy:    r.config.Policy,
			Members:   []matchmaking.Candidate{candidate},
		},
		partitionID: partition.ID,
		arrivals:    []Arrival{arrival},
	}
}

func (r *Runner) completeRoom(room *simulatedRoom, filledAt time.Time) error {
	estimates := make(map[string]rating.Estimate, len(room.view.Members))
	participants := make([]OutcomeParticipant, len(room.arrivals))
	for index, arrival := range room.arrivals {
		estimates[arrival.PlayerID] = arrival.RatingSnapshot
		participants[index] = OutcomeParticipant{PlayerID: arrival.PlayerID, LatentSkill: arrival.LatentSkill}
	}
	prediction, err := r.predictor.Predict(rating.PredictionRequest{
		RoomID:      room.view.RoomID,
		ModeID:      room.view.ModeID,
		GeneratedAt: filledAt,
		Estimates:   estimates,
	})
	if err != nil {
		return fmt.Errorf("predict completed room %q: %w", room.view.RoomID, err)
	}
	finishedAt := filledAt.Add(r.config.ResultDelay)
	result, err := r.outcomes.GenerateOutcome(OutcomeRequest{
		EventID:             "result-" + room.view.RoomID,
		RoomID:              room.view.RoomID,
		PartitionID:         room.partitionID,
		DeckID:              "deck-" + room.view.RoomID,
		ScoringRulesVersion: r.config.ScoringRulesVersion,
		FinishedAt:          finishedAt,
		AvailableAt:         finishedAt.Add(r.config.ResultAvailabilityDelay),
		Participants:        participants,
	})
	if err != nil {
		return fmt.Errorf("generate completed room %q outcome: %w", room.view.RoomID, err)
	}
	room.filledAt = timePointer(filledAt)
	room.prediction = prediction
	room.result = result
	return nil
}

func timePointer(value time.Time) *time.Time {
	return &value
}
