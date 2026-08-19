package service

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// TestEMA_HandComputedFixture_Period3: values are an arithmetic progression
// and period=3 gives alpha=0.5 exactly, so every result is an exact integer
// -- no floating-point tolerance needed, which makes a wrong seed or a wrong
// alpha immediately visible rather than lost in rounding.
//
// values  = [10, 12, 14, 16, 18, 20]
// seed (SMA of first 3, at index 2) = (10+12+14)/3 = 12
// ema[3] = 16*0.5 + 12*0.5 = 14
// ema[4] = 18*0.5 + 14*0.5 = 16
// ema[5] = 20*0.5 + 16*0.5 = 18
func TestEMA_HandComputedFixture_Period3(t *testing.T) {
	values := []float64{10, 12, 14, 16, 18, 20}
	want := []float64{0, 0, 12, 14, 16, 18}

	got := ema(values, 3)

	if len(got) != len(want) {
		t.Fatalf("got %d values, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ema[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestEMA_HandComputedFixture_Period4: a second, independent fixture with a
// different period and a different progression, to catch a bug that
// happens to cancel out against the period-3 case above.
//
// values = [10, 20, 30, 40, 50, 60], period=4, alpha=0.4
// seed (SMA of first 4, at index 3) = (10+20+30+40)/4 = 25
// ema[4] = 50*0.4 + 25*0.6 = 35
// ema[5] = 60*0.4 + 35*0.6 = 45
func TestEMA_HandComputedFixture_Period4(t *testing.T) {
	values := []float64{10, 20, 30, 40, 50, 60}
	want := []float64{0, 0, 0, 25, 35, 45}

	got := ema(values, 4)

	if len(got) != len(want) {
		t.Fatalf("got %d values, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ema[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestEMA_DegenerateWindows(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	cases := []struct {
		name   string
		period int
	}{
		{"zero period", 0},
		{"negative period", -1},
		{"period exceeds data", 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ema(values, tc.period)
			if len(got) != len(values) {
				t.Fatalf("got %d values, want %d", len(got), len(values))
			}
			for i, v := range got {
				if v != 0 {
					t.Errorf("ema[%d] = %v, want 0", i, v)
				}
			}
		})
	}
}

// TestWilderRSI_HandComputedFixture carries a period-3 RSI through its seed
// bar and three further smoothed bars, hand-computed via Wilder's own
// recurrence (avg = (avg*(period-1) + current)/period) rather than a simple
// average -- the distinguishing feature this test exists to catch a
// regression in.
//
// closes = [10, 12, 11, 13, 12, 14, 13], period = 3
// deltas at bars 1..6: +2, -1, +2, -1, +2, -1
//
// Seed (bars 1-3): avgGain = (2+0+2)/3 = 4/3, avgLoss = (0+1+0)/3 = 1/3
//
//	RS = 4, RSI[3] = 100 - 100/5 = 80
//
// Bar 4 (delta -1): avgGain = (4/3*2 + 0)/3 = 8/9, avgLoss = (1/3*2 + 1)/3 = 5/9
//
//	RS = 1.6, RSI[4] = 100 - 100/2.6 = 800/13 ≈ 61.53846153846154
//
// Bar 5 (delta +2): avgGain = (8/9*2 + 2)/3 = 34/27, avgLoss = (5/9*2 + 0)/3 = 10/27
//
//	RS = 3.4, RSI[5] = 100 - 100/4.4 = 850/11 ≈ 77.27272727272727
//
// Bar 6 (delta -1): avgGain = (34/27*2 + 0)/3 = 68/81, avgLoss = (10/27*2 + 1)/3 = 47/81
//
//	RS = 68/47, RSI[6] = 100*68/115 ≈ 59.130434782608695
func TestWilderRSI_HandComputedFixture(t *testing.T) {
	closes := []float64{10, 12, 11, 13, 12, 14, 13}
	want := map[int]float64{
		3: 80,
		4: 800.0 / 13.0,
		5: 850.0 / 11.0,
		6: 6800.0 / 115.0,
	}

	got := wilderRSI(closes, 3)

	if len(got) != len(closes) {
		t.Fatalf("got %d values, want %d", len(got), len(closes))
	}
	for i := 0; i < 3; i++ {
		if got[i] != 0 {
			t.Errorf("rsi[%d] = %v, want 0 (before the seed bar)", i, got[i])
		}
	}
	for i, w := range want {
		if !almostEqual(got[i], w) {
			t.Errorf("rsi[%d] = %v, want %v", i, got[i], w)
		}
	}
}

// TestWilderRSI_DegenerateWindows covers the three cases §2.2 defines
// explicitly rather than leaving to a 0/0 or a divide-by-zero: an
// all-gains window, an all-losses window, and a perfectly flat window.
func TestWilderRSI_DegenerateWindows(t *testing.T) {
	cases := []struct {
		name   string
		closes []float64
		want   float64
	}{
		{"all gains", []float64{10, 11, 12, 13}, 100},
		{"all losses", []float64{10, 9, 8, 7}, 0},
		{"perfectly flat", []float64{10, 10, 10, 10}, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wilderRSI(tc.closes, 3)
			if !almostEqual(got[3], tc.want) {
				t.Errorf("rsi[3] = %v, want %v", got[3], tc.want)
			}
		})
	}
}

func TestWilderRSI_InvalidPeriods(t *testing.T) {
	closes := []float64{1, 2, 3, 4, 5}
	cases := []struct {
		name   string
		period int
	}{
		{"zero period", 0},
		{"negative period", -1},
		{"period equals data length", 5},
		{"period exceeds data", 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wilderRSI(closes, tc.period)
			if len(got) != len(closes) {
				t.Fatalf("got %d values, want %d", len(got), len(closes))
			}
			for i, v := range got {
				if v != 0 {
					t.Errorf("rsi[%d] = %v, want 0", i, v)
				}
			}
		})
	}
}
