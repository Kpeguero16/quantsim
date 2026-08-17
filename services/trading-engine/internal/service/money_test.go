package service_test

import (
	"math"
	"testing"

	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
)

// tolerance for float money comparisons. Postgres holds NUMERIC(20,4) and is
// authoritative; these tests are checking the arithmetic, not the storage
// precision, so they compare to well inside four decimal places.
const tolerance = 1e-9

func closeTo(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func TestWeightedAvgCost(t *testing.T) {
	tests := []struct {
		name                               string
		oldQty, oldAvg, fillQty, fillPrice float64
		want                               float64
	}{
		{"first buy into an empty position", 0, 0, 10, 100, 100},
		{"equal sizes average evenly", 10, 100, 10, 120, 110},
		{"unequal sizes weight by quantity", 10, 100, 30, 200, 175},
		{"a buy at the same price does not move it", 10, 100, 5, 100, 100},
		{"buying back into a fully sold position resets to the new price", 0, 250, 4, 90, 90},
		{"fractional quantities", 2.5, 40, 7.5, 80, 70},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.WeightedAvgCost(tt.oldQty, tt.oldAvg, tt.fillQty, tt.fillPrice)
			closeTo(t, got, tt.want, "avg cost")
		})
	}
}

// The case that matters most: a position sold to zero keeps its row, so the
// next buy arrives with oldQty == 0 and a stale oldAvg still on it. If that
// stale average leaked into the result, every P/L after a round trip would be
// wrong -- and wrong by a plausible amount, which is worse than obviously
// broken.
func TestWeightedAvgCost_ZeroQuantityDoesNotDivideByZero(t *testing.T) {
	if got := service.WeightedAvgCost(0, 999, 0, 50); got != 0 {
		t.Errorf("got %v, want 0 -- a zero-share fill against an empty position", got)
	}
	if math.IsNaN(service.WeightedAvgCost(0, 0, 0, 0)) {
		t.Error("got NaN; 0/0 must be guarded, not computed")
	}
}

func TestRealizedPL(t *testing.T) {
	tests := []struct {
		name                         string
		avgCost, fillPrice, quantity float64
		want                         float64
	}{
		{"profit", 100, 150, 10, 500},
		{"loss is negative, not absolute", 150, 100, 10, -500},
		{"flat", 100, 100, 10, 0},
		{"scales with quantity, not position size", 100, 110, 3, 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.RealizedPL(tt.avgCost, tt.fillPrice, tt.quantity)
			closeTo(t, got, tt.want, "realized P/L")
		})
	}
}
