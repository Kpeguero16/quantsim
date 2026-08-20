package service

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

// simulateSingle runs one symbol through the portfolio engine. Step 16 wrote
// every expectation in this file against Simulate; Step 19 deleted that
// function and retargeted the calls here WITHOUT touching a single expected
// number (plan D1). That is the point: the numbers are the invariant, and a
// single-symbol run is the len(symbols)==1 case of SimulatePortfolio rather
// than a mode of its own. If one of them ever has to change, the behavior
// changed, and the change is a bug until proven otherwise.
func simulateSingle(t *testing.T, bars []Bar, signals []Signal, startingCapital float64) SimulationResult {
	t.Helper()
	symbols, portBars, portSignals := buildPortfolio(t,
		portfolioFixture{symbol: "AAPL", bars: bars, signals: signals})
	return SimulatePortfolio(symbols, portBars, portSignals, startingCapital)
}

// openCloseBars builds bars with distinct Open/Close per bar, so a test can
// prove which one a fill actually used.
func openCloseBars(pairs [][2]float64) []Bar {
	bars := make([]Bar, len(pairs))
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, p := range pairs {
		bars[i] = Bar{Timestamp: start.AddDate(0, 0, i), Open: p[0], Close: p[1]}
	}
	return bars
}

// TestSimulate_FillsAtNextBarsOpen is the direct test of SPEC.md §2.4's
// lookahead-avoidance rule: a signal generated from bar i's data fills at bar
// i+1's OPEN, never bar i's own close and never bar i+1's close.
func TestSimulate_FillsAtNextBarsOpen(t *testing.T) {
	bars := openCloseBars([][2]float64{
		{100, 101}, // i0
		{102, 103}, // i1 -- signals[1] = Buy, so the fill is at i2's Open (104), not i1's Close (103) or i2's Close (105)
		{104, 105}, // i2
		{106, 107}, // i3 -- signals[3] = Sell, fill at i4's Open (108)
		{108, 109}, // i4
	})
	signals := []Signal{SignalNone, SignalBuy, SignalNone, SignalSell, SignalNone}
	startingCapital := 1000.0

	result := simulateSingle(t, bars, signals, startingCapital)

	if len(result.Trades) != 2 {
		t.Fatalf("got %d trades, want 2 (one buy, one sell)", len(result.Trades))
	}

	buy := result.Trades[0]
	if buy.Side != SideBuy || !approxEqual(buy.Price, 104) {
		t.Errorf("buy fill = %+v, want price 104 (bar i2's open)", buy)
	}
	wantQty := startingCapital / 104
	if !approxEqual(buy.Quantity, wantQty) {
		t.Errorf("buy quantity = %v, want %v (all-in at the fill price)", buy.Quantity, wantQty)
	}

	sell := result.Trades[1]
	if sell.Side != SideSell || !approxEqual(sell.Price, 108) {
		t.Errorf("sell fill = %+v, want price 108 (bar i4's open)", sell)
	}
	if sell.RealizedPL == nil {
		t.Fatal("sell trade has no RealizedPL")
	}
	wantPL := (108 - 104) * wantQty
	if !approxEqual(*sell.RealizedPL, wantPL) {
		t.Errorf("realized P/L = %v, want %v", *sell.RealizedPL, wantPL)
	}

	if !approxEqual(result.FinalEquity, sell.Quantity*108) {
		t.Errorf("final equity = %v, want %v (all cash after the sell)", result.FinalEquity, sell.Quantity*108)
	}
	if result.FinalEquity <= startingCapital {
		t.Errorf("final equity %v should exceed starting capital %v for this profitable round trip", result.FinalEquity, startingCapital)
	}
}

// TestSimulate_SignalOnTheLastBarNeverFills: SPEC.md §2.4 states this
// explicitly -- there is no bar after the last one to open at, so a signal
// there costs the range rather than being filled anyway at a stale price.
func TestSimulate_SignalOnTheLastBarNeverFills(t *testing.T) {
	bars := openCloseBars([][2]float64{{100, 101}, {102, 103}, {104, 105}})
	signals := []Signal{SignalNone, SignalNone, SignalBuy}

	result := simulateSingle(t, bars, signals, 1000)

	if len(result.Trades) != 0 {
		t.Fatalf("got %d trades, want 0 -- the final bar's signal has no next bar to fill at", len(result.Trades))
	}
	if !approxEqual(result.FinalEquity, 1000) {
		t.Errorf("final equity = %v, want 1000 (never invested)", result.FinalEquity)
	}
}

