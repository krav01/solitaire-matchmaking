package matchmaking_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestEvaluatorFilterAppliesEligibilityInInputOrder(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 4, 3, 0, 0, 0, time.UTC)
	room := testRoom(createdAt, 5, []float64{23, 24, 25, 26})
	evaluator := newEvaluator(t)
	evaluatedAt := createdAt.Add(10 * time.Second)
	candidates := []matchmaking.Candidate{
		testCandidate("accepted", "player-5", 27, createdAt, evaluatedAt),
		testCandidate(room.Members[0].TicketID, "other-player", 25, createdAt, evaluatedAt),
		testCandidate("duplicate-player", room.Members[1].PlayerID, 25, createdAt, evaluatedAt),
		func() matchmaking.Candidate {
			candidate := testCandidate("wrong-model", "player-6", 25, createdAt, evaluatedAt)
			candidate.Rating.ModelVersion = "rating-v2"
			return candidate
		}(),
		func() matchmaking.Candidate {
			candidate := testCandidate("future-snapshot", "player-7", 25, createdAt, evaluatedAt)
			candidate.SnapshotAt = evaluatedAt.Add(time.Second)
			return candidate
		}(),
	}
	originalRoom := room
	originalRoom.Members = append([]matchmaking.Candidate(nil), room.Members...)
	originalCandidates := append([]matchmaking.Candidate(nil), candidates...)

	decisions, err := evaluator.Filter(room, candidates, evaluatedAt)
	if err != nil {
		t.Fatalf("Filter() error = %v", err)
	}
	want := []struct {
		eligible  bool
		rejection matchmaking.RejectionCode
	}{
		{eligible: true},
		{rejection: matchmaking.RejectionDuplicateTicket},
		{rejection: matchmaking.RejectionDuplicatePlayer},
		{rejection: matchmaking.RejectionRatingModelMismatch},
		{rejection: matchmaking.RejectionInvalidCandidate},
	}
	if len(decisions) != len(want) {
		t.Fatalf("Filter() returned %d decisions, want %d", len(decisions), len(want))
	}
	for index, decision := range decisions {
		if decision.TicketID != candidates[index].TicketID || decision.Eligible != want[index].eligible || decision.Rejection != want[index].rejection {
			t.Errorf("decision[%d] = %+v, want eligible=%v rejection=%q", index, decision, want[index].eligible, want[index].rejection)
		}
	}
	if decisions[0].WinProbabilitySpread == nil {
		t.Fatal("candidate completing the room has no probability-spread evaluation")
	}
	if !reflect.DeepEqual(room, originalRoom) || !reflect.DeepEqual(candidates, originalCandidates) {
		t.Fatal("Filter() mutated its inputs")
	}
}

func TestEvaluatorRejectsWholeRoomSkillGap(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 4, 3, 0, 0, 0, time.UTC)
	room := testRoom(createdAt, 5, []float64{20, 21, 22, 23})
	room.Policy.MaxSkillGap = 5
	candidate := testCandidate("candidate", "player-5", 17, createdAt, createdAt.Add(10*time.Second))

	decisions, err := newEvaluator(t).Filter(room, []matchmaking.Candidate{candidate}, createdAt.Add(10*time.Second))
	if err != nil {
		t.Fatalf("Filter() error = %v", err)
	}
	if decisions[0].Eligible || decisions[0].Rejection != matchmaking.RejectionSkillGapExceeded || decisions[0].SkillGap != 6 {
		t.Fatalf("Filter() decision = %+v, want whole-room skill-gap rejection", decisions[0])
	}
}

func TestEvaluatorRejectsCompleteRoomProbabilitySpread(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 4, 3, 0, 0, 0, time.UTC)
	room := testRoom(createdAt, 5, []float64{45, 35, 25, 15, 5})
	room.Policy.MaxSkillGap = 50
	room.Policy.MaxWinProbabilitySpread = 0.1

	report, err := newEvaluator(t).EvaluateFairness(room, createdAt.Add(10*time.Second))
	if err != nil {
		t.Fatalf("EvaluateFairness() error = %v", err)
	}
	if report.WithinHardLimits || report.SkillGap != 40 || report.WinProbabilitySpread <= room.Policy.MaxWinProbabilitySpread {
		t.Fatalf("EvaluateFairness() report = %+v, want probability-spread rejection", report)
	}
}

