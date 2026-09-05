package rating

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// ModelDeployment is the portable, revision-fenced state persisted by an
// integration adapter. RollbackVersion is cleared after a rollback.
type ModelDeployment struct {
	ActiveVersion   string    `json:"active_version"`
	RollbackVersion string    `json:"rollback_version,omitempty"`
	Revision        uint64    `json:"revision"`
	ChangedAt       time.Time `json:"changed_at"`
}

type ModelTransitionKind string

const (
	ModelTransitionActivation ModelTransitionKind = "activation"
	ModelTransitionRollback   ModelTransitionKind = "rollback"
)

// ModelTransition is an append-only audit record for a deployment change.
type ModelTransition struct {
	Kind        ModelTransitionKind `json:"kind"`
	FromVersion string              `json:"from_version"`
	ToVersion   string              `json:"to_version"`
	Revision    uint64              `json:"revision"`
	ChangedAt   time.Time           `json:"changed_at"`
	Reason      string              `json:"reason"`
}

type ActivateModelCommand struct {
	ExpectedRevision uint64                `json:"expected_revision"`
	ActivatedAt      time.Time             `json:"activated_at"`
	Reason           string                `json:"reason"`
	Comparison       ModelComparisonReport `json:"comparison"`
}

type RollbackModelCommand struct {
	ExpectedRevision uint64    `json:"expected_revision"`
	RolledBackAt     time.Time `json:"rolled_back_at"`
	Reason           string    `json:"reason"`
}

func NewModelDeployment(activeVersion string, changedAt time.Time) (ModelDeployment, error) {
	deployment := ModelDeployment{
		ActiveVersion: activeVersion,
		Revision:      1,
		ChangedAt:     changedAt,
	}
	if err := deployment.Validate(); err != nil {
		return ModelDeployment{}, err
	}

	return deployment, nil
}

func (d ModelDeployment) Validate() error {
	if d.ActiveVersion == "" || d.Revision == 0 || d.ChangedAt.IsZero() {
		return errors.New("model deployment requires an active version, revision and change time")
	}
	if d.RollbackVersion != "" && d.RollbackVersion == d.ActiveVersion {
		return errors.New("model deployment rollback version must differ from the active version")
	}

	return nil
}

// ActivateModel promotes only an eligible candidate compared with the current
// active baseline and retains that baseline as the one-step rollback target.
func ActivateModel(current ModelDeployment, command ActivateModelCommand) (ModelDeployment, ModelTransition, error) {
	if err := validateModelChange(current, command.ExpectedRevision, command.ActivatedAt, command.Reason); err != nil {
		return ModelDeployment{}, ModelTransition{}, err
	}
	comparison := command.Comparison
	if err := comparison.ValidateForActivation(); err != nil {
		return ModelDeployment{}, ModelTransition{}, err
	}
	if comparison.BaselineVersion != current.ActiveVersion {
		return ModelDeployment{}, ModelTransition{}, fmt.Errorf(
			"comparison baseline %q does not match active model %q",
			comparison.BaselineVersion,
			current.ActiveVersion,
		)
	}
	if comparison.CandidateVersion == "" || comparison.CandidateVersion == current.ActiveVersion {
		return ModelDeployment{}, ModelTransition{}, errors.New("activation requires a distinct candidate version")
	}
	if comparison.HoldoutAvailableThrough.After(command.ActivatedAt) {
		return ModelDeployment{}, ModelTransition{}, errors.New("activation time cannot predate compared holdout data")
	}

	next := ModelDeployment{
		ActiveVersion:   comparison.CandidateVersion,
		RollbackVersion: current.ActiveVersion,
		Revision:        current.Revision + 1,
		ChangedAt:       command.ActivatedAt,
	}
	transition := ModelTransition{
		Kind:        ModelTransitionActivation,
		FromVersion: current.ActiveVersion,
		ToVersion:   next.ActiveVersion,
		Revision:    next.Revision,
		ChangedAt:   next.ChangedAt,
		Reason:      command.Reason,
	}

	return next, transition, nil
}

// RollbackModel restores the single retained predecessor and consumes the
// rollback target so repeated calls cannot toggle versions accidentally.
func RollbackModel(current ModelDeployment, command RollbackModelCommand) (ModelDeployment, ModelTransition, error) {
	if err := validateModelChange(current, command.ExpectedRevision, command.RolledBackAt, command.Reason); err != nil {
		return ModelDeployment{}, ModelTransition{}, err
	}
	if current.RollbackVersion == "" {
		return ModelDeployment{}, ModelTransition{}, errors.New("model deployment has no rollback target")
	}

	next := ModelDeployment{
		ActiveVersion: current.RollbackVersion,
		Revision:      current.Revision + 1,
		ChangedAt:     command.RolledBackAt,
	}
	transition := ModelTransition{
		Kind:        ModelTransitionRollback,
		FromVersion: current.ActiveVersion,
		ToVersion:   next.ActiveVersion,
		Revision:    next.Revision,
		ChangedAt:   next.ChangedAt,
		Reason:      command.Reason,
	}

	return next, transition, nil
}

func validateModelChange(current ModelDeployment, expectedRevision uint64, changedAt time.Time, reason string) error {
	if err := current.Validate(); err != nil {
		return err
	}
	if expectedRevision != current.Revision {
		return fmt.Errorf("model deployment revision is %d, expected %d", current.Revision, expectedRevision)
	}
	if current.Revision == math.MaxUint64 {
		return errors.New("model deployment revision cannot be incremented")
	}
	if changedAt.IsZero() || !changedAt.After(current.ChangedAt) {
		return errors.New("model deployment change time must be after the current state")
	}
	if reason == "" {
		return errors.New("model deployment change reason is required")
	}

	return nil
}
