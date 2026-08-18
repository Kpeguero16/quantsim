package service

import (
	"testing"
	"time"
)

func barsFromCloses(closes []float64) []Bar {
	bars := make([]Bar, len(closes))
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range closes {
		bars[i] = Bar{Timestamp: start.AddDate(0, 0, i), Open: c, Close: c}
	}
	return bars
}

// TestGenerateSignals_SingleCleanCrossover hand-verifies GenerateSignals
// against a bar series constructed so the short/long MAs cross exactly once
// in each direction -- a golden cross at index 4, a death cross at index 9 --
// with every other bar producing SignalNone.
func TestGenerateSignals_SingleCleanCrossover(t *testing.T) {
	closes := []float64{10, 10, 10, 10, 20, 30, 40, 50, 40, 30, 20, 10}
	bars := barsFromCloses(closes)

	signals := GenerateSignals(bars, 2, 3)

	if len(signals) != len(bars) {
		t.Fatalf("got %d signals, want %d (one per bar)", len(signals), len(bars))
	}
	for i, s := range signals {
		want := SignalNone
		switch i {
		case 4:
			want = SignalBuy
		case 9:
			want = SignalSell
		}
		if s != want {
			t.Errorf("signal[%d] = %v, want %v", i, s, want)
		}
	}
}

// TestGenerateSignals_NothingBeforeTheLongWindow: there is no long MA to
// cross against until long_window bars exist, so every earlier bar must be
// SignalNone regardless of price movement.
func TestGenerateSignals_NothingBeforeTheLongWindow(t *testing.T) {
	closes := []float64{1, 100, 1, 100, 1}
	bars := barsFromCloses(closes)

	signals := GenerateSignals(bars, 2, 4)

	for i := 0; i < 3; i++ {
		if signals[i] != SignalNone {
			t.Errorf("signal[%d] = %v, want SignalNone (long MA not yet formed)", i, signals[i])
		}
	}
}

// TestGenerateSignals_InvalidWindowsProduceNoSignals covers the guard
// clauses directly: a non-positive short window, a long window that does not
// exceed the short one, and a long window longer than the data. None of
// these should panic or index out of range.
func TestGenerateSignals_InvalidWindowsProduceNoSignals(t *testing.T) {
	bars := barsFromCloses([]float64{1, 2, 3, 4, 5})

	cases := []struct {
		name        string
		shortWindow int
		longWindow  int
	}{
		{"short window zero", 0, 3},
		{"short window negative", -1, 3},
		{"long window equal to short", 2, 2},
		{"long window less than short", 3, 2},
		{"long window exceeds data", 2, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signals := GenerateSignals(bars, tc.shortWindow, tc.longWindow)
			for i, s := range signals {
				if s != SignalNone {
					t.Errorf("signal[%d] = %v, want SignalNone", i, s)
				}
			}
		})
	}
}

// TestGenerateSignals_AlreadyAboveOnTheFirstEligibleBarFiresNoSignal: if the
// short MA is already above the long MA the moment both first exist, that is
// not a crossing -- there is no prior bar to have crossed from, so GenerateSignals
// must not treat "no established side yet" as if it were "was below." A
// mutation that drops the haveState guard passes every other test in this
// file (their series happen to start tied or below) but fires a false signal
// here.
func TestGenerateSignals_AlreadyAboveOnTheFirstEligibleBarFiresNoSignal(t *testing.T) {
	// Strictly increasing closes: at i=longWindow-1, short(2) is already
	// greater than long(3) -- above is true on the very first eligible bar.
	closes := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	bars := barsFromCloses(closes)

	signals := GenerateSignals(bars, 2, 3)

	for i, s := range signals {
		if s != SignalNone {
			t.Errorf("signal[%d] = %v, want SignalNone -- a monotonically increasing series never crosses, it starts above", i, s)
		}
	}
}

// TestGenerateSignals_RepeatedFireOnlyAtTheCrossingBar: once the MAs cross,
// they stay on the same side for several bars in this series. Only the
// crossing bar itself may carry a signal -- every bar after it on the same
// side must be SignalNone, or Simulate would see a buy signal it has already
// acted on and (depending on its own state) could act on again.
func TestGenerateSignals_RepeatedFireOnlyAtTheCrossingBar(t *testing.T) {
	closes := []float64{10, 10, 10, 20, 30, 40, 50, 60, 70, 80}
	bars := barsFromCloses(closes)

	signals := GenerateSignals(bars, 2, 3)

	fired := 0
	for _, s := range signals {
		if s != SignalNone {
			fired++
		}
	}
	if fired != 1 {
		t.Fatalf("got %d fired signals in a monotonic run, want exactly 1 (the crossing bar)", fired)
	}
}