func TestEvaluatorRejectsExpiredRoom(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 4, 3, 0, 0, 0, time.UTC)
	room := testRoom(createdAt, 5, nil)
	candidate := testCandidate("candidate", "player-1", 25, createdAt, createdAt.Add(10*time.Second))

	decisions, err := newEvaluator(t).Filter(room, []matchmaking.Candidate{candidate}, room.Deadline.Add(time.Nanosecond))
	if err != nil {
		t.Fatalf("Filter() error = %v", err)
	}
	if decisions[0].Rejection != matchmaking.RejectionRoomExpired {
		t.Fatalf("Filter() decision = %+v, want room-expired rejection", decisions[0])
	}
}

func TestEvaluatorEnforcesCandidateLimit(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 4, 3, 0, 0, 0, time.UTC)
	room := testRoom(createdAt, 5, nil)
	room.Policy.CandidateLimit = 1
	candidates := []matchmaking.Candidate{
		testCandidate("candidate-1", "player-1", 25, createdAt, createdAt),
		testCandidate("candidate-2", "player-2", 25, createdAt, createdAt),
	}
	if _, err := newEvaluator(t).Filter(room, candidates, createdAt); err == nil {
		t.Fatal("Filter() error = nil, want candidate-limit error")
	}
}

func TestRoomViewValidateRejectsInvalidMembership(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 4, 3, 0, 0, 0, time.UTC)
	valid := testRoom(createdAt, 5, []float64{24, 25})
	tests := []struct {
		name   string
		change func(*matchmaking.RoomView)
	}{
		{name: "missing mode", change: func(room *matchmaking.RoomView) { room.ModeID = "" }},
		{name: "unsupported capacity", change: func(room *matchmaking.RoomView) { room.Capacity = 4 }},
		{name: "deadline exceeds fill timeout", change: func(room *matchmaking.RoomView) { room.Deadline = room.Deadline.Add(time.Second) }},
		{name: "duplicate player", change: func(room *matchmaking.RoomView) { room.Members[1].PlayerID = room.Members[0].PlayerID }},
		{name: "incompatible model", change: func(room *matchmaking.RoomView) { room.Members[0].Rating.ModelVersion = "rating-v2" }},
		{name: "snapshot after deadline", change: func(room *matchmaking.RoomView) { room.Members[0].SnapshotAt = room.Deadline.Add(time.Second) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			room := valid
			room.Members = append([]matchmaking.Candidate(nil), valid.Members...)
			test.change(&room)
			if err := room.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func newEvaluator(t *testing.T) *matchmaking.Evaluator {
	t.Helper()
	model, err := rating.NewBaseline(rating.DefaultBaselineConfig("rating-v1"))
	if err != nil {
		t.Fatalf("NewBaseline() error = %v", err)
	}
	evaluator, err := matchmaking.NewEvaluator(model)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}
	return evaluator
}

func testRoom(createdAt time.Time, capacity int, means []float64) matchmaking.RoomView {
	room := matchmaking.RoomView{
		RoomID:    "room-1",
		ModeID:    "classic",
		Capacity:  capacity,
		CreatedAt: createdAt,
		Deadline:  createdAt.Add(30 * time.Second),
		Policy: matchmaking.Policy{
			Version:                 "matching-v1",
			RatingModelVersion:      "rating-v1",
			InitialSkillGap:         2,
			MaxSkillGap:             10,
			MaxWinProbabilitySpread: 1,
			ExpansionInterval:       5 * time.Second,
			FillTimeout:             30 * time.Second,
			AgePriorityAfter:        15 * time.Second,
			CandidateLimit:          100,
			PreferNearlyFull:        true,
		},
		Members: make([]matchmaking.Candidate, len(means)),
	}
	for index, mean := range means {
		room.Members[index] = testCandidate(
			"ticket-"+string(rune('a'+index)),
			"player-"+string(rune('1'+index)),
			mean,
			createdAt.Add(-time.Second),
			createdAt,
		)
	}
	return room
}

func testCandidate(ticketID, playerID string, mean float64, joinedAt, snapshotAt time.Time) matchmaking.Candidate {
	return matchmaking.Candidate{
		TicketID:   ticketID,
		PlayerID:   playerID,
		JoinedAt:   joinedAt,
		SnapshotAt: snapshotAt,
		Rating: rating.Estimate{
			Mean:         mean,
			Uncertainty:  5,
			ModelVersion: "rating-v1",
			UpdatedAt:    joinedAt.Add(-time.Minute),
		},
	}
}
