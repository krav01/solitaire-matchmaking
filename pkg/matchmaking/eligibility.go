package matchmaking

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

// PlacementPredictor is the rating capability required to validate a complete
// room's pre-game win-probability spread.
type PlacementPredictor interface {
	Predict(rating.PredictionRequest) (rating.RoomPrediction, error)
}

// RejectionCode is a stable reason why a candidate cannot join a room.
type RejectionCode string

const (
	RejectionInvalidCandidate       RejectionCode = "invalid_candidate"
	RejectionRoomExpired            RejectionCode = "room_expired"
	RejectionRoomFull               RejectionCode = "room_full"
	RejectionDuplicateTicket        RejectionCode = "duplicate_ticket"
	RejectionDuplicatePlayer        RejectionCode = "duplicate_player"
	RejectionRatingModelMismatch    RejectionCode = "rating_model_mismatch"
	RejectionSkillGapExceeded       RejectionCode = "skill_gap_exceeded"
	RejectionProbabilityGapExceeded RejectionCode = "win_probability_spread_exceeded"
)

// CandidateDecision records eligibility without mutating the room or candidate.
// WinProbabilitySpread is present only when the candidate would complete a room.
type CandidateDecision struct {
	TicketID             string
	Eligible             bool
	Rejection            RejectionCode
	SkillGap             float64
	WinProbabilitySpread *float64
}

// FairnessReport compares a complete room against both hard policy limits.
type FairnessReport struct {
	SkillGap                    float64
	MaximumSkillGap             float64
	WinProbabilitySpread        float64
	MaximumWinProbabilitySpread float64
	WithinHardLimits            bool
}

// Evaluator applies hard eligibility and fairness rules. It does not rank
// eligible candidates; fill-speed optimization is a separate stage.
type Evaluator struct {
	predictor PlacementPredictor
}

func NewEvaluator(predictor PlacementPredictor) (*Evaluator, error) {
	if predictor == nil {
		return nil, errors.New("placement predictor is required")
	}
	return &Evaluator{predictor: predictor}, nil
}

// Filter evaluates candidates independently against the same immutable room
// view and preserves their input order.
func (e *Evaluator) Filter(room RoomView, candidates []Candidate, evaluatedAt time.Time) ([]CandidateDecision, error) {
	if err := room.Validate(); err != nil {
		return nil, err
	}
	if evaluatedAt.IsZero() || evaluatedAt.Before(room.CreatedAt) {
		return nil, errors.New("evaluation time must be at or after room creation")
	}
	if len(candidates) > room.Policy.CandidateLimit {
		return nil, errors.New("candidate batch exceeds the policy scan limit")
	}

	decisions := make([]CandidateDecision, len(candidates))
	for index, candidate := range candidates {
		decision, err := e.evaluateCandidate(room, candidate, evaluatedAt)
		if err != nil {
			return nil, fmt.Errorf("evaluate candidate %q: %w", candidate.TicketID, err)
		}
		decisions[index] = decision
	}
	return decisions, nil
}

func (e *Evaluator) evaluateCandidate(room RoomView, candidate Candidate, evaluatedAt time.Time) (CandidateDecision, error) {
	decision := CandidateDecision{TicketID: candidate.TicketID}
	candidateValid := candidate.Validate() == nil
	if !candidateValid || candidate.JoinedAt.After(evaluatedAt) || candidate.SnapshotAt.After(evaluatedAt) {
		decision.Rejection = RejectionInvalidCandidate
		return decision, nil
	}
	if evaluatedAt.After(room.Deadline) {
		decision.Rejection = RejectionRoomExpired
		return decision, nil
	}
	if len(room.Members) == room.Capacity {
		decision.Rejection = RejectionRoomFull
		return decision, nil
	}
	for _, member := range room.Members {
		if candidate.TicketID == member.TicketID {
			decision.Rejection = RejectionDuplicateTicket
			return decision, nil
		}
		if candidate.PlayerID == member.PlayerID {
			decision.Rejection = RejectionDuplicatePlayer
			return decision, nil
		}
	}
	if candidate.Rating.ModelVersion != room.Policy.RatingModelVersion {
		decision.Rejection = RejectionRatingModelMismatch
		return decision, nil
	}

	proposedMembers := make([]Candidate, 0, len(room.Members)+1)
	proposedMembers = append(proposedMembers, room.Members...)
	proposedMembers = append(proposedMembers, candidate)
	decision.SkillGap = skillGap(proposedMembers)
	if decision.SkillGap > room.Policy.MaxSkillGap {
		decision.Rejection = RejectionSkillGapExceeded
		return decision, nil
	}
	if len(proposedMembers) < room.Capacity {
		decision.Eligible = true
		return decision, nil
	}

	completeRoom := room
	completeRoom.Members = proposedMembers
	report, err := e.EvaluateFairness(completeRoom, evaluatedAt)
	if err != nil {
		return CandidateDecision{}, err
	}
	decision.SkillGap = report.SkillGap
	decision.WinProbabilitySpread = pointer(report.WinProbabilitySpread)
	if !report.WithinHardLimits {
		decision.Rejection = RejectionProbabilityGapExceeded
		return decision, nil
	}
	decision.Eligible = true
	return decision, nil
}

