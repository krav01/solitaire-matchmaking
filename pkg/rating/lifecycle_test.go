package rating_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestModelDeploymentActivationAndOneShotRollback(t *testing.T) {
	t.Parallel()

	observations, config := passingModelComparison(t)
	comparison, err := rating.CompareHoldoutModels(observations, config)
	if err != nil {
		t.Fatalf("CompareHoldoutModels() error = %v", err)
	}
	startedAt := config.TrainingCutoff.Add(-time.Hour)
	current, err := rating.NewModelDeployment("rating-v1", startedAt)
	if err != nil {
		t.Fatalf("NewModelDeployment() error = %v", err)
	}
	activatedAt := comparison.HoldoutAvailableThrough.Add(time.Hour)

	active, activation, err := rating.ActivateModel(current, rating.ActivateModelCommand{
		ExpectedRevision: 1,
		ActivatedAt:      activatedAt,
		Reason:           "candidate passed holdout policy",
		Comparison:       comparison,
	})
	if err != nil {
		t.Fatalf("ActivateModel() error = %v", err)
	}
	if active.ActiveVersion != "rating-v2" || active.RollbackVersion != "rating-v1" || active.Revision != 2 {
		t.Fatalf("activated deployment = %+v", active)
	}
	if activation.Kind != rating.ModelTransitionActivation || activation.FromVersion != "rating-v1" || activation.ToVersion != "rating-v2" {
		t.Fatalf("activation transition = %+v", activation)
	}

	rolledBack, rollback, err := rating.RollbackModel(active, rating.RollbackModelCommand{
		ExpectedRevision: 2,
		RolledBackAt:     activatedAt.Add(time.Minute),
		Reason:           "candidate production guard breached",
	})
	if err != nil {
		t.Fatalf("RollbackModel() error = %v", err)
	}
	if rolledBack.ActiveVersion != "rating-v1" || rolledBack.RollbackVersion != "" || rolledBack.Revision != 3 {
		t.Fatalf("rolled-back deployment = %+v", rolledBack)
	}
	if rollback.Kind != rating.ModelTransitionRollback || rollback.FromVersion != "rating-v2" || rollback.ToVersion != "rating-v1" {
		t.Fatalf("rollback transition = %+v", rollback)
	}
	if _, _, err := rating.RollbackModel(rolledBack, rating.RollbackModelCommand{
		ExpectedRevision: 3,
		RolledBackAt:     activatedAt.Add(2 * time.Minute),
		Reason:           "repeat",
	}); err == nil || !strings.Contains(err.Error(), "no rollback target") {
		t.Fatalf("second RollbackModel() error = %v", err)
	}
}

func TestActivateModelRejectsUnsafeTransitions(t *testing.T) {
	t.Parallel()

	observations, config := passingModelComparison(t)
	comparison, err := rating.CompareHoldoutModels(observations, config)
	if err != nil {
		t.Fatalf("CompareHoldoutModels() error = %v", err)
	}
	current, err := rating.NewModelDeployment("rating-v1", config.TrainingCutoff.Add(-time.Hour))
	if err != nil {
		t.Fatalf("NewModelDeployment() error = %v", err)
	}
	validTime := comparison.HoldoutAvailableThrough.Add(time.Hour)

	tests := []struct {
		name      string
		mutate    func(*rating.ModelDeployment, *rating.ActivateModelCommand)
		wantError string
	}{
		{name: "stale revision", mutate: func(_ *rating.ModelDeployment, command *rating.ActivateModelCommand) {
			command.ExpectedRevision = 2
		}, wantError: "revision"},
		{name: "ineligible comparison", mutate: func(_ *rating.ModelDeployment, command *rating.ActivateModelCommand) {
			command.Comparison.Eligible = false
		}, wantError: "not eligible"},
		{name: "wrong baseline", mutate: func(_ *rating.ModelDeployment, command *rating.ActivateModelCommand) {
			command.Comparison.BaselineVersion = "rating-v0"
		}, wantError: "segment"},
		{name: "activation predates holdout", mutate: func(_ *rating.ModelDeployment, command *rating.ActivateModelCommand) {
			command.ActivatedAt = command.Comparison.HoldoutAvailableThrough.Add(-time.Nanosecond)
		}, wantError: "predate"},
		{name: "missing reason", mutate: func(_ *rating.ModelDeployment, command *rating.ActivateModelCommand) {
			command.Reason = ""
		}, wantError: "reason"},
		{name: "revision overflow", mutate: func(deployment *rating.ModelDeployment, command *rating.ActivateModelCommand) {
			deployment.Revision = math.MaxUint64
			command.ExpectedRevision = math.MaxUint64
		}, wantError: "cannot be incremented"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deployment := current
			command := rating.ActivateModelCommand{
				ExpectedRevision: 1,
				ActivatedAt:      validTime,
				Reason:           "candidate passed holdout policy",
				Comparison:       comparison,
			}
			tt.mutate(&deployment, &command)

			_, _, err := rating.ActivateModel(deployment, command)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("ActivateModel() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}
