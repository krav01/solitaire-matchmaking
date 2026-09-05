package tournament

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

var (
	ErrResultConflict             = errors.New("result identity conflicts with stored result")
	ErrResultRoomNotFound         = errors.New("result room not found")
	ErrResultRoomNotCollecting    = errors.New("room is not collecting results")
	ErrResultDeadlinePassed       = errors.New("result deadline has passed")
	ErrResultParticipantsMismatch = errors.New("result participants do not match room sessions")
)

const MaxResultDeadlineBatchSize = 256

// VerifiedParticipant binds an authoritative placement to the allocated game
// session that produced it. Optional features remain absent when unobserved.
type VerifiedParticipant struct {
	SessionID string          `json:"session_id"`
	PlayerID  string          `json:"player_id"`
	Place     int             `json:"place"`
	Features  rating.Features `json:"features"`
}

// FinalizeResultCommand is idempotent by EventID. AcceptedAt is the service
// processing time and is excluded from retry equality.
type FinalizeResultCommand struct {
	EventID             string                `json:"event_id"`
	RoomID              string                `json:"room_id"`
	ModeID              string                `json:"mode_id"`
	DeckID              string                `json:"deck_id"`
	ScoringRulesVersion string                `json:"scoring_rules_version"`
	FinishedAt          time.Time             `json:"finished_at"`
	AvailableAt         time.Time             `json:"available_at"`
	AcceptedAt          time.Time             `json:"-"`
	Participants        []VerifiedParticipant `json:"participants"`
}

func (command FinalizeResultCommand) Validate() error {
	if command.AcceptedAt.IsZero() || command.AcceptedAt.Before(command.AvailableAt) {
		return errors.New("result acceptance must not precede availability")
	}
	if err := command.MatchResult().Validate(); err != nil {
		return err
	}
	sessions := make(map[string]struct{}, len(command.Participants))
	for _, participant := range command.Participants {
		if participant.SessionID == "" {
			return errors.New("result participant session id is required")
		}
		if _, exists := sessions[participant.SessionID]; exists {
			return fmt.Errorf("result contains duplicate session %q", participant.SessionID)
		}
		sessions[participant.SessionID] = struct{}{}
		if err := validateFeatures(participant.Features); err != nil {
			return fmt.Errorf("result features for player %q: %w", participant.PlayerID, err)
		}
	}
	return nil
}

func (command FinalizeResultCommand) RequestDigest() (string, error) {
	if err := command.Validate(); err != nil {
		return "", err
	}
	participants := slices.Clone(command.Participants)
	slices.SortFunc(participants, func(left, right VerifiedParticipant) int {
		if left.PlayerID < right.PlayerID {
			return -1
		}
		if left.PlayerID > right.PlayerID {
			return 1
		}
		return 0
	})
	return sha256Digest(struct {
		EventID             string                `json:"event_id"`
		RoomID              string                `json:"room_id"`
		ModeID              string                `json:"mode_id"`
		DeckID              string                `json:"deck_id"`
		ScoringRulesVersion string                `json:"scoring_rules_version"`
		FinishedAt          time.Time             `json:"finished_at"`
		AvailableAt         time.Time             `json:"available_at"`
		Participants        []VerifiedParticipant `json:"participants"`
	}{
		EventID: command.EventID, RoomID: command.RoomID, ModeID: command.ModeID,
		DeckID: command.DeckID, ScoringRulesVersion: command.ScoringRulesVersion,
		FinishedAt: command.FinishedAt.UTC(), AvailableAt: command.AvailableAt.UTC(),
		Participants: participants,
	})
}

func (command FinalizeResultCommand) MatchResult() rating.MatchResult {
	participants := make([]rating.ParticipantResult, len(command.Participants))
	for index, participant := range command.Participants {
		participants[index] = rating.ParticipantResult{
			PlayerID: participant.PlayerID, Place: participant.Place, Features: participant.Features,
		}
	}
	return rating.MatchResult{
		EventID: command.EventID, RoomID: command.RoomID, ModeID: command.ModeID,
		DeckID: command.DeckID, ScoringRulesVersion: command.ScoringRulesVersion,
		FinishedAt: command.FinishedAt, AvailableAt: command.AvailableAt,
		Participants: participants,
	}
}

type FinalizedResult struct {
	Result        rating.MatchResult `json:"result"`
	RoomVersion   int64              `json:"room_version"`
	CompletedAt   time.Time          `json:"completed_at"`
	RatingPending bool               `json:"rating_pending"`
	Replay        bool               `json:"replay"`
}

type ResultDeadlineBatch struct {
	Limit     int
	ExpiredAt time.Time
}

func (batch ResultDeadlineBatch) Validate() error {
	if batch.Limit <= 0 || batch.Limit > MaxResultDeadlineBatchSize {
		return errors.New("result deadline batch size is outside the supported range")
	}
	if batch.ExpiredAt.IsZero() {
		return errors.New("result deadline evaluation time is required")
	}
	return nil
}

type ExpiredRoom struct {
	RoomID         string
	RoomVersion    int64
	ResultDeadline time.Time
	ExpiredAt      time.Time
}

type ResultRepository interface {
	FinalizeResult(context.Context, FinalizeResultCommand) (FinalizedResult, error)
	ExpireResultRooms(context.Context, ResultDeadlineBatch) ([]ExpiredRoom, error)
}

type ResultService struct {
	repository ResultRepository
}

func NewResultService(repository ResultRepository) (*ResultService, error) {
	if repository == nil {
		return nil, errors.New("result repository is required")
	}
	return &ResultService{repository: repository}, nil
}

func (service *ResultService) Finalize(ctx context.Context, command FinalizeResultCommand) (FinalizedResult, error) {
	if err := command.Validate(); err != nil {
		return FinalizedResult{}, err
	}
	return service.repository.FinalizeResult(ctx, command)
}

func (service *ResultService) ExpireDue(ctx context.Context, batch ResultDeadlineBatch) ([]ExpiredRoom, error) {
	if err := batch.Validate(); err != nil {
		return nil, err
	}
	return service.repository.ExpireResultRooms(ctx, batch)
}

func validateFeatures(features rating.Features) error {
	for name, value := range map[string]*int64{
		"elapsed_ms":     features.ElapsedMillis,
		"moves":          features.Moves,
		"undo_moves":     features.UndoMoves,
		"revealed_cards": features.RevealedCards,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("%s must be non-negative", name)
		}
	}
	return nil
}