// TestSimulate_BuySignalWhileAlreadyHoldingIsANoOp: §2.3's one-open-position
// rule. A second buy signal while already invested must not add a second
// trade or disturb the existing position's cost basis.
func TestSimulate_BuySignalWhileAlreadyHoldingIsANoOp(t *testing.T) {
	bars := openCloseBars([][2]float64{{100, 101}, {102, 103}, {104, 105}})
	signals := []Signal{SignalBuy, SignalBuy, SignalNone}

	result := simulateSingle(t, bars, signals, 1000)

	if len(result.Trades) != 1 {
		t.Fatalf("got %d trades, want 1 -- the second buy signal fires while already holding and must be a no-op", len(result.Trades))
	}
}

// TestSimulate_SellSignalWhileFlatIsANoOp is the mirror of the above: a sell
// signal with nothing held must not fabricate a trade or move cash.
func TestSimulate_SellSignalWhileFlatIsANoOp(t *testing.T) {
	bars := openCloseBars([][2]float64{{100, 101}, {102, 103}})
	signals := []Signal{SignalSell, SignalNone}

	result := simulateSingle(t, bars, signals, 1000)

	if len(result.Trades) != 0 {
		t.Fatalf("got %d trades, want 0 -- nothing was held to sell", len(result.Trades))
	}
	if !approxEqual(result.FinalEquity, 1000) {
		t.Errorf("final equity = %v, want 1000 (cash untouched)", result.FinalEquity)
	}
}

// TestSimulate_TradesIsNeverNilEvenWithZeroTrades guards a real bug this
// suite's own len()-only assertions above didn't catch: a `var trades
// []TradeRecord` (nil until the first append) is indistinguishable from an
// empty slice by len(), but not by encoding/json -- a nil slice marshals as
// `null`, an empty one as `[]`. BacktestDetail.Trades is a JSON array field
// on the wire (services/backtesting/internal/service/types.go), and the
// frontend's TradeLogTable calls `.length` on it unconditionally, matching
// the same "list responses are never null" rule ListBacktests' handler
// already enforces for the collection endpoint and GetBacktest's store path
// already enforces by starting from `[]service.TradeRecord{}`. This is the
// one place that didn't, reachable whenever a run's date range has no room
// for GenerateSignals to fire even once.
func TestSimulate_TradesIsNeverNilEvenWithZeroTrades(t *testing.T) {
	bars := openCloseBars([][2]float64{{100, 101}, {102, 103}})
	signals := []Signal{SignalNone, SignalNone}

	result := simulateSingle(t, bars, signals, 1000)

	if result.Trades == nil {
		t.Fatal("Trades is nil; it must be a non-nil empty slice so it marshals as [] rather than null")
	}

	encoded, err := json.Marshal(result.Trades)
	if err != nil {
		t.Fatalf("json.Marshal(Trades): %v", err)
	}
	if string(encoded) != "[]" {
		t.Errorf("json.Marshal(Trades) = %s, want [] -- a nil slice here would break every consumer expecting an array", encoded)
	}
}

// TestSimulate_EquityMarksToMarketAtEveryBarsClose: the curve must reflect
// the position's value using each bar's own Close, including the bar a fill
// just happened on -- not the fill price itself.
func TestSimulate_EquityMarksToMarketAtEveryBarsClose(t *testing.T) {
	bars := openCloseBars([][2]float64{{100, 101}, {102, 200}})
	signals := []Signal{SignalBuy, SignalNone}

	result := simulateSingle(t, bars, signals, 1000)

	wantQty := 1000.0 / 102
	wantEquityAtFillBar := wantQty * 200 // bar 1's Close, not its Open (102) or bar 0's Close
	if !approxEqual(result.EquityCurve[1], wantEquityAtFillBar) {
		t.Errorf("equity[1] = %v, want %v (marked to bar 1's close)", result.EquityCurve[1], wantEquityAtFillBar)
	}
}
