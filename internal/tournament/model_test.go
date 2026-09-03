package tournament_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/tournament"
)

func TestConfigValidateCapacity(t *testing.T) {
	t.Parallel()
	valid := tournament.Config{
		ID: "cash-standard", Version: "tournament-v1", ModeID: "klondike",
		Capacity: 5, EntryFeeMinor: 100, Currency: "USD",
		ScoringRulesVersion: "score-v1", SettlementVersion: "settlement-v1",
		PolicyVersion: "matching-v1", RatingModelVersion: "rating-v1",
		ResultTimeout: time.Hour,
	}
	for _, capacity := range []int{5, 6, 7} {
		capacity := capacity
		t.Run(strconv.Itoa(capacity)+" players", func(t *testing.T) {
			t.Parallel()
			value := valid
			value.Capacity = capacity
			if err := value.Validate(); err != nil {
				t.Fatalf("Validate() unexpected error: %v", err)
			}
		})
	}
	for _, capacity := range []int{0, 4, 8} {
		capacity := capacity
		t.Run(strconv.Itoa(capacity)+" rejected", func(t *testing.T) {
			t.Parallel()
			value := valid
			value.Capacity = capacity
			if err := value.Validate(); err == nil {
				t.Fatal("Validate() expected an error")
			}
		})
	}
}
