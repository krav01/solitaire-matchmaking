package matchmaking_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
)

func TestPolicyAllowedSkillGapExpandsInBoundedSteps(t *testing.T) {
	t.Parallel()
	policy := testRoom(time.Date(2026, time.September, 4, 4, 0, 0, 0, time.UTC), 5, nil).Policy
	policy.InitialSkillGap = 2
	policy.MaxSkillGap = 8
	policy.ExpansionInterval = 10 * time.Second
	policy.FillTimeout = 30 * time.Second

	tests := []struct {
		name    string
		age     time.Duration
		want    float64
		wantErr bool
	}{
		{name: "initial", age: 0, want: 2},
		{name: "before first step", age: 10*time.Second - time.Nanosecond, want: 2},
		{name: "first step", age: 10 * time.Second, want: 4},
		{name: "second step", age: 20 * time.Second, want: 6},
		{name: "at timeout", age: 30 * time.Second, want: 8},
		{name: "after timeout remains bounded", age: time.Minute, want: 8},
		{name: "negative age", age: -time.Nanosecond, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := policy.AllowedSkillGap(test.age)
			if (err != nil) != test.wantErr {
				t.Fatalf("AllowedSkillGap() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("AllowedSkillGap() = %f, want %f", got, test.want)
			}
		})
	}
}

func TestEvaluatorUsesExpandedSkillWindowWithoutRelaxingHardLimit(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 4, 4, 0, 0, 0, time.UTC)
	room := testRoom(createdAt, 5, []float64{24, 25, 25, 26})
	room.Policy.InitialSkillGap = 2
	room.Policy.MaxSkillGap = 8
	room.Policy.ExpansionInterval = 10 * time.Second
	candidate := testCandidate("candidate", "player-5", 29, createdAt, createdAt)

	evaluator := newEvaluator(t)
	early, err := evaluator.Filter(room, []matchmaking.Candidate{candidate}, createdAt.Add(9*time.Second))
	if err != nil {
		t.Fatalf("early Filter() error = %v", err)
	}
	if early[0].Rejection != matchmaking.RejectionSkillWindowExceeded || early[0].AllowedSkillGap != 2 {
		t.Fatalf("early decision = %+v, want active-window rejection", early[0])
	}

	later, err := evaluator.Filter(room, []matchmaking.Candidate{candidate}, createdAt.Add(20*time.Second))
	if err != nil {
		t.Fatalf("later Filter() error = %v", err)
	}
	if !later[0].Eligible || later[0].AllowedSkillGap != 6 {
		t.Fatalf("later decision = %+v, want eligibility after expansion", later[0])
	}

	outlier := testCandidate("outlier", "player-6", 40, createdAt, createdAt)
	hardLimit, err := evaluator.Filter(room, []matchmaking.Candidate{outlier}, room.Deadline)
	if err != nil {
		t.Fatalf("hard-limit Filter() error = %v", err)
	}
	if hardLimit[0].Rejection != matchmaking.RejectionSkillGapExceeded {
		t.Fatalf("hard-limit decision = %+v, want hard skill-gap rejection", hardLimit[0])
	}
}

func TestPrioritizeWaitingRoomsMovesStarvingRoomsFirst(t *testing.T) {
	t.Parallel()
	evaluatedAt := time.Date(2026, time.September, 4, 4, 0, 30, 0, time.UTC)
	young := testRoom(evaluatedAt.Add(-5*time.Second), 5, nil)
	young.RoomID = "young"
	older := testRoom(evaluatedAt.Add(-25*time.Second), 5, nil)
	older.RoomID = "older"
	old := testRoom(evaluatedAt.Add(-20*time.Second), 5, nil)
	old.RoomID = "old"
	rooms := []matchmaking.RoomView{young, old, older}
	original := append([]matchmaking.RoomView(nil), rooms...)

	ordered, err := matchmaking.PrioritizeWaitingRooms(rooms, evaluatedAt)
	if err != nil {
		t.Fatalf("PrioritizeWaitingRooms() error = %v", err)
	}
	wantIDs := []string{"older", "old", "young"}
	for index, wantID := range wantIDs {
		if ordered[index].RoomID != wantID {
			t.Fatalf("ordered room %d = %q, want %q", index, ordered[index].RoomID, wantID)
		}
	}
	if !reflect.DeepEqual(rooms, original) {
		t.Fatal("PrioritizeWaitingRooms() mutated input rooms")
	}
}

func TestPrioritizeWaitingRoomsPreservesYoungRoomOrder(t *testing.T) {
	t.Parallel()
	evaluatedAt := time.Date(2026, time.September, 4, 4, 0, 10, 0, time.UTC)
	first := testRoom(evaluatedAt.Add(-5*time.Second), 5, nil)
	first.RoomID = "first"
	second := testRoom(evaluatedAt.Add(-10*time.Second), 5, nil)
	second.RoomID = "second"

	ordered, err := matchmaking.PrioritizeWaitingRooms([]matchmaking.RoomView{first, second}, evaluatedAt)
	if err != nil {
		t.Fatalf("PrioritizeWaitingRooms() error = %v", err)
	}
	if ordered[0].RoomID != "first" || ordered[1].RoomID != "second" {
		t.Fatalf("young-room order changed: %q, %q", ordered[0].RoomID, ordered[1].RoomID)
	}
}
