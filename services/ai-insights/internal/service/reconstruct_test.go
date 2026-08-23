package service_test

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// Every expected figure in this file is hand-computed from the fixture, never
// captured from a run of the code under test. bars() closes are 100+day, so
// AAPL closes 101 on day 1, 102 on day 2, and so on.
const eps = 1e-9

func trade(symbol string, side service.Side, qty, price float64, d int) service.Trade {
	return service.Trade{
		ID: symbol + string(rune('0'+d)), Symbol: symbol, Side: side,
		Quantity: qty, Price: price, ExecutedAt: day(d),
	}
}

func buy(symbol string, qty, price float64, d int) service.Trade {
	return trade(symbol, service.SideBuy, qty, price, d)
}

func sell(symbol string, qty, price float64, d int) service.Trade {
	return trade(symbol, service.SideSell, qty, price, d)
}

func reconstruct(t *testing.T, trades []service.Trade, barsBySymbol map[string][]service.Bar) service.Reconstruction {
	t.Helper()
	got, err := service.Reconstruct(trades, barsBySymbol, service.Calendar(barsBySymbol))
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	return got
}

func assertFloats(t *testing.T, what string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d values (%v), want %d (%v)", what, len(got), got, len(want), want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > eps {
			t.Errorf("%s[%d]: got %v, want %v", what, i, got[i], want[i])
		}
	}
}

// Cash returns to the starting balance plus exactly the realized gain: buy 10
// at 100 costs 1000, sell 10 at 110 returns 1100, so 100000 -> 100100.
func TestReconstruct_RoundTripReturnsCashToStartingBalancePlusRealized(t *testing.T) {
	got := reconstruct(t,
		[]service.Trade{buy("AAPL", 10, 100, 1), sell("AAPL", 10, 110, 3)},
		map[string][]service.Bar{"AAPL": bars(1, 2, 3), "SPY": bars(1, 2, 3)})

	assertDates(t, got.Dates, 1, 2, 3)
	// day 1: 99000 cash + 10 shares at close 101 = 100010
	// day 2: 99000 cash + 10 shares at close 102 = 100020
	// day 3: sold, 100100 cash, nothing held
	assertFloats(t, "equity", got.Equity, []float64{100010, 100020, 100100})
	assertFloats(t, "cash", got.Cash, []float64{99000, 99000, 100100})

	if len(got.Holdings) != 0 {
		t.Errorf("holdings = %v, want empty -- the position was fully sold", got.Holdings)
	}
}

// The case a "current positions" implementation silently gets wrong: a symbol
// bought and fully sold before the window ends still has to contribute to
// equity on the days it was actually held.
func TestReconstruct_ASymbolSoldMidWindowStillContributesWhileHeld(t *testing.T) {
	got := reconstruct(t,
		[]service.Trade{buy("AAPL", 10, 100, 2), sell("AAPL", 10, 120, 3)},
		map[string][]service.Bar{"AAPL": bars(1, 2, 3, 4), "SPY": bars(1, 2, 3, 4)})

	// Window opens at the first trade, so day 1 is absent.
	assertDates(t, got.Dates, 2, 3, 4)
	// day 2: 99000 cash + 10 at close 102 = 100020 -- strictly above cash,
	// which is the whole point: the holding is valued, not ignored.
	assertFloats(t, "equity", got.Equity, []float64{100020, 100200, 100200})
	if got.Equity[0] <= got.Cash[0] {
		t.Errorf("day 2 equity %v does not exceed cash %v -- the holding was not valued",
			got.Equity[0], got.Cash[0])
	}
}

// SPEC §2.2's insensitivity claim, tested rather than assumed:
// GET /trading/trades breaks same-timestamp ties on a random UUID, so the
// order two same-instant trades arrive in is arbitrary and must not matter.
func TestReconstruct_SameTimestampTradesAreOrderInsensitive(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": bars(1, 2), "MSFT": bars(1, 2), "SPY": bars(1, 2),
	}
	a := buy("AAPL", 5, 100, 1)
	b := buy("MSFT", 3, 50, 1)
	// Identical instants, so nothing in the data distinguishes them.
	a.ExecutedAt = day(1)
	b.ExecutedAt = day(1)

	forward := reconstruct(t, []service.Trade{a, b}, barsBySymbol)
	reversed := reconstruct(t, []service.Trade{b, a}, barsBySymbol)

	assertFloats(t, "equity", reversed.Equity, forward.Equity)
	assertFloats(t, "cash", reversed.Cash, forward.Cash)
	if len(forward.Holdings) != len(reversed.Holdings) {
		t.Fatalf("holdings differ: %v vs %v", forward.Holdings, reversed.Holdings)
	}
	for s, q := range forward.Holdings {
		if math.Abs(reversed.Holdings[s]-q) > eps {
			t.Errorf("holding %s: %v vs %v", s, q, reversed.Holdings[s])
		}
	}
}

