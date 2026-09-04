package matchmaking_test

import (
	"math"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
)

func TestPolicyValidate(t *testing.T) {
	t.Parallel()
	valid := matchmaking.Policy{
		Version: "matching-v1", RatingModelVersion: "rating-v1",
		InitialSkillGap: 2, MaxSkillGap: 5, MaxWinProbabilitySpread: 0.1,
		ExpansionInterval: 5 * time.Second, FillTimeout: 30 * time.Second,
		AgePriorityAfter: 15 * time.Second, CandidateLimit: 100, RoomLimit: 100, PreferNearlyFull: true,
	}
	tests := []struct {
		name    string
		change  func(*matchmaking.Policy)
		wantErr bool
	}{
		{name: "valid"},
		{name: "missing version", change: func(p *matchmaking.Policy) { p.Version = "" }, wantErr: true},
		{name: "reversed skill gaps", change: func(p *matchmaking.Policy) { p.MaxSkillGap = 1 }, wantErr: true},
		{name: "not a number skill gap", change: func(p *matchmaking.Policy) { p.InitialSkillGap = math.NaN() }, wantErr: true},
		{name: "invalid probability spread", change: func(p *matchmaking.Policy) { p.MaxWinProbabilitySpread = 1.1 }, wantErr: true},
		{name: "expansion after timeout", change: func(p *matchmaking.Policy) { p.ExpansionInterval = time.Minute }, wantErr: true},
		{name: "age priority after timeout", change: func(p *matchmaking.Policy) { p.AgePriorityAfter = time.Minute }, wantErr: true},
		{name: "empty candidate window", change: func(p *matchmaking.Policy) { p.CandidateLimit = 0 }, wantErr: true},
		{name: "empty room window", change: func(p *matchmaking.Policy) { p.RoomLimit = 0 }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			value := valid
			if tt.change != nil {
				tt.change(&value)
			}
			err := value.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
