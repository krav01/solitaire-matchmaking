package rating_test

import (
	"math"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

func TestEstimateValidate(t *testing.T) {
	t.Parallel()
	now := time.Now()
	validDeviation := 4.5
	tests := []struct {
		name    string
		value   rating.Estimate
		wantErr bool
	}{
		{name: "valid without performance deviation", value: rating.Estimate{Mean: 25, Uncertainty: 8.3, ModelVersion: "rating-v1", UpdatedAt: now}},
		{name: "valid with performance deviation", value: rating.Estimate{Mean: 25, Uncertainty: 3, PerformanceDeviation: &validDeviation, ModelVersion: "rating-v1", UpdatedAt: now}},
		{name: "zero uncertainty", value: rating.Estimate{Mean: 25, ModelVersion: "rating-v1", UpdatedAt: now}, wantErr: true},
		{name: "not a number mean", value: rating.Estimate{Mean: math.NaN(), Uncertainty: 3, ModelVersion: "rating-v1", UpdatedAt: now}, wantErr: true},
		{name: "missing model version", value: rating.Estimate{Mean: 25, Uncertainty: 3, UpdatedAt: now}, wantErr: true},
		{name: "missing update time", value: rating.Estimate{Mean: 25, Uncertainty: 3, ModelVersion: "rating-v1"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.value.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
