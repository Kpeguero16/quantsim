package service

import (
	"math"
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

func mustNewMACrossover(t *testing.T, shortWindow, longWindow int) Strategy {
	t.Helper()
	s, err := newMACrossover(shortWindow, longWindow)
	if err != nil {
		t.Fatalf("newMACrossover(%d, %d): unexpected error: %v", shortWindow, longWindow, err)
	}
	return s
}

// TestGenerateSignals_SingleCleanCrossover hand-verifies GenerateSignals
// against a bar series constructed so the short/long MAs cross exactly once
// in each direction -- a golden cross at index 4, a death cross at index 9 --
// with every other bar producing SignalNone. Unchanged from Step 16 except
// that it now goes through the Strategy interface (SPEC.md Step 18 §2.1)
// rather than a free function -- the assertions are byte-identical, proving
// the refactor changed no behavior.
func TestGenerateSignals_SingleCleanCrossover(t *testing.T) {
	closes := []float64{10, 10, 10, 10, 20, 30, 40, 50, 40, 30, 20, 10}
	bars := barsFromCloses(closes)

	signals := mustNewMACrossover(t, 2, 3).GenerateSignals(bars)

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

	signals := mustNewMACrossover(t, 2, 4).GenerateSignals(bars)

	for i := 0; i < 3; i++ {
		if signals[i] != SignalNone {
			t.Errorf("signal[%d] = %v, want SignalNone (long MA not yet formed)", i, signals[i])
		}
	}
}

// TestNewMACrossover_RejectsInvalidWindowParams covers the parameter bounds
// NewStrategy/newMACrossover reject at construction (SPEC.md Step 18 §2.1):
// concentrated here now, rather than tolerated as all-SignalNone by a free
// GenerateSignals function the way Step 16 originally handled bad input --
// a deliberate design change (§2.1), not an accidental one.
func TestNewMACrossover_RejectsInvalidWindowParams(t *testing.T) {
	cases := []struct {
		name        string
		shortWindow int
		longWindow  int
	}{
		{"short window zero", 0, 3},
		{"short window negative", -1, 3},
		{"long window equal to short", 2, 2},
		{"long window less than short", 3, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newMACrossover(tc.shortWindow, tc.longWindow); err == nil {
				t.Fatalf("newMACrossover(%d, %d): got no error, want ErrInvalidRequest", tc.shortWindow, tc.longWindow)
			}
		})
	}
}

