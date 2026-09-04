package matchmaking

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// RoomSelection is the chosen eligible destination for one candidate.
type RoomSelection struct {
	RoomID        string
	MembersBefore int
	Capacity      int
	AgePriority   bool
	Decision      CandidateDecision
}

type roomOption struct {
	room      RoomView
	decision  CandidateDecision
	inputRank int
}

// SelectRoom chooses one eligible room from a bounded homogeneous partition.
// Starvation protection wins first. For young rooms, a nearly full room wins
// when the policy enables that preference, followed by the smaller skill gap.
func (e *Evaluator) SelectRoom(rooms []RoomView, candidate Candidate, evaluatedAt time.Time) (RoomSelection, bool, error) {
	if len(rooms) == 0 {
		return RoomSelection{}, false, nil
	}
	if err := validateSelectionScope(rooms); err != nil {
		return RoomSelection{}, false, err
	}
	if len(rooms) > rooms[0].Policy.RoomLimit {
		return RoomSelection{}, false, errors.New("room batch exceeds the policy scan limit")
	}

	options := make([]roomOption, 0, len(rooms))
	for index, room := range rooms {
		decisions, err := e.Filter(room, []Candidate{candidate}, evaluatedAt)
		if err != nil {
			return RoomSelection{}, false, fmt.Errorf("room %q: %w", room.RoomID, err)
		}
		if decisions[0].Eligible {
			options = append(options, roomOption{room: room, decision: decisions[0], inputRank: index})
		}
	}
	if len(options) == 0 {
		return RoomSelection{}, false, nil
	}

	slices.SortStableFunc(options, func(left, right roomOption) int {
		leftOld := roomAgePrioritized(left.room, evaluatedAt)
		rightOld := roomAgePrioritized(right.room, evaluatedAt)
		if leftOld != rightOld {
			if leftOld {
				return -1
			}
			return 1
		}
		if leftOld {
			if left.room.CreatedAt.Before(right.room.CreatedAt) {
				return -1
			}
			if left.room.CreatedAt.After(right.room.CreatedAt) {
				return 1
			}
		}
		if left.room.Policy.PreferNearlyFull && len(left.room.Members) != len(right.room.Members) {
			return len(right.room.Members) - len(left.room.Members)
		}
		if left.room.Policy.PreferNearlyFull && left.decision.SkillGap != right.decision.SkillGap {
			if left.decision.SkillGap < right.decision.SkillGap {
				return -1
			}
			return 1
		}
		return left.inputRank - right.inputRank
	})

	selected := options[0]
	return RoomSelection{
		RoomID:        selected.room.RoomID,
		MembersBefore: len(selected.room.Members),
		Capacity:      selected.room.Capacity,
		AgePriority:   roomAgePrioritized(selected.room, evaluatedAt),
		Decision:      selected.decision,
	}, true, nil
}

func validateSelectionScope(rooms []RoomView) error {
	reference := rooms[0]
	seen := make(map[string]struct{}, len(rooms))
	for index, room := range rooms {
		if err := room.Validate(); err != nil {
			return fmt.Errorf("room %d: %w", index, err)
		}
		if room.ModeID != reference.ModeID || room.Capacity != reference.Capacity || room.Policy != reference.Policy {
			return errors.New("room selection requires one mode, capacity and immutable policy")
		}
		if _, duplicate := seen[room.RoomID]; duplicate {
			return fmt.Errorf("room selection contains duplicate room %q", room.RoomID)
		}
		seen[room.RoomID] = struct{}{}
	}
	return nil
}
