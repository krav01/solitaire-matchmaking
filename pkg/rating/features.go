package rating

import (
	"errors"
	"fmt"
	"slices"
)

// FeatureName identifies one verified gameplay observation.
type FeatureName string

const (
	FeatureScore         FeatureName = "score"
	FeatureElapsedMillis FeatureName = "elapsed_ms"
	FeatureCompleted     FeatureName = "completed"
	FeatureMoves         FeatureName = "moves"
	FeatureUndoMoves     FeatureName = "undo_moves"
	FeatureRevealedCards FeatureName = "revealed_cards"

	maxExactFloatInteger int64 = 1 << 53
)

// FeatureDefinition selects one observation and assigns it to an exclusive
// signal family. A schema may contain at most one feature from each family.
type FeatureDefinition struct {
	Name         FeatureName `json:"name"`
	SignalFamily string      `json:"signal_family"`
}

// FeatureSchema is an immutable extraction contract for one compatible game
// context. Changing its fields or definitions requires a new Version.
type FeatureSchema struct {
	Version             string              `json:"version"`
	ModeID              string              `json:"mode_id"`
	ScoringRulesVersion string              `json:"scoring_rules_version"`
	DeckVersion         string              `json:"deck_version"`
	Definitions         []FeatureDefinition `json:"definitions"`
}

// FeatureContext supplies verified metadata that is not part of MatchResult.
type FeatureContext struct {
	DeckVersion string `json:"deck_version"`
}

// FeatureObservation distinguishes a missing value from an observed zero.
// Value is raw and is not imputed, scaled or weighted by the encoder.
type FeatureObservation struct {
	Name    FeatureName `json:"name"`
	Value   float64     `json:"value"`
	Present bool        `json:"present"`
}

// PlayerFeatureVector contains observations in schema-definition order.
type PlayerFeatureVector struct {
	PlayerID     string               `json:"player_id"`
	Observations []FeatureObservation `json:"observations"`
}

// FeatureBatch is a replayable raw extraction from one verified result.
type FeatureBatch struct {
	SchemaVersion       string                `json:"schema_version"`
	EventID             string                `json:"event_id"`
	ModeID              string                `json:"mode_id"`
	ScoringRulesVersion string                `json:"scoring_rules_version"`
	DeckVersion         string                `json:"deck_version"`
	DeckID              string                `json:"deck_id"`
	Players             []PlayerFeatureVector `json:"players"`
}

// FeatureEncoder extracts deterministic raw vectors using an immutable schema.
type FeatureEncoder struct {
	schema FeatureSchema
}

func NewFeatureEncoder(schema FeatureSchema) (*FeatureEncoder, error) {
	if err := validateFeatureSchema(schema); err != nil {
		return nil, err
	}

	return &FeatureEncoder{schema: cloneFeatureSchema(schema)}, nil
}

// Schema returns a copy so callers cannot mutate the active extraction contract.
func (e *FeatureEncoder) Schema() FeatureSchema {
	return cloneFeatureSchema(e.schema)
}

// Encode validates context compatibility and emits players in identifier order.
func (e *FeatureEncoder) Encode(result MatchResult, context FeatureContext) (FeatureBatch, error) {
	if err := result.Validate(); err != nil {
		return FeatureBatch{}, err
	}
	if result.ModeID != e.schema.ModeID {
		return FeatureBatch{}, fmt.Errorf("feature schema mode %q does not match result mode %q", e.schema.ModeID, result.ModeID)
	}
	if result.ScoringRulesVersion != e.schema.ScoringRulesVersion {
		return FeatureBatch{}, fmt.Errorf("feature schema scoring rules %q do not match result scoring rules %q", e.schema.ScoringRulesVersion, result.ScoringRulesVersion)
	}
	if context.DeckVersion == "" {
		return FeatureBatch{}, errors.New("feature context deck version is required")
	}
	if context.DeckVersion != e.schema.DeckVersion {
		return FeatureBatch{}, fmt.Errorf("feature schema deck version %q does not match context deck version %q", e.schema.DeckVersion, context.DeckVersion)
	}

	participants := slices.Clone(result.Participants)
	slices.SortFunc(participants, func(left, right ParticipantResult) int {
		if left.PlayerID < right.PlayerID {
			return -1
		}
		if left.PlayerID > right.PlayerID {
			return 1
		}
		return 0
	})

	players := make([]PlayerFeatureVector, 0, len(participants))
	for _, participant := range participants {
		observations := make([]FeatureObservation, 0, len(e.schema.Definitions))
		for _, definition := range e.schema.Definitions {
			observation, err := extractFeature(definition.Name, participant.Features)
			if err != nil {
				return FeatureBatch{}, fmt.Errorf("encode feature %q for player %q: %w", definition.Name, participant.PlayerID, err)
			}
			observations = append(observations, observation)
		}
		players = append(players, PlayerFeatureVector{
			PlayerID:     participant.PlayerID,
			Observations: observations,
		})
	}

	return FeatureBatch{
		SchemaVersion:       e.schema.Version,
		EventID:             result.EventID,
		ModeID:              result.ModeID,
		ScoringRulesVersion: result.ScoringRulesVersion,
		DeckVersion:         context.DeckVersion,
		DeckID:              result.DeckID,
		Players:             players,
	}, nil
}