// TestMACrossover_GenerateSignals_LongWindowExceedingDataProducesNoSignals:
// a validly constructed strategy asked to generate signals over fewer bars
// than its own long_window needs must not panic or index out of range --
// GenerateSignals stays defensive even though RunBacktest's WarmupBars()
// check (SPEC.md §2.4) is expected to catch this first in production.
func TestMACrossover_GenerateSignals_LongWindowExceedingDataProducesNoSignals(t *testing.T) {
	bars := barsFromCloses([]float64{1, 2, 3, 4, 5})

	signals := mustNewMACrossover(t, 2, 10).GenerateSignals(bars)

	if len(signals) != len(bars) {
		t.Fatalf("got %d signals, want %d", len(signals), len(bars))
	}
	for i, s := range signals {
		if s != SignalNone {
			t.Errorf("signal[%d] = %v, want SignalNone", i, s)
		}
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

	signals := mustNewMACrossover(t, 2, 3).GenerateSignals(bars)

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

	signals := mustNewMACrossover(t, 2, 3).GenerateSignals(bars)

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

// TestNewStrategy_MACrossover_RoundTripsThroughJSON: NewStrategy's job is to
// decode raw JSON into the same thing newMACrossover builds directly --
// asserted by checking Kind, WarmupBars and that Params re-encodes the same
// values (SPEC.md §2.6: params is re-marshaled canonically, never the raw
// bytes received).
func TestNewStrategy_MACrossover_RoundTripsThroughJSON(t *testing.T) {
	strat, err := NewStrategy(StrategyMACrossover, []byte(`{"short_window": 5, "long_window": 20}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strat.Kind() != StrategyMACrossover {
		t.Errorf("Kind() = %v, want %v", strat.Kind(), StrategyMACrossover)
	}
	if strat.WarmupBars() != 20 {
		t.Errorf("WarmupBars() = %d, want 20", strat.WarmupBars())
	}
	if got := string(strat.Params()); got != `{"short_window":5,"long_window":20}` {
		t.Errorf("Params() = %s, want canonical {short_window,long_window} JSON", got)
	}
}

// TestNewStrategy_UnknownKindIsInvalidRequest: a kind string matching none
// of the three named strategies must be rejected rather than panicking on a
// nil case.
func TestNewStrategy_UnknownKindIsInvalidRequest(t *testing.T) {
	if _, err := NewStrategy("not-a-real-strategy", []byte(`{}`)); err == nil {
		t.Fatal("NewStrategy(not-a-real-strategy): got no error, want ErrInvalidRequest")
	}
}

// TestNewStrategy_MalformedParamsIsInvalidRequest: params that don't even
// parse as the expected object shape must fail the same way an
// out-of-bounds value does -- one error path, not a JSON panic.
func TestNewStrategy_MalformedParamsIsInvalidRequest(t *testing.T) {
	if _, err := NewStrategy(StrategyMACrossover, []byte(`not json`)); err == nil {
		t.Fatal("got no error for malformed params, want ErrInvalidRequest")
	}
}

func mustNewRSIStrategy(t *testing.T, period int, oversold, overbought float64) Strategy {
	t.Helper()
	s, err := newRSIStrategy(period, oversold, overbought)
	if err != nil {
		t.Fatalf("newRSIStrategy(%d, %v, %v): unexpected error: %v", period, oversold, overbought, err)
	}
	return s
}

func TestNewRSIStrategy_RejectsInvalidParams(t *testing.T) {
	cases := []struct {
		name       string
		period     int
		oversold   float64
		overbought float64
	}{
		{"period zero", 0, 30, 70},
		{"period one", 1, 30, 70},
		{"oversold zero", 3, 0, 70},
		{"oversold negative", 3, -5, 70},
		{"oversold equal to overbought", 3, 50, 50},
		{"oversold greater than overbought", 3, 80, 70},
		{"overbought at 100", 3, 30, 100},
		{"overbought over 100", 3, 30, 110},
		{"period over maxWarmupBars", 501, 30, 70},
		// A period this large overflows WarmupBars' period+1 to a negative
		// number, which would silently pass NewStrategy's
		// "WarmupBars() > maxWarmupBars" check if this constructor didn't
		// reject it first.
		{"period near MaxInt overflows WarmupBars", math.MaxInt, 30, 70},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newRSIStrategy(tc.period, tc.oversold, tc.overbought); err == nil {
				t.Fatalf("newRSIStrategy(%d, %v, %v): got no error, want ErrInvalidRequest",
					tc.period, tc.oversold, tc.overbought)
			}
		})
	}
}

func TestRSIStrategy_WarmupBars(t *testing.T) {
	strat := mustNewRSIStrategy(t, 14, 30, 70)
	if got := strat.WarmupBars(); got != 15 {
		t.Errorf("WarmupBars() = %d, want 15 (period+1)", got)
	}
}

// TestRSIStrategy_GenerateSignals_ZoneExits reuses the exact closes/period
// fixture indicators_test.go's TestWilderRSI_HandComputedFixture already
// verified (RSI at indices 3..6 = 80, 61.538..., 77.273..., 59.130...), so
// the expected crossings here follow from already-checked numbers rather
// than a second independent guess:
//
//	i=4: prev=80 >= overbought(75), cur=61.538 < 75  -> Sell
//	i=5: prev=61.538 <= oversold(65), cur=77.273 > 65 -> Buy
//	i=6: prev=77.273 >= overbought(75), cur=59.130 < 75 -> Sell
func TestRSIStrategy_GenerateSignals_ZoneExits(t *testing.T) {
	closes := []float64{10, 12, 11, 13, 12, 14, 13}
	bars := barsFromCloses(closes)

	signals := mustNewRSIStrategy(t, 3, 65, 75).GenerateSignals(bars)

	want := map[int]Signal{4: SignalSell, 5: SignalBuy, 6: SignalSell}
	for i, s := range signals {
		if s != want[i] {
			t.Errorf("signal[%d] = %v, want %v", i, s, want[i])
		}
	}
}

// TestRSIStrategy_GenerateSignals_NoCrossingProducesNoSignals: thresholds
// wide enough that RSI never exits either zone must produce every bar as
// SignalNone, not a false fire from a loosely written condition.
func TestRSIStrategy_GenerateSignals_NoCrossingProducesNoSignals(t *testing.T) {
	closes := []float64{10, 12, 11, 13, 12, 14, 13}
	bars := barsFromCloses(closes)

	signals := mustNewRSIStrategy(t, 3, 1, 99).GenerateSignals(bars)

	for i, s := range signals {
		if s != SignalNone {
			t.Errorf("signal[%d] = %v, want SignalNone", i, s)
		}
	}
}

// TestRSIStrategy_GenerateSignals_WarmupExceedingDataProducesNoSignals
// mirrors maCrossover's own defensive behavior (SPEC.md §2.1's shared
// GenerateSignals contract): fewer bars than WarmupBars requires must not
// panic or index out of range.
func TestRSIStrategy_GenerateSignals_WarmupExceedingDataProducesNoSignals(t *testing.T) {
	bars := barsFromCloses([]float64{10, 11, 12})

	signals := mustNewRSIStrategy(t, 3, 30, 70).GenerateSignals(bars)

	if len(signals) != len(bars) {
		t.Fatalf("got %d signals, want %d", len(signals), len(bars))
	}
	for i, s := range signals {
		if s != SignalNone {
			t.Errorf("signal[%d] = %v, want SignalNone", i, s)
		}
	}
}

// TestNewStrategy_RSI_RoundTripsThroughJSON mirrors the MA crossover
// round-trip test: Kind, WarmupBars and Params' canonical re-encoding all
// have to agree with what newRSIStrategy builds directly.
func TestNewStrategy_RSI_RoundTripsThroughJSON(t *testing.T) {
	strat, err := NewStrategy(StrategyRSI, []byte(`{"period": 14, "oversold": 30, "overbought": 70}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strat.Kind() != StrategyRSI {
		t.Errorf("Kind() = %v, want %v", strat.Kind(), StrategyRSI)
	}
	if strat.WarmupBars() != 15 {
		t.Errorf("WarmupBars() = %d, want 15", strat.WarmupBars())
	}
	if got := string(strat.Params()); got != `{"period":14,"oversold":30,"overbought":70}` {
		t.Errorf("Params() = %s, want canonical {period,oversold,overbought} JSON", got)
	}
}

func mustNewMACDStrategy(t *testing.T, fast, slow, signal int) Strategy {
	t.Helper()
	s, err := newMACDStrategy(fast, slow, signal)
	if err != nil {
		t.Fatalf("newMACDStrategy(%d, %d, %d): unexpected error: %v", fast, slow, signal, err)
	}
	return s
}

func TestNewMACDStrategy_RejectsInvalidParams(t *testing.T) {
	cases := []struct {
		name   string
		fast   int
		slow   int
		signal int
	}{
		{"fast zero", 0, 26, 9},
		{"fast one", 1, 26, 9},
		{"slow equal to fast", 12, 12, 9},
		{"slow less than fast", 26, 12, 9},
		{"signal zero", 12, 26, 0},
		{"signal one", 12, 26, 1},
		{"slow over maxWarmupBars", 12, 501, 9},
		{"signal over maxWarmupBars", 12, 26, 501},
		// slow_period this large overflows WarmupBars' slow+signal-1 to a
		// negative number, which would silently pass NewStrategy's
		// "WarmupBars() > maxWarmupBars" check if this constructor didn't
		// reject it first.
		{"slow near MaxInt overflows WarmupBars", 2, math.MaxInt, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newMACDStrategy(tc.fast, tc.slow, tc.signal); err == nil {
				t.Fatalf("newMACDStrategy(%d, %d, %d): got no error, want ErrInvalidRequest",
					tc.fast, tc.slow, tc.signal)
			}
		})
	}
}

func TestMACDStrategy_WarmupBars(t *testing.T) {
	cases := []struct {
		fast, slow, signal int
		want               int
	}{
		{12, 26, 9, 34},
		{2, 4, 2, 5},
		{2, 3, 2, 4},
	}
	for _, tc := range cases {
		strat := mustNewMACDStrategy(t, tc.fast, tc.slow, tc.signal)
		if got := strat.WarmupBars(); got != tc.want {
			t.Errorf("WarmupBars(%d,%d,%d) = %d, want %d", tc.fast, tc.slow, tc.signal, got, tc.want)
		}
	}
}

// TestMACDStrategy_GenerateSignals_ConstantMACDNeverSignals: a pure linear
// price ramp makes both EMAs converge to a constant lag behind price, so
// the MACD line -- and therefore the signal line, an EMA of a constant
// input -- settles to an exact constant (5, verified by hand below) with no
// crossing anywhere. This is a strong regression check for the offset
// arithmetic GenerateSignals does when slicing macdLine for the signal-line
// EMA and re-aligning the result: an off-by-one there would compare a real
// MACD value against an unwritten-zero placeholder at the boundary,
// producing a spurious signal a constant series must never produce.
//
// closes = [10,20,...,100], fast=3 (alpha .5), slow=4 (alpha .4)
// fastEMA settles to price - 10 exactly; slowEMA settles to price - 15
// exactly; their difference is a constant 5 from bar index 3 onward.
// signal = ema(constant 5, period 3) is also exactly 5 throughout.
func TestMACDStrategy_GenerateSignals_ConstantMACDNeverSignals(t *testing.T) {
	closes := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	bars := barsFromCloses(closes)

	signals := mustNewMACDStrategy(t, 3, 4, 3).GenerateSignals(bars)

	if len(signals) != len(bars) {
		t.Fatalf("got %d signals, want %d", len(signals), len(bars))
	}
	for i, s := range signals {
		if s != SignalNone {
			t.Errorf("signal[%d] = %v, want SignalNone (constant MACD never crosses)", i, s)
		}
	}
}

// TestMACDStrategy_GenerateSignals_BullishCrossAfterAReversal: a price
// series that declines then reverses upward, hand-computed (and
// cross-checked with an independent script) through both EMAs, the MACD
// line, and the signal line -- verifying an actual crossing is detected at
// the right bar, not just that a non-crossing series stays quiet.
//
// closes = [100,90,80,70,60,70,80,90,100,110], fast=2, slow=4, signal=2
// macd   @ i=4: -10.0,      signal @ i=4: -10.0       (tie, no signal -- first compared bar)
// macd   @ i=5: -4.66667,   signal @ i=5: -6.44444     -> BUY (macd crosses above signal)
// every bar after i=5 stays with macd above signal (both trending up
// together as the reversal continues), so no further signal fires.
func TestMACDStrategy_GenerateSignals_BullishCrossAfterAReversal(t *testing.T) {
	closes := []float64{100, 90, 80, 70, 60, 70, 80, 90, 100, 110}
	bars := barsFromCloses(closes)

	signals := mustNewMACDStrategy(t, 2, 4, 2).GenerateSignals(bars)

	want := map[int]Signal{5: SignalBuy}
	for i, s := range signals {
		if s != want[i] {
			t.Errorf("signal[%d] = %v, want %v", i, s, want[i])
		}
	}
}

// TestMACDStrategy_GenerateSignals_WarmupExceedingDataProducesNoSignals
// mirrors the other two strategies' defensive contract.
func TestMACDStrategy_GenerateSignals_WarmupExceedingDataProducesNoSignals(t *testing.T) {
	bars := barsFromCloses([]float64{10, 11, 12})

	signals := mustNewMACDStrategy(t, 2, 3, 2).GenerateSignals(bars)

	if len(signals) != len(bars) {
		t.Fatalf("got %d signals, want %d", len(signals), len(bars))
	}
	for i, s := range signals {
		if s != SignalNone {
			t.Errorf("signal[%d] = %v, want SignalNone", i, s)
		}
	}
}

// TestNewStrategy_MACD_RoundTripsThroughJSON mirrors the other two
// strategies' round-trip test.
func TestNewStrategy_MACD_RoundTripsThroughJSON(t *testing.T) {
	strat, err := NewStrategy(StrategyMACD, []byte(`{"fast_period": 12, "slow_period": 26, "signal_period": 9}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strat.Kind() != StrategyMACD {
		t.Errorf("Kind() = %v, want %v", strat.Kind(), StrategyMACD)
	}
	if strat.WarmupBars() != 34 {
		t.Errorf("WarmupBars() = %d, want 34", strat.WarmupBars())
	}
	if got := string(strat.Params()); got != `{"fast_period":12,"slow_period":26,"signal_period":9}` {
		t.Errorf("Params() = %s, want canonical {fast_period,slow_period,signal_period} JSON", got)
	}
}

// TestNewStrategy_WarmupBarsAtTheBoundIsAccepted: exactly maxWarmupBars is
// still valid -- only exceeding it is rejected.
func TestNewStrategy_WarmupBarsAtTheBoundIsAccepted(t *testing.T) {
	strat, err := NewStrategy(StrategyMACrossover, []byte(`{"short_window": 2, "long_window": 500}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strat.WarmupBars() != maxWarmupBars {
		t.Fatalf("WarmupBars() = %d, want %d", strat.WarmupBars(), maxWarmupBars)
	}
}

func TestNewStrategy_MACrossover_WarmupExceedsMaxIsInvalid(t *testing.T) {
	if _, err := NewStrategy(StrategyMACrossover, []byte(`{"short_window": 2, "long_window": 501}`)); err == nil {
		t.Fatal("got no error for long_window=501, want ErrInvalidRequest (exceeds maxWarmupBars)")
	}
}

// TestNewStrategy_MACD_WarmupExceedsMaxEvenWhenSlowAlonePasses is the exact
// case maxWarmupBars exists to catch (SPEC.md §2.4): slow_period=480 alone
// is comfortably under 500, but slow+signal-1 = 480+30-1 = 509 is not. A
// bound checked only against slow_period would miss this entirely.
func TestNewStrategy_MACD_WarmupExceedsMaxEvenWhenSlowAlonePasses(t *testing.T) {
	if _, err := NewStrategy(StrategyMACD, []byte(`{"fast_period": 2, "slow_period": 480, "signal_period": 30}`)); err == nil {
		t.Fatal("got no error for slow=480/signal=30 (WarmupBars=509), want ErrInvalidRequest")
	}
}

// TestNewStrategy_MACD_WarmupAtTheBoundIsAccepted: slow+signal-1 landing
// exactly on 500 (471+30-1) must still succeed.
func TestNewStrategy_MACD_WarmupAtTheBoundIsAccepted(t *testing.T) {
	strat, err := NewStrategy(StrategyMACD, []byte(`{"fast_period": 2, "slow_period": 471, "signal_period": 30}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strat.WarmupBars() != maxWarmupBars {
		t.Fatalf("WarmupBars() = %d, want %d", strat.WarmupBars(), maxWarmupBars)
	}
}