// A trade can land on a date the calendar does not contain, because some other
// symbol had no bar that day. It still happened and still moved cash. Matching
// trades to dates on equality would drop it silently.
func TestReconstruct_ATradeOffTheCalendarStillMovesCash(t *testing.T) {
	// AAPL has no bar on day 2, so the calendar is [1, 3] -- but the trade is
	// on day 2.
	got := reconstruct(t,
		[]service.Trade{buy("AAPL", 10, 100, 2)},
		map[string][]service.Bar{"AAPL": bars(1, 3), "SPY": bars(1, 2, 3)})

	assertDates(t, got.Dates, 3)
	// 99000 cash + 10 at day 3's close of 103 = 100030. Cash below the
	// starting balance is the proof the buy was applied at all.
	assertFloats(t, "cash", got.Cash, []float64{99000})
	assertFloats(t, "equity", got.Equity, []float64{100030})
}

func TestReconstruct_WindowOpensAtTheFirstTrade(t *testing.T) {
	got := reconstruct(t,
		[]service.Trade{buy("AAPL", 1, 100, 3)},
		map[string][]service.Bar{"AAPL": bars(1, 2, 3, 4, 5), "SPY": bars(1, 2, 3, 4, 5)})

	assertDates(t, got.Dates, 3, 4, 5)
}

// D5's property test. naiveFinalState is deliberately the dumbest possible
// implementation -- no sorting, no calendar, no dust handling -- so agreeing
// with it is agreement between two independent implementations rather than a
// function checked against itself.
func naiveFinalState(trades []service.Trade) (float64, map[string]float64) {
	cash := service.StartingBalance
	holdings := map[string]float64{}
	for _, t := range trades {
		if t.Side == service.SideSell {
			cash += t.Quantity * t.Price
			holdings[t.Symbol] -= t.Quantity
		} else {
			cash -= t.Quantity * t.Price
			holdings[t.Symbol] += t.Quantity
		}
	}
	return cash, holdings
}

func TestReconstruct_FinalStateAgreesWithAnIndependentFold(t *testing.T) {
	trades := []service.Trade{
		buy("AAPL", 10, 100, 1),
		buy("MSFT", 4, 250, 1),
		sell("AAPL", 4, 105, 2),
		buy("TSLA", 2, 300, 2),
		sell("MSFT", 4, 240, 3),
		buy("AAPL", 6, 98, 4),
		sell("TSLA", 2, 310, 5),
	}
	barsBySymbol := map[string][]service.Bar{
		"AAPL": bars(1, 2, 3, 4, 5), "MSFT": bars(1, 2, 3, 4, 5),
		"TSLA": bars(1, 2, 3, 4, 5), "SPY": bars(1, 2, 3, 4, 5),
	}

	got := reconstruct(t, trades, barsBySymbol)
	wantCash, wantHoldings := naiveFinalState(trades)

	if math.Abs(got.Cash[len(got.Cash)-1]-wantCash) > eps {
		t.Errorf("final cash: got %v, want %v", got.Cash[len(got.Cash)-1], wantCash)
	}
	for symbol, qty := range wantHoldings {
		if math.Abs(qty) < 1e-9 {
			continue // fully closed; Reconstruct drops it, the naive fold keeps a zero
		}
		if math.Abs(got.Holdings[symbol]-qty) > eps {
			t.Errorf("holding %s: got %v, want %v", symbol, got.Holdings[symbol], qty)
		}
	}
	// MSFT and TSLA were both fully sold, so they must be absent rather than
	// present at zero.
	for _, symbol := range []string{"MSFT", "TSLA"} {
		if _, ok := got.Holdings[symbol]; ok {
			t.Errorf("%s is still in holdings at %v, want absent", symbol, got.Holdings[symbol])
		}
	}
}

