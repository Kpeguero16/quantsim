package service

import (
	"testing"
)

// portfolioFixture is one symbol's contribution to a multi-symbol test: its
// bars and the signals generated from them, which SimulatePortfolio always
// receives already aligned to a common timeline (alignBars, §2.1).
type portfolioFixture struct {
	symbol  string
	bars    []Bar
	signals []Signal
}

// buildPortfolio turns fixtures into SimulatePortfolio's parallel-slice
// arguments, in the alphabetical order alignBars guarantees. Fixtures are
// written in whatever order reads best in the test; the ordering that matters
// to the engine is imposed here, exactly as it is in production.
func buildPortfolio(t *testing.T, fixtures ...portfolioFixture) ([]string, [][]Bar, [][]Signal) {
	t.Helper()

	sorted := make([]portfolioFixture, len(fixtures))
	copy(sorted, fixtures)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].symbol < sorted[j-1].symbol; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	symbols := make([]string, len(sorted))
	bars := make([][]Bar, len(sorted))
	signals := make([][]Signal, len(sorted))
	for i, f := range sorted {
		if len(f.bars) != len(f.signals) {
			t.Fatalf("%s: %d bars but %d signals -- fixtures must be aligned",
				f.symbol, len(f.bars), len(f.signals))
		}
		if i > 0 && len(f.bars) != len(bars[0]) {
			t.Fatalf("%s: %d bars, but %s has %d -- every aligned series is equal length",
				f.symbol, len(f.bars), symbols[0], len(bars[0]))
		}
		symbols[i], bars[i], signals[i] = f.symbol, f.bars, f.signals
	}
	return symbols, bars, signals
}

// pairsOf turns a price series into openCloseBars pairs with open == close.
// Most portfolio fixtures care about which bar a fill landed on and how the
// cash pool moved, not about separating open from close -- the fill-timing
// rule those distinct values exist to prove is already covered exhaustively by
// simulate_test.go and is unchanged by this step.
func pairsOf(prices []float64) [][2]float64 {
	pairs := make([][2]float64, len(prices))
	for i, p := range prices {
		pairs[i] = [2]float64{p, p}
	}
	return pairs
}

