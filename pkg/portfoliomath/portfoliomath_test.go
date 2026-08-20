package portfoliomath

import (
	"math"
	"testing"
)

// tolerance for comparing hand-computed expectations against float64
// arithmetic. Every expected value in this file was worked out by hand, not
// captured from a run of the code under test.
const tol = 1e-9

func closeTo(got, want float64) bool { return math.Abs(got-want) < tol }

func TestDailyReturns(t *testing.T) {
	tests := []struct {
		name   string
		equity []float64
		want   []float64
	}{
		{"empty", nil, nil},
		{"single point has no return", []float64{100}, nil},
		{"up then down", []float64{100, 110, 99}, []float64{0.1, -0.1}},
		{"flat curve returns zeros, not nothing", []float64{100, 100}, []float64{0}},
		// The zero-opening guard: the first period is skipped entirely, so the
		// result is SHORTER than len(equity)-1. A return of 0 here would claim
		// nothing happened between being worth nothing and being worth 100.
		{"zero opening value is skipped", []float64{0, 100, 110}, []float64{0.1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DailyReturns(tt.equity)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v (len %d), want %v (len %d)", got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if !closeTo(got[i], tt.want[i]) {
					t.Errorf("index %d: got %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSharpe(t *testing.T) {
	// returns 0.2 and 0.1 -> mean 0.15, population stdev 0.05,
	// so 0.15/0.05 * sqrt(252) = 3 * sqrt(252).
	wantVarying := 3 * math.Sqrt(252)

	tests := []struct {
		name   string
		equity []float64
		want   float64
	}{
		{"empty curve", nil, 0},
		{"single point", []float64{100}, 0},
		{"varying returns", []float64{100, 120, 132}, wantVarying},
		// Zero variance must be a defined 0, never NaN and never a division by
		// zero -- a flat curve is a real outcome, not missing data.
		{"flat curve has zero variance", []float64{100, 100, 100}, 0},
		// THE single-return case, and the reason StdevPopulation divides by n.
		// With a sample stdev's n-1 denominator this is sqrt(0/0) = NaN, and
		// "not enough data" silently becomes a NaN in a user-facing metric.
		{"exactly one usable return", []float64{100, 110}, 0},
		// Same case reached a different way: the zero-opening guard drops the
		// first period, leaving one return behind it.
		{"one return after a zero opening", []float64{0, 100, 110}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Sharpe(tt.equity)
			if math.IsNaN(got) {
				t.Fatalf("got NaN, want %v -- Sharpe must never return NaN", tt.want)
			}
			if !closeTo(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMean(t *testing.T) {
	if got := Mean([]float64{1, 2, 3, 4}); !closeTo(got, 2.5) {
		t.Errorf("got %v, want 2.5", got)
	}
	if got := Mean([]float64{-1, 1}); !closeTo(got, 0) {
		t.Errorf("got %v, want 0", got)
	}
}

func TestStdevPopulation(t *testing.T) {
	// The textbook population-stdev fixture: mean 5, squared deviations
	// summing to 32, divided by n=8 gives variance 4 and stdev exactly 2.
	// A sample stdev (n-1) would give sqrt(32/7) = 2.13..., so this fixture
	// fails loudly if the denominator is ever changed.
	xs := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	if got := StdevPopulation(xs, Mean(xs)); !closeTo(got, 2) {
		t.Errorf("got %v, want exactly 2 (population stdev, divided by n)", got)
	}

	// One element: population stdev is a defined 0. A sample stdev divides by
	// zero here, which is what Sharpe's single-return case depends on avoiding.
	if got := StdevPopulation([]float64{0.1}, 0.1); got != 0 {
		t.Errorf("got %v, want 0 for a single element", got)
	}
}

func TestMaxDrawdownPct(t *testing.T) {
	tests := []struct {
		name   string
		equity []float64
		want   float64
	}{
		{"empty", nil, 0},
		// Never negative: a curve that only rises has no drawdown at all.
		{"monotonically rising", []float64{100, 110, 120}, 0},
		{"flat", []float64{100, 100, 100}, 0},
		{"peak 120 to trough 90", []float64{100, 120, 90, 150}, 25},
		// The deepest decline anywhere, not the last one or the final gap.
		{"deepest of two declines", []float64{100, 50, 100, 80}, 50},
		// A wholly negative curve cannot express a meaningful percentage
		// decline; the peak<=0 guard makes it 0 rather than a nonsense number.
		{"non-positive curve", []float64{-10, -5}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaxDrawdownPct(tt.equity)
			if got < 0 {
				t.Fatalf("got %v -- a drawdown is never negative", got)
			}
			if !closeTo(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