func validateFeatureSchema(schema FeatureSchema) error {
	if schema.Version == "" || schema.ModeID == "" || schema.ScoringRulesVersion == "" || schema.DeckVersion == "" {
		return errors.New("feature schema version, mode, scoring rules and deck version are required")
	}
	if len(schema.Definitions) == 0 || len(schema.Definitions) > 6 {
		return errors.New("feature schema requires one to six definitions")
	}

	names := make(map[FeatureName]struct{}, len(schema.Definitions))
	families := make(map[string]FeatureName, len(schema.Definitions))
	hasScore := false
	hasElapsed := false
	for _, definition := range schema.Definitions {
		if !supportedFeature(definition.Name) {
			return fmt.Errorf("feature schema contains unsupported feature %q", definition.Name)
		}
		if _, exists := names[definition.Name]; exists {
			return fmt.Errorf("feature schema contains duplicate feature %q", definition.Name)
		}
		names[definition.Name] = struct{}{}

		if definition.SignalFamily == "" {
			return fmt.Errorf("feature %q requires a signal family", definition.Name)
		}
		if existing, exists := families[definition.SignalFamily]; exists {
			return fmt.Errorf("features %q and %q share signal family %q", existing, definition.Name, definition.SignalFamily)
		}
		families[definition.SignalFamily] = definition.Name

		hasScore = hasScore || definition.Name == FeatureScore
		hasElapsed = hasElapsed || definition.Name == FeatureElapsedMillis
	}
	if hasScore && hasElapsed {
		return errors.New("score and elapsed_ms cannot be enabled together because score may include time")
	}

	return nil
}

func supportedFeature(name FeatureName) bool {
	switch name {
	case FeatureScore, FeatureElapsedMillis, FeatureCompleted, FeatureMoves, FeatureUndoMoves, FeatureRevealedCards:
		return true
	default:
		return false
	}
}

func extractFeature(name FeatureName, features Features) (FeatureObservation, error) {
	observation := FeatureObservation{Name: name}

	switch name {
	case FeatureScore:
		return integerObservation(observation, features.Score)
	case FeatureElapsedMillis:
		return integerObservation(observation, features.ElapsedMillis)
	case FeatureCompleted:
		if features.Completed != nil {
			observation.Present = true
			if *features.Completed {
				observation.Value = 1
			}
		}
		return observation, nil
	case FeatureMoves:
		return integerObservation(observation, features.Moves)
	case FeatureUndoMoves:
		return integerObservation(observation, features.UndoMoves)
	case FeatureRevealedCards:
		return integerObservation(observation, features.RevealedCards)
	default:
		return FeatureObservation{}, fmt.Errorf("unsupported feature %q", name)
	}
}

func integerObservation(observation FeatureObservation, value *int64) (FeatureObservation, error) {
	if value == nil {
		return observation, nil
	}
	if *value < -maxExactFloatInteger || *value > maxExactFloatInteger {
		return FeatureObservation{}, fmt.Errorf("value %d exceeds exact float64 integer range", *value)
	}

	observation.Value = float64(*value)
	observation.Present = true
	return observation, nil
}

func cloneFeatureSchema(schema FeatureSchema) FeatureSchema {
	schema.Definitions = slices.Clone(schema.Definitions)
	return schema
}