// tradesBySymbol groups a trade log by symbol, preserving order, so a test can
// assert one symbol's fills without indexing into a combined log whose
// interleaving is itself under test elsewhere.
func tradesBySymbol(trades []TradeRecord, symbol string) []TradeRecord {
	out := []TradeRecord{}
	for _, t := range trades {
		if t.Symbol == symbol {
			out = append(out, t)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The N=1 collapse. SPEC.md Step 19 §4 designated the A/B equivalence against
// Simulate the first test written; it was transient by construction (plan D1)
// and was deleted with Simulate itself in T7. What carries that invariant
// permanently is simulate_test.go -- Step 16's expectations, every number
// unchanged, now running through SimulatePortfolio at N=1.
// ---------------------------------------------------------------------------

// TestSimulatePortfolio_SameBarContentionPartialThenZeroFill covers both
// outcomes SPEC.md §2.2 names for a buy that cannot get its full target: a
// partial fill capped by available cash, and -- for a symbol later in the
// alphabet, once the pool is exhausted -- no fill at all, which is a
// legitimate outcome and not an error.
//
// MMM is deliberately holding a position that has quintupled, so the
// equity-based target (2100/3 = 700) exceeds the cash actually on hand (600).
// AAA takes all 600 of it; ZZZ, processed later, finds nothing left.
func TestSimulatePortfolio_SameBarContentionPartialThenZeroFill(t *testing.T) {
	const startingCapital = 900.0 // target starts at 900/3 = 300

	flat := func(prices ...float64) []Bar {
		return openCloseBars(pairsOf(prices))
	}

	// i1: MMM buys 300 at 100 -> 3 shares, cash 600.
	// i2: MMM's price is 500, so equity at open = 600 cash + 1500 = 2100,
	//     target 700. AAA buys min(600, 700) = 600 -> partial. cash 0.
	//     ZZZ buys min(0, 700) = 0 -> no fill at all.
	symbols, bars, signals := buildPortfolio(t,
		portfolioFixture{
			symbol:  "MMM",
			bars:    flat(100, 100, 500),
			signals: []Signal{SignalBuy, SignalNone, SignalNone},
		},
		portfolioFixture{
			symbol:  "AAA",
			bars:    flat(10, 10, 10),
			signals: []Signal{SignalNone, SignalBuy, SignalNone},
		},
		portfolioFixture{
			symbol:  "ZZZ",
			bars:    flat(10, 10, 10),
			signals: []Signal{SignalNone, SignalBuy, SignalNone},
		},
	)

	result := SimulatePortfolio(symbols, bars, signals, startingCapital)

	mmm := tradesBySymbol(result.Trades, "MMM")
	if len(mmm) != 1 || !approxEqual(mmm[0].Quantity*mmm[0].Price, 300) {
		t.Fatalf("MMM trades = %+v, want one buy spending 300 (900/3)", mmm)
	}

	aaa := tradesBySymbol(result.Trades, "AAA")
	if len(aaa) != 1 {
		t.Fatalf("AAA trades = %+v, want exactly one (a partial fill)", aaa)
	}
	if spent := aaa[0].Quantity * aaa[0].Price; !approxEqual(spent, 600) {
		t.Errorf("AAA spent %v, want 600 -- capped by available cash, not by its 700 target", spent)
	}

	if zzz := tradesBySymbol(result.Trades, "ZZZ"); len(zzz) != 0 {
		t.Errorf("ZZZ trades = %+v, want none -- the pool was exhausted before its turn", zzz)
	}
}

// TestSimulatePortfolio_SellFreesCashForASameBarBuy is the test that proves
// this is genuinely one shared pool rather than N sub-budgets, AND that
// SPEC.md §2.2's sells-before-buys ordering is doing real work.
//
// The seller (ZZZ) sorts *after* the buyer (AAA) deliberately. Under a naive
// single alphabetical pass, AAA would be processed first and see only the
// pre-sale cash of 500, taking 500. With sells settled first, the pool holds
// 1500 when AAA's target is computed, and AAA takes 750. Reversing these two
// symbols' names would make this test pass against a broken implementation.
func TestSimulatePortfolio_SellFreesCashForASameBarBuy(t *testing.T) {
	const startingCapital = 1000.0 // target starts at 1000/2 = 500

	// i1: ZZZ buys 500 at 50 -> 10 shares, cash 500.
	// i3: ZZZ sells 10 at 100 -> cash 1500. Equity at open is then 1500 with
	//     nothing held, so AAA's target is 750 and it spends all of it.
	symbols, bars, signals := buildPortfolio(t,
		portfolioFixture{
			symbol:  "ZZZ",
			bars:    openCloseBars(pairsOf([]float64{50, 50, 50, 100})),
			signals: []Signal{SignalBuy, SignalNone, SignalSell, SignalNone},
		},
		portfolioFixture{
			symbol:  "AAA",
			bars:    openCloseBars(pairsOf([]float64{25, 25, 25, 25})),
			signals: []Signal{SignalNone, SignalNone, SignalBuy, SignalNone},
		},
	)

	result := SimulatePortfolio(symbols, bars, signals, startingCapital)

	aaa := tradesBySymbol(result.Trades, "AAA")
	if len(aaa) != 1 {
		t.Fatalf("AAA trades = %+v, want exactly one buy", aaa)
	}
	spent := aaa[0].Quantity * aaa[0].Price
	if approxEqual(spent, 500) {
		t.Fatalf("AAA spent 500 -- that is the pre-sale cash, so buys were processed "+
			"before ZZZ's same-bar sell settled (SPEC.md §2.2 step 1). Trades: %+v", result.Trades)
	}
	if !approxEqual(spent, 750) {
		t.Errorf("AAA spent %v, want 750 (half of the 1500 pool ZZZ's sale left behind)", spent)
	}
}

// TestSimulatePortfolio_TargetTracksEquityNotStartingCapital is the direct
// test of the position-sizing rule this SPEC was revised to adopt (§2.2, §5):
// a symbol's target is a share of *current* equity, so a win funds a larger
// next entry and a loss a smaller one. Under the rejected fixed
// starting_capital/N target, both entries below would be identical.
func TestSimulatePortfolio_TargetTracksEquityNotStartingCapital(t *testing.T) {
	const startingCapital = 1000.0 // target starts at 1000/2 = 500

	entryAfter := func(t *testing.T, exitPrice float64) float64 {
		t.Helper()
		// i1: AAA buys 500 at 100 -> 5 shares, cash 500.
		// i3: AAA sells 5 at exitPrice.
		// i5: AAA buys again -- the size under test.
		symbols, bars, signals := buildPortfolio(t,
			portfolioFixture{
				symbol:  "AAA",
				bars:    openCloseBars(pairsOf([]float64{100, 100, 100, exitPrice, exitPrice, exitPrice})),
				signals: []Signal{SignalBuy, SignalNone, SignalSell, SignalNone, SignalBuy, SignalNone},
			},
			portfolioFixture{
				symbol:  "ZZZ",
				bars:    openCloseBars(pairsOf([]float64{10, 10, 10, 10, 10, 10})),
				signals: []Signal{SignalNone, SignalNone, SignalNone, SignalNone, SignalNone, SignalNone},
			},
		)
		result := SimulatePortfolio(symbols, bars, signals, startingCapital)
		trades := tradesBySymbol(result.Trades, "AAA")
		if len(trades) != 3 {
			t.Fatalf("AAA trades = %+v, want 3 (buy, sell, buy)", trades)
		}
		return trades[2].Quantity * trades[2].Price
	}

	afterWin := entryAfter(t, 200) // 5 shares sold at 200 -> cash 1500, target 750
	afterLoss := entryAfter(t, 50) // 5 shares sold at 50 -> cash 750, target 375
	const firstEntry = 500.0

	if !approxEqual(afterWin, 750) {
		t.Errorf("entry after a winning trade spent %v, want 750 -- the target must be read off "+
			"current equity (1500/2), not starting capital", afterWin)
	}
	if !approxEqual(afterLoss, 375) {
		t.Errorf("entry after a losing trade spent %v, want 375 (750/2)", afterLoss)
	}
	if afterWin <= firstEntry || afterLoss >= firstEntry {
		t.Errorf("targets did not move with equity: first %v, after a win %v, after a loss %v",
			firstEntry, afterWin, afterLoss)
	}
}

// TestSimulatePortfolio_EquityAggregatesDivergentPaths: the curve is
// cash plus every held position marked to that bar's close (§2.2 step 4),
// which has to hold while symbols move in opposite directions.
func TestSimulatePortfolio_EquityAggregatesDivergentPaths(t *testing.T) {
	const startingCapital = 1000.0 // 500 each

	// Both buy at i1's open of 100 -> 5 shares each. From i2 AAA rises and
	// ZZZ falls by the same amount, so the portfolio's equity stays flat
	// even though neither symbol's does.
	symbols, bars, signals := buildPortfolio(t,
		portfolioFixture{
			symbol:  "AAA",
			bars:    openCloseBars([][2]float64{{100, 100}, {100, 100}, {100, 120}, {120, 140}}),
			signals: []Signal{SignalBuy, SignalNone, SignalNone, SignalNone},
		},
		portfolioFixture{
			symbol:  "ZZZ",
			bars:    openCloseBars([][2]float64{{100, 100}, {100, 100}, {100, 80}, {80, 60}}),
			signals: []Signal{SignalBuy, SignalNone, SignalNone, SignalNone},
		},
	)

	result := SimulatePortfolio(symbols, bars, signals, startingCapital)

	if len(result.EquityCurve) != 4 {
		t.Fatalf("equity curve length = %d, want 4 (one per aligned bar)", len(result.EquityCurve))
	}
	// i1 onward: 5 shares of each, no cash left. The gains and losses cancel.
	for i := 1; i < 4; i++ {
		if !approxEqual(result.EquityCurve[i], 1000) {
			t.Errorf("equity[%d] = %v, want 1000 -- AAA's gain and ZZZ's loss are equal and opposite",
				i, result.EquityCurve[i])
		}
	}
	if !approxEqual(result.FinalEquity, 1000) {
		t.Errorf("final equity = %v, want 1000", result.FinalEquity)
	}
}

// TestSimulatePortfolio_NoTradesStillReturnsAnEmptyNonNilLog is the Step 17
// bug applied preemptively: BacktestDetail.Trades is a JSON array field, and a
// nil slice marshals as `null`, not `[]`. len() cannot tell the two apart, so
// this asserts nil-ness directly.
func TestSimulatePortfolio_NoTradesStillReturnsAnEmptyNonNilLog(t *testing.T) {
	symbols, bars, signals := buildPortfolio(t,
		portfolioFixture{
			symbol:  "AAA",
			bars:    openCloseBars(pairsOf([]float64{100, 100})),
			signals: []Signal{SignalNone, SignalNone},
		},
		portfolioFixture{
			symbol:  "ZZZ",
			bars:    openCloseBars(pairsOf([]float64{100, 100})),
			signals: []Signal{SignalNone, SignalNone},
		},
	)

	result := SimulatePortfolio(symbols, bars, signals, 1000)

	if result.Trades == nil {
		t.Error("Trades is nil, want an empty non-nil slice (a nil slice marshals as null, not [])")
	}
	if len(result.Trades) != 0 {
		t.Errorf("Trades = %+v, want none", result.Trades)
	}
	if !approxEqual(result.FinalEquity, 1000) {
		t.Errorf("final equity = %v, want the starting capital untouched", result.FinalEquity)
	}
}

// TestSimulatePortfolio_EmptyTimelineIsNotAnError: alignBars returns an empty
// intersection rather than an error (§2.1), and RunBacktest is what maps that
// to ErrDateRangeUnavailable. Reaching this engine with one should not panic.
func TestSimulatePortfolio_EmptyTimelineIsNotAnError(t *testing.T) {
	result := SimulatePortfolio([]string{"AAA", "ZZZ"}, [][]Bar{{}, {}}, [][]Signal{{}, {}}, 1000)

	if len(result.EquityCurve) != 0 {
		t.Errorf("equity curve = %v, want empty", result.EquityCurve)
	}
	if result.Trades == nil {
		t.Error("Trades is nil, want an empty non-nil slice")
	}
	if !approxEqual(result.FinalEquity, 1000) {
		t.Errorf("final equity = %v, want the starting capital", result.FinalEquity)
	}
}
