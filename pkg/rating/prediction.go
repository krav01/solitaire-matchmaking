package rating

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"slices"
	"time"
)

const maximumLogitRange = 700.0

// PredictionRequest contains only information available before a room result.
type PredictionRequest struct {
	RoomID      string              `json:"room_id"`
	ModeID      string              `json:"mode_id"`
	GeneratedAt time.Time           `json:"generated_at"`
	Estimates   map[string]Estimate `json:"estimates"`
}

// PlacementPrediction is one player's probability distribution over all places.
type PlacementPrediction struct {
	PlayerID              string    `json:"player_id"`
	PlaceProbabilities    []float64 `json:"place_probabilities"`
	FirstPlaceProbability float64   `json:"first_place_probability"`
	ExpectedPlace         float64   `json:"expected_place"`
}

// RoomPrediction is an immutable pre-game snapshot used by matchmaking and
// later calibration. Participants are sorted by player id.
type RoomPrediction struct {
	RoomID       string                `json:"room_id"`
	ModeID       string                `json:"mode_id"`
	ModelVersion string                `json:"model_version"`
	GeneratedAt  time.Time             `json:"generated_at"`
	Participants []PlacementPrediction `json:"participants"`
}

// Predict returns a coherent Plackett-Luce placement distribution. Skill and
// performance variance determine a shared room scale, so uncertainty flattens
// predictions without being treated as lower skill.
func (b *Baseline) Predict(request PredictionRequest) (RoomPrediction, error) {
	if request.RoomID == "" || request.ModeID == "" || request.GeneratedAt.IsZero() {
		return RoomPrediction{}, errors.New("prediction room, mode and generation time are required")
	}
	if len(request.Estimates) < MinRoomSize || len(request.Estimates) > MaxRoomSize {
		return RoomPrediction{}, fmt.Errorf("prediction requires %d to %d players", MinRoomSize, MaxRoomSize)
	}

	playerIDs := make([]string, 0, len(request.Estimates))
	estimates := make([]Estimate, 0, len(request.Estimates))
	for playerID := range request.Estimates {
		playerIDs = append(playerIDs, playerID)
	}
	slices.Sort(playerIDs)
	for _, playerID := range playerIDs {
		estimate := request.Estimates[playerID]
		if playerID == "" {
			return RoomPrediction{}, errors.New("prediction player id is required")
		}
		if err := b.validateEstimate(playerID, estimate); err != nil {
			return RoomPrediction{}, err
		}
		if estimate.UpdatedAt.After(request.GeneratedAt) {
			return RoomPrediction{}, fmt.Errorf("pre-game rating for player %q was updated after prediction time", playerID)
		}
		estimates = append(estimates, estimate)
	}

	weights, err := b.placementWeights(estimates)
	if err != nil {
		return RoomPrediction{}, err
	}
	distributions := plackettLuceDistributions(weights)
	prediction := RoomPrediction{
		RoomID:       request.RoomID,
		ModeID:       request.ModeID,
		ModelVersion: b.config.Version,
		GeneratedAt:  request.GeneratedAt,
		Participants: make([]PlacementPrediction, len(playerIDs)),
	}
	for playerIndex, playerID := range playerIDs {
		placeProbabilities := distributions[playerIndex]
		expectedPlace := 0.0
		for placeIndex, probability := range placeProbabilities {
			expectedPlace += float64(placeIndex+1) * probability
		}
		prediction.Participants[playerIndex] = PlacementPrediction{
			PlayerID:              playerID,
			PlaceProbabilities:    placeProbabilities,
			FirstPlaceProbability: placeProbabilities[0],
			ExpectedPlace:         expectedPlace,
		}
	}
	return prediction, nil
}

func (b *Baseline) placementWeights(estimates []Estimate) ([]float64, error) {
	minimumMean := estimates[0].Mean
	maximumMean := estimates[0].Mean
	varianceNorm := 0.0
	for _, estimate := range estimates {
		minimumMean = math.Min(minimumMean, estimate.Mean)
		maximumMean = math.Max(maximumMean, estimate.Mean)
		deviation := b.config.DefaultPerformanceDeviation
		if estimate.PerformanceDeviation != nil {
			deviation = *estimate.PerformanceDeviation
		}
		varianceNorm = math.Hypot(varianceNorm, math.Hypot(estimate.Uncertainty, deviation))
	}
	roomScale := math.Sqrt(2) * varianceNorm / math.Sqrt(float64(len(estimates)))
	if !positiveFinite(roomScale) {
		return nil, errors.New("prediction room scale must be positive and finite")
	}

	center := minimumMean/2 + maximumMean/2
	logits := make([]float64, len(estimates))
	maximumLogit := -math.MaxFloat64
	for index, estimate := range estimates {
		logit := (estimate.Mean - center) / roomScale
		if math.IsInf(logit, 1) {
			logit = maximumLogitRange / 2
		} else if math.IsInf(logit, -1) {
			logit = -maximumLogitRange / 2
		}
		logits[index] = logit
		maximumLogit = math.Max(maximumLogit, logit)
	}

	weights := make([]float64, len(logits))
	for index, logit := range logits {
		normalized := math.Max(logit-maximumLogit, -maximumLogitRange)
		weights[index] = math.Exp(normalized)
	}
	return weights, nil
}

func plackettLuceDistributions(weights []float64) [][]float64 {
	playerCount := len(weights)
	stateCount := 1 << playerCount
	stateProbabilities := make([]float64, stateCount)
	stateProbabilities[0] = 1
	distributions := make([][]float64, playerCount)
	for playerIndex := range distributions {
		distributions[playerIndex] = make([]float64, playerCount)
	}

	for selectedCount := 0; selectedCount < playerCount; selectedCount++ {
		next := make([]float64, stateCount)
		for selected, stateProbability := range stateProbabilities {
			if stateProbability == 0 || bits.OnesCount(uint(selected)) != selectedCount {
				continue
			}
			remainingWeight := 0.0
			for playerIndex, weight := range weights {
				if selected&(1<<playerIndex) == 0 {
					remainingWeight += weight
				}
			}
			for playerIndex, weight := range weights {
				if selected&(1<<playerIndex) != 0 {
					continue
				}
				choiceProbability := weight / remainingWeight
				jointProbability := stateProbability * choiceProbability
				distributions[playerIndex][selectedCount] += jointProbability
				next[selected|(1<<playerIndex)] += jointProbability
			}
		}
		stateProbabilities = next
	}
	return distributions
}
