package matchmaking_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
)

func TestSelectRoomPrefersNearlyFullEligibleRoom(t *testing.T) {
	t.Parallel()
	evaluatedAt := time.Date(2026, time.September, 4, 5, 0, 10, 0, time.UTC)
	lessFull := testRoom(evaluatedAt.Add(-5*time.Second), 5, []float64{25})
	lessFull.RoomID = "less-full"
	nearlyFull := testRoom(evaluatedAt.Add(-5*time.Second), 5, []float64{25, 25, 25, 25})
	nearlyFull.RoomID = "nearly-full"
	nearlyFull.AggregateVersion = 42
	rooms := []matchmaking.RoomView{lessFull, nearlyFull}
	original := cloneRooms(rooms)
	candidate := testCandidate("candidate", "new-player", 25, evaluatedAt.Add(-time.Minute), evaluatedAt)

	selection, found, err := newEvaluator(t).SelectRoom(rooms, candidate, evaluatedAt)
	if err != nil {
		t.Fatalf("SelectRoom() error = %v", err)
	}
	if !found || selection.RoomID != nearlyFull.RoomID || selection.RoomVersion != 42 || selection.MembersBefore != 4 || selection.Capacity != 5 {
		t.Fatalf("SelectRoom() = %+v, found=%v, want nearly-full room", selection, found)
	}
	if !reflect.DeepEqual(rooms, original) {
		t.Fatal("SelectRoom() mutated input rooms")
	}
}

func TestSelectRoomAgePriorityOverridesFillPreference(t *testing.T) {
	t.Parallel()
	evaluatedAt := time.Date(2026, time.September, 4, 5, 0, 25, 0, time.UTC)
	old := testRoom(evaluatedAt.Add(-20*time.Second), 5, []float64{25})
	old.RoomID = "old"
	nearlyFull := testRoom(evaluatedAt.Add(-5*time.Second), 5, []float64{25, 25, 25, 25})
	nearlyFull.RoomID = "nearly-full"
	candidate := testCandidate("candidate", "new-player", 25, evaluatedAt.Add(-time.Minute), evaluatedAt)

	selection, found, err := newEvaluator(t).SelectRoom([]matchmaking.RoomView{nearlyFull, old}, candidate, evaluatedAt)
	if err != nil {
		t.Fatalf("SelectRoom() error = %v", err)
	}
	if !found || selection.RoomID != old.RoomID || !selection.AgePriority {
		t.Fatalf("SelectRoom() = %+v, found=%v, want age-prioritized room", selection, found)
	}
}

func TestSelectRoomSkipsIneligibleNearlyFullRoom(t *testing.T) {
	t.Parallel()
	evaluatedAt := time.Date(2026, time.September, 4, 5, 0, 5, 0, time.UTC)
	eligible := testRoom(evaluatedAt.Add(-5*time.Second), 5, []float64{25})
	eligible.RoomID = "eligible"
	ineligible := testRoom(evaluatedAt.Add(-5*time.Second), 5, []float64{35, 35, 35, 35})
	ineligible.RoomID = "ineligible-nearly-full"
	candidate := testCandidate("candidate", "new-player", 25, evaluatedAt.Add(-time.Minute), evaluatedAt)

	selection, found, err := newEvaluator(t).SelectRoom([]matchmaking.RoomView{ineligible, eligible}, candidate, evaluatedAt)
	if err != nil {
		t.Fatalf("SelectRoom() error = %v", err)
	}
	if !found || selection.RoomID != eligible.RoomID {
		t.Fatalf("SelectRoom() = %+v, found=%v, want eligible room", selection, found)
	}
}

func TestSelectRoomReturnsNotFoundWhenHardFairnessRejectsAllRooms(t *testing.T) {
	t.Parallel()
	evaluatedAt := time.Date(2026, time.September, 4, 5, 0, 30, 0, time.UTC)
	room := testRoom(evaluatedAt.Add(-30*time.Second), 5, []float64{25})
	room.Policy.MaxSkillGap = 5
	candidate := testCandidate("candidate", "new-player", 40, evaluatedAt.Add(-time.Minute), evaluatedAt)

	selection, found, err := newEvaluator(t).SelectRoom([]matchmaking.RoomView{room}, candidate, evaluatedAt)
	if err != nil {
		t.Fatalf("SelectRoom() error = %v", err)
	}
	if found || selection != (matchmaking.RoomSelection{}) {
		t.Fatalf("SelectRoom() = %+v, found=%v, want no selection", selection, found)
	}
}

func TestSelectRoomEnforcesHomogeneousBoundedScope(t *testing.T) {
	t.Parallel()
	evaluatedAt := time.Date(2026, time.September, 4, 5, 0, 10, 0, time.UTC)
	first := testRoom(evaluatedAt.Add(-5*time.Second), 5, nil)
	second := testRoom(evaluatedAt.Add(-5*time.Second), 5, nil)
	second.RoomID = "room-2"
	candidate := testCandidate("candidate", "new-player", 25, evaluatedAt.Add(-time.Minute), evaluatedAt)

	second.ModeID = "draw-three"
	if _, _, err := newEvaluator(t).SelectRoom([]matchmaking.RoomView{first, second}, candidate, evaluatedAt); err == nil {
		t.Fatal("SelectRoom() error = nil for mixed modes")
	}
	second.ModeID = first.ModeID
	first.Policy.RoomLimit = 1
	second.Policy.RoomLimit = 1
	if _, _, err := newEvaluator(t).SelectRoom([]matchmaking.RoomView{first, second}, candidate, evaluatedAt); err == nil {
		t.Fatal("SelectRoom() error = nil for a room batch above the scan limit")
	}
}

func cloneRooms(source []matchmaking.RoomView) []matchmaking.RoomView {
	clone := make([]matchmaking.RoomView, len(source))
	for index, room := range source {
		clone[index] = room
		clone[index].Members = append([]matchmaking.Candidate(nil), room.Members...)
	}
	return clone
}
