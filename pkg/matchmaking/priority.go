package matchmaking

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// AllowedSkillGap returns the discrete skill window for a forming-room age.
// The window starts at InitialSkillGap, expands once per ExpansionInterval and
// reaches, but never exceeds, MaxSkillGap at the fill timeout.
func (p Policy) AllowedSkillGap(roomAge time.Duration) (float64, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	if roomAge < 0 {
		return 0, errors.New("room age must be non-negative")
	}
	if roomAge >= p.FillTimeout || p.InitialSkillGap == p.MaxSkillGap {
		return p.MaxSkillGap, nil
	}

	totalSteps := p.FillTimeout / p.ExpansionInterval
	if p.FillTimeout%p.ExpansionInterval != 0 {
		totalSteps++
	}
	elapsedSteps := min(roomAge/p.ExpansionInterval, totalSteps)
	progress := float64(elapsedSteps) / float64(totalSteps)
	return p.InitialSkillGap + (p.MaxSkillGap-p.InitialSkillGap)*progress, nil
}

// PrioritizeWaitingRooms returns an immutable ordering for active rooms. Once a
// room reaches its own AgePriorityAfter threshold, it moves ahead of younger
// rooms; prioritized rooms are ordered oldest first. Young-room input order is
// preserved for later fill-speed ranking.
func PrioritizeWaitingRooms(rooms []RoomView, evaluatedAt time.Time) ([]RoomView, error) {
	if evaluatedAt.IsZero() {
		return nil, errors.New("room-priority evaluation time is required")
	}
	ordered := slices.Clone(rooms)
	for index, room := range ordered {
		if err := room.Validate(); err != nil {
			return nil, fmt.Errorf("room %d: %w", index, err)
		}
		if evaluatedAt.Before(room.CreatedAt) || evaluatedAt.After(room.Deadline) {
			return nil, fmt.Errorf("room %d: room priority requires an active forming room", index)
		}
	}

	slices.SortStableFunc(ordered, func(left, right RoomView) int {
		leftPrioritized := evaluatedAt.Sub(left.CreatedAt) >= left.Policy.AgePriorityAfter
		rightPrioritized := evaluatedAt.Sub(right.CreatedAt) >= right.Policy.AgePriorityAfter
		if leftPrioritized != rightPrioritized {
			if leftPrioritized {
				return -1
			}
			return 1
		}
		if !leftPrioritized {
			return 0
		}
		if left.CreatedAt.Before(right.CreatedAt) {
			return -1
		}
		if left.CreatedAt.After(right.CreatedAt) {
			return 1
		}
		return 0
	})
	return ordered, nil
}