func TestReconstruct_NoTradesIsAnEmptyCurveAndNoError(t *testing.T) {
	got := reconstruct(t, nil, map[string][]service.Bar{"AAPL": bars(1, 2), "SPY": bars(1, 2)})

	if len(got.Dates) != 0 || len(got.Equity) != 0 || len(got.Cash) != 0 {
		t.Errorf("got a non-empty curve for an account that never traded: %+v", got)
	}
	if got.Holdings == nil {
		t.Error("Holdings is nil, want an empty non-nil map")
	}
}

// A held symbol with no bars is an internal inconsistency -- the caller built
// the calendar from a different symbol set than it reconstructed with. Valuing
// the position at zero would understate equity by exactly its value and look
// like a bad day rather than a bug.
func TestReconstruct_AHeldSymbolWithNoClosesIsAnError(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{"AAPL": bars(1, 2), "SPY": bars(1, 2)}

	_, err := service.Reconstruct(
		[]service.Trade{buy("TSLA", 5, 300, 1)},
		barsBySymbol,
		service.Calendar(barsBySymbol),
	)
	if !errors.Is(err, service.ErrMissingClose) {
		t.Fatalf("got %v, want ErrMissingClose", err)
	}
	// The message names the symbol and the date, so the inconsistency is
	// diagnosable from a log line alone.
	if msg := err.Error(); !strings.Contains(msg, "TSLA") || !strings.Contains(msg, "2026-03-01") {
		t.Errorf("error %q names neither the symbol nor the date", msg)
	}
}

// Guards the parallel-slice invariant the whole type depends on.
func TestReconstruct_DatesCashAndEquityStayParallel(t *testing.T) {
	got := reconstruct(t,
		[]service.Trade{buy("AAPL", 1, 100, 1), sell("AAPL", 1, 105, 3)},
		map[string][]service.Bar{"AAPL": bars(1, 2, 3, 4), "SPY": bars(1, 2, 3, 4)})

	if len(got.Dates) != len(got.Cash) || len(got.Dates) != len(got.Equity) {
		t.Fatalf("lengths diverge: dates=%d cash=%d equity=%d",
			len(got.Dates), len(got.Cash), len(got.Equity))
	}
	var last time.Time
	for i, d := range got.Dates {
		if i > 0 && !d.After(last) {
			t.Errorf("date %d (%v) does not follow %v", i, d, last)
		}
		last = d
	}
}

// A KNOWN LIMITATION, pinned deliberately rather than left to be rediscovered.
//
// The calendar is a flat intersection over every symbol the account has ever
// held. A gap in the MIDDLE of one symbol's bars drops one date, which is what
// we want. A gap at the END truncates the whole curve there -- and Holdings,
// being the map as of the last calendar date, then describes a portfolio the
// account does not have. Silently, with no error.
//
// Nothing in this package can fix that: Reconstruct honestly reports what the
// calendar it was handed supports. It is caught downstream by the
// reconciliation guard against the live account (SPEC.md §2.12), and the
// principled fix -- qualifying each date by the symbols held ON that date --
// is a change to SPEC §2.1's intersection rule and belongs in its own step.
//
// This test asserts the truncation HAPPENS. If a future change to Calendar
// makes it stop happening, this fails, and whoever made that change is
// required to revisit §2.12 rather than silently obsoleting it.
func TestReconstruct_ATailGapTruncatesTheCurveAndHoldings_KnownLimitation(t *testing.T) {
	// TSLA was bought, sold, and its bars stop at day 2 -- delisted, renamed,
	// or missing from an ingest. AAPL and SPY have bars through day 4.
	barsBySymbol := map[string][]service.Bar{
		"AAPL": bars(1, 2, 3, 4),
		"SPY":  bars(1, 2, 3, 4),
		"TSLA": bars(1, 2),
	}
	trades := []service.Trade{
		buy("TSLA", 1, 300, 1),
		sell("TSLA", 1, 310, 2),
		buy("AAPL", 10, 100, 1),
		buy("AAPL", 90, 100, 4), // after the calendar ends
	}

	got := reconstruct(t, trades, barsBySymbol)

	// The curve stops at day 2, though AAPL and SPY are priced through day 4.
	assertDates(t, got.Dates, 1, 2)

	// Holdings is as of day 2, so the day-4 buy is absent: 10 shares reported
	// where the account really holds 100.
	if q := got.Holdings["AAPL"]; math.Abs(q-10) > eps {
		t.Fatalf("AAPL holding = %v, want 10 -- this test pins the truncation, "+
			"so if it now reports 100 the limitation is fixed and SPEC §2.12 needs revisiting", q)
	}

	// And the naive fold over the same trades disagrees, which is exactly the
	// signal §2.12's runtime guard fires on.
	_, wantHoldings := naiveFinalState(trades)
	if math.Abs(wantHoldings["AAPL"]-100) > eps {
		t.Fatalf("fixture is wrong: the account should really hold 100 AAPL, got %v", wantHoldings["AAPL"])
	}
	if math.Abs(got.Holdings["AAPL"]-wantHoldings["AAPL"]) < eps {
		t.Error("reconstruction agrees with the account, so this fixture no longer " +
			"demonstrates the truncation it exists to pin")
	}
}

