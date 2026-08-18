package service

import (
	"math"
	"testing"
	"time"
)

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
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

	result := Simulate(bars, signals, startingCapital)

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

	result := Simulate(bars, signals, 1000)

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

	result := Simulate(bars, signals, 1000)

	if len(result.Trades) != 1 {
		t.Fatalf("got %d trades, want 1 -- the second buy signal fires while already holding and must be a no-op", len(result.Trades))
	}
}

// TestSimulate_SellSignalWhileFlatIsANoOp is the mirror of the above: a sell
// signal with nothing held must not fabricate a trade or move cash.
func TestSimulate_SellSignalWhileFlatIsANoOp(t *testing.T) {
	bars := openCloseBars([][2]float64{{100, 101}, {102, 103}})
	signals := []Signal{SignalSell, SignalNone}

	result := Simulate(bars, signals, 1000)

	if len(result.Trades) != 0 {
		t.Fatalf("got %d trades, want 0 -- nothing was held to sell", len(result.Trades))
	}
	if !approxEqual(result.FinalEquity, 1000) {
		t.Errorf("final equity = %v, want 1000 (cash untouched)", result.FinalEquity)
	}
}

// TestSimulate_EquityMarksToMarketAtEveryBarsClose: the curve must reflect
// the position's value using each bar's own Close, including the bar a fill
// just happened on -- not the fill price itself.
func TestSimulate_EquityMarksToMarketAtEveryBarsClose(t *testing.T) {
	bars := openCloseBars([][2]float64{{100, 101}, {102, 200}})
	signals := []Signal{SignalBuy, SignalNone}

	result := Simulate(bars, signals, 1000)

	wantQty := 1000.0 / 102
	wantEquityAtFillBar := wantQty * 200 // bar 1's Close, not its Open (102) or bar 0's Close
	if !approxEqual(result.EquityCurve[1], wantEquityAtFillBar) {
		t.Errorf("equity[1] = %v, want %v (marked to bar 1's close)", result.EquityCurve[1], wantEquityAtFillBar)
	}
}
