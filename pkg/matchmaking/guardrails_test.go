package matchmaking_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func BenchmarkSelectRoom(b *testing.B) {
	evaluatedAt := time.Date(2026, time.September, 5, 0, 0, 30, 0, time.UTC)
	rooms := make([]matchmaking.RoomView, 100)
	for i := range rooms {
		rooms[i] = testRoom(evaluatedAt.Add(-5*time.Second), 5, []float64{24, 25, 26})
		rooms[i].RoomID = fmt.Sprintf("room-%03d", i)
	}
	candidate := testCandidate("candidate", "candidate-player", 25, evaluatedAt.Add(-time.Minute), evaluatedAt.Add(-time.Minute))
	model, err := rating.NewBaseline(rating.DefaultBaselineConfig("rating-v1"))
	if err != nil {
		b.Fatalf("NewBaseline() error = %v", err)
	}
	evaluator, err := matchmaking.NewEvaluator(model)
	if err != nil {
		b.Fatalf("NewEvaluator() error = %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		selection, found, err := evaluator.SelectRoom(rooms, candidate, evaluatedAt)
		if err != nil {
			b.Fatalf("SelectRoom() error = %v", err)
		}
		if !found || selection.RoomID == "" {
			b.Fatal("SelectRoom() returned no room")
		}
	}
}

func FuzzSelectRoomPreservesHardSkillGap(f *testing.F) {
	f.Add(float64(25), float64(25))
	f.Add(float64(10), float64(50))
	f.Add(float64(-100), float64(100))

	f.Fuzz(func(t *testing.T, memberMean, candidateMean float64) {
		if math.IsNaN(memberMean) || math.IsInf(memberMean, 0) || math.IsNaN(candidateMean) || math.IsInf(candidateMean, 0) {
			t.Skip()
		}

		evaluatedAt := time.Date(2026, time.September, 5, 0, 0, 30, 0, time.UTC)
		room := testRoom(evaluatedAt.Add(-5*time.Second), 5, []float64{memberMean, memberMean, memberMean, memberMean})
		room.Policy.InitialSkillGap = 10
		room.Policy.MaxSkillGap = 10
		candidate := testCandidate("candidate", "candidate-player", candidateMean, evaluatedAt.Add(-time.Minute), evaluatedAt.Add(-time.Minute))

		selection, found, err := newEvaluator(t).SelectRoom([]matchmaking.RoomView{room}, candidate, evaluatedAt)
		if err != nil || !found {
			return
		}
		if !selection.Decision.Eligible {
			t.Fatal("SelectRoom() returned an ineligible selection")
		}
		if selection.Decision.SkillGap > room.Policy.MaxSkillGap {
			t.Fatalf("SelectRoom() skill gap = %v, hard limit = %v", selection.Decision.SkillGap, room.Policy.MaxSkillGap)
		}
		if selection.MembersBefore >= selection.Capacity {
			t.Fatalf("SelectRoom() selected a full room: members=%d capacity=%d", selection.MembersBefore, selection.Capacity)
		}
	})
}