// driftBars prices a symbol in cents, the way market-data stores it.
//
// The cents matter, and not as realism. bars() closes at whole dollars, and
// whole dollars are exact in binary, so a portfolio priced from bars() sums to
// the same float64 whatever order the holdings are visited in. A bit-stability
// test built on bars() therefore passes against code with no bit stability at
// all -- measured, on the first fixture tried for the test below: 1 distinct
// equity curve over 200 runs BEFORE the fix, and 199 after the closes were
// rounded to cents instead.
func driftBars(base float64, days int) []service.Bar {
	out := make([]service.Bar, 0, days)
	for d := 1; d <= days; d++ {
		out = append(out, service.Bar{
			Timestamp: day(d),
			Close:     math.Round((base+float64(d)*0.37)*100) / 100,
		})
	}
	return out
}

// driftFixture is a portfolio whose per-date equity sum is order-sensitive:
// cent closes, NUMERIC(20,4) quantities, five holdings over twenty-five days.
func driftFixture() ([]service.Trade, map[string][]service.Bar) {
	const days = 25
	barsBySymbol := map[string][]service.Bar{
		"AAPL":  driftBars(187.43, days),
		"MSFT":  driftBars(411.19, days),
		"GOOGL": driftBars(168.77, days),
		"AMZN":  driftBars(203.31, days),
		"TSLA":  driftBars(246.53, days),
		"SPY":   driftBars(585.07, days),
		"QQQ":   driftBars(498.61, days),
	}
	trades := []service.Trade{
		buy("AAPL", 137.4321, 187.80, 1),
		buy("MSFT", 73.1177, 411.56, 1),
		buy("GOOGL", 219.4413, 169.14, 2),
		buy("AMZN", 111.9931, 203.68, 2),
		buy("TSLA", 59.7717, 246.90, 3),
	}
	return trades, barsBySymbol
}

// equityBits renders a curve as its exact bit patterns, so two curves compare
// as identical only if every value is identical to the last bit.
func equityBits(r service.Reconstruction) string {
	var b strings.Builder
	for _, e := range r.Equity {
		fmt.Fprintf(&b, "%016x|", math.Float64bits(e))
	}
	return b.String()
}

// Identical input must reconstruct to an identical curve, bit for bit.
//
// This is the precondition ReportHash depends on and did not have. The equity
// sum ran in map iteration order, which Go randomizes per pass, and float64
// addition is not associative, so the same holdings at the same closes summed
// to results differing in their last bits. Every displayed figure rounds that
// away; the hash is defined on the exact bytes and sees all of it, and the
// narrative cache is keyed on the hash.
//
// Note what this does NOT assert: that the curve equals any particular value.
// The requirement is that two runs agree with each other, not that either
// agrees with the exact decimal sum (SPEC.md Step 23 §4.2).
//
// Verified to fail against the unfixed code: 199 distinct curves over 200 runs.
func TestReconstruct_IsBitStableAcrossRuns(t *testing.T) {
	trades, barsBySymbol := driftFixture()
	calendar := service.Calendar(barsBySymbol)

	first, err := service.Reconstruct(trades, barsBySymbol, calendar)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if len(first.Equity) < 20 {
		t.Fatalf("fixture reconstructed only %d dates; it cannot exercise "+
			"enough map iterations to be a stability test", len(first.Equity))
	}
	want := equityBits(first)

	const runs = 200
	for i := 1; i < runs; i++ {
		got, err := service.Reconstruct(trades, barsBySymbol, calendar)
		if err != nil {
			t.Fatalf("run %d: Reconstruct: %v", i, err)
		}
		if bits := equityBits(got); bits != want {
			t.Fatalf("run %d produced a different equity curve from run 0:\n got %s\nwant %s",
				i, bits, want)
		}
	}
}