// EvaluateFairness validates both hard fairness constraints for a complete room.
func (e *Evaluator) EvaluateFairness(room RoomView, evaluatedAt time.Time) (FairnessReport, error) {
	if err := room.Validate(); err != nil {
		return FairnessReport{}, err
	}
	if len(room.Members) != room.Capacity {
		return FairnessReport{}, errors.New("fairness evaluation requires a complete room")
	}
	if evaluatedAt.IsZero() || evaluatedAt.Before(room.CreatedAt) || evaluatedAt.After(room.Deadline) {
		return FairnessReport{}, errors.New("fairness evaluation must occur during room formation")
	}

	estimates := make(map[string]rating.Estimate, len(room.Members))
	for _, member := range room.Members {
		if member.SnapshotAt.After(evaluatedAt) {
			return FairnessReport{}, fmt.Errorf("room member %q was snapshotted after evaluation", member.PlayerID)
		}
		estimates[member.PlayerID] = member.Rating
	}
	prediction, err := e.predictor.Predict(rating.PredictionRequest{
		RoomID:      room.RoomID,
		ModeID:      room.ModeID,
		GeneratedAt: evaluatedAt,
		Estimates:   estimates,
	})
	if err != nil {
		return FairnessReport{}, fmt.Errorf("predict complete room: %w", err)
	}
	spread, err := validatedProbabilitySpread(prediction, room, evaluatedAt)
	if err != nil {
		return FairnessReport{}, err
	}

	gap := skillGap(room.Members)
	return FairnessReport{
		SkillGap:                    gap,
		MaximumSkillGap:             room.Policy.MaxSkillGap,
		WinProbabilitySpread:        spread,
		MaximumWinProbabilitySpread: room.Policy.MaxWinProbabilitySpread,
		WithinHardLimits:            gap <= room.Policy.MaxSkillGap && spread <= room.Policy.MaxWinProbabilitySpread,
	}, nil
}

func validatedProbabilitySpread(prediction rating.RoomPrediction, room RoomView, evaluatedAt time.Time) (float64, error) {
	if prediction.RoomID != room.RoomID || prediction.ModeID != room.ModeID ||
		prediction.ModelVersion != room.Policy.RatingModelVersion ||
		!prediction.GeneratedAt.Equal(evaluatedAt) || len(prediction.Participants) != len(room.Members) {
		return 0, errors.New("placement prediction does not match the complete room")
	}
	members := make(map[string]struct{}, len(room.Members))
	for _, member := range room.Members {
		members[member.PlayerID] = struct{}{}
	}
	minimum := math.Inf(1)
	maximum := math.Inf(-1)
	total := 0.0
	seen := make(map[string]struct{}, len(prediction.Participants))
	for _, participant := range prediction.Participants {
		probability := participant.FirstPlaceProbability
		if _, exists := members[participant.PlayerID]; !exists || participant.PlayerID == "" {
			return 0, errors.New("placement prediction contains an unknown player")
		}
		if _, duplicate := seen[participant.PlayerID]; duplicate {
			return 0, errors.New("placement prediction contains a duplicate player")
		}
		if !finite(probability) || probability < 0 || probability > 1 {
			return 0, errors.New("first-place probabilities must be finite and between zero and one")
		}
		if len(participant.PlaceProbabilities) != len(room.Members) ||
			math.Abs(participant.PlaceProbabilities[0]-probability) > 1e-9 {
			return 0, errors.New("first-place probability must match the placement distribution")
		}
		seen[participant.PlayerID] = struct{}{}
		minimum = math.Min(minimum, probability)
		maximum = math.Max(maximum, probability)
		total += probability
	}
	if math.Abs(total-1) > 1e-9 {
		return 0, errors.New("first-place probabilities must sum to one")
	}
	return maximum - minimum, nil
}

func skillGap(candidates []Candidate) float64 {
	if len(candidates) < 2 {
		return 0
	}
	minimum := candidates[0].Rating.Mean
	maximum := minimum
	for _, candidate := range candidates[1:] {
		minimum = math.Min(minimum, candidate.Rating.Mean)
		maximum = math.Max(maximum, candidate.Rating.Mean)
	}
	return maximum - minimum
}

func pointer(value float64) *float64 {
	return &value
}
