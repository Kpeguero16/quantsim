package service

import (
	"reflect"
	"testing"
	"time"
)

// day is the epoch every alignment fixture counts from -- the same
// 2025-01-01 UTC base openCloseBars uses, so alignment and simulation
// fixtures line up when a test needs both.
func day(offset int) time.Time {
	return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, offset)
}

// barsOn builds one symbol's series from day offsets, with a distinct price
// per day so a test can prove which bar survived alignment, not just how many.
func barsOn(offsets ...int) []Bar {
	bars := make([]Bar, len(offsets))
	for i, o := range offsets {
		price := float64(100 + o)
		bars[i] = Bar{Timestamp: day(o), Open: price, Close: price + 0.5}
	}
	return bars
}

// dayOffsets reads a series back as the day offsets it covers, which is what
// every assertion below is actually about.
func dayOffsets(bars []Bar) []int {
	out := []int{}
	for _, b := range bars {
		out = append(out, int(b.Timestamp.Sub(day(0)).Hours()/24))
	}
	return out
}

// wideRange is deliberately larger than any fixture, so a test that means to
// exercise intersection is never quietly also exercising range slicing.
var rangeStart, rangeEnd = day(0), day(365)

// TestAlignBars_SingleSymbolIsExactlySliceRange is the N=1 collapse at the
// alignment layer: with one symbol there is nothing to intersect against, so
// the output must be sliceRange's own, unchanged. SPEC.md Step 19 §1's framing
// -- a single-symbol run is len(symbols)==1 of the general path, not a
// special case -- has to hold here before it can hold in SimulatePortfolio.
//
// One deliberate difference, in the empty case only: sliceRange returns a nil
// slice when nothing matches, alignBars an empty non-nil one (see
// TestAlignBars_EmptyAlignmentIsNonNil for why). This test therefore uses a
// range that matches bars, which is the case the equality claim is about.
func TestAlignBars_SingleSymbolIsExactlySliceRange(t *testing.T) {
	bars := barsOn(0, 1, 2, 5, 9)
	start, end := day(1), day(5)

	symbols, aligned := alignBars(map[string][]Bar{"AAPL": bars}, start, end)

	if !reflect.DeepEqual(symbols, []string{"AAPL"}) {
		t.Fatalf("symbols = %v, want [AAPL]", symbols)
	}
	want := sliceRange(bars, start, end)
	if !reflect.DeepEqual(aligned[0], want) {
		t.Errorf("aligned[0] = %v, want sliceRange's own output %v", aligned[0], want)
	}
}

// TestAlignBars_SortsSymbolsRegardlessOfInsertionOrder: the returned order is
// what breaks same-bar cash contention in SimulatePortfolio (§2.2) and what
// the run persists (plan D4), so it must come from the symbols themselves and
// never from how the caller happened to assemble them.
//
// Six symbols and repeated calls, both deliberately. The input is a map, so a
// test cannot force an iteration order -- it can only make "accidentally
// already sorted" improbable enough to count as caught. Go randomizes map
// iteration per range statement, so an unsorted implementation has a 1/720
// chance of passing any one call; across the repeats below it has none worth
// worrying about. An earlier three-symbol single-call version of this test let
// a deleted sort.Strings through roughly one run in six, which is not a guard.
func TestAlignBars_SortsSymbolsRegardlessOfInsertionOrder(t *testing.T) {
	in := map[string][]Bar{
		"MSFT":  barsOn(0, 1),
		"AAPL":  barsOn(0, 1),
		"GOOGL": barsOn(0, 1),
		"TSLA":  barsOn(0, 1),
		"NVDA":  barsOn(0, 1),
		"AMZN":  barsOn(0, 1),
	}
	want := []string{"AAPL", "AMZN", "GOOGL", "MSFT", "NVDA", "TSLA"}

	for i := 0; i < 20; i++ {
		symbols, aligned := alignBars(in, rangeStart, rangeEnd)

		if !reflect.DeepEqual(symbols, want) {
			t.Fatalf("call %d: symbols = %v, want %v", i, symbols, want)
		}
		if len(aligned) != len(want) {
			t.Fatalf("call %d: got %d aligned series, want %d -- one per symbol",
				i, len(aligned), len(want))
		}
	}
}

// TestAlignBars_DropsDaysMissingFromAnySymbol is the intersection rule itself
// (§2.1). A gap in one symbol's calendar removes that day from *every*
// symbol's series, not just its own -- otherwise bar index i would stop
// meaning the same trading day across symbols.
func TestAlignBars_DropsDaysMissingFromAnySymbol(t *testing.T) {
	in := map[string][]Bar{
		"AAPL": barsOn(0, 1, 2, 3),
		"MSFT": barsOn(0, 2, 3), // no bar on day 1
	}

	symbols, aligned := alignBars(in, rangeStart, rangeEnd)

	want := []int{0, 2, 3}
	for i, symbol := range symbols {
		if got := dayOffsets(aligned[i]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s aligned to days %v, want %v", symbol, got, want)
		}
	}
}

// TestAlignBars_GapInTheDrivingSymbolIsAlsoDropped guards the asymmetry the
// implementation could plausibly have: the first symbol drives the output
// order, so a day *it* is missing must be dropped just as readily as one the
// looked-up symbols are missing. Reversing the roles of the fixture above is
// what proves the rule is genuinely an intersection and not "day 0's symbol
// wins."
func TestAlignBars_GapInTheDrivingSymbolIsAlsoDropped(t *testing.T) {
	in := map[string][]Bar{
		"AAPL": barsOn(0, 2, 3), // alphabetically first, and missing day 1
		"MSFT": barsOn(0, 1, 2, 3),
	}

	symbols, aligned := alignBars(in, rangeStart, rangeEnd)

	want := []int{0, 2, 3}
	for i, symbol := range symbols {
		if got := dayOffsets(aligned[i]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s aligned to days %v, want %v", symbol, got, want)
		}
	}
}

// TestAlignBars_EveryAlignedSeriesIsTheSameLengthAndSameDay is the invariant
// SimulatePortfolio depends on and that no individual case above states
// directly: at every index i, every symbol's bar is the same calendar day.
func TestAlignBars_EveryAlignedSeriesIsTheSameLengthAndSameDay(t *testing.T) {
	in := map[string][]Bar{
		"AAPL":  barsOn(0, 1, 2, 3, 4, 7),
		"GOOGL": barsOn(1, 2, 3, 4, 7, 9),
		"MSFT":  barsOn(0, 2, 3, 4, 5, 7),
	}

	symbols, aligned := alignBars(in, rangeStart, rangeEnd)

	length := len(aligned[0])
	if length != 4 { // days 2, 3, 4, 7 -- the only ones all three share
		t.Fatalf("aligned length = %d, want 4 (days 2,3,4,7)", length)
	}
	for i, symbol := range symbols {
		if len(aligned[i]) != length {
			t.Fatalf("%s has %d bars, want %d -- every series must be equal length",
				symbol, len(aligned[i]), length)
		}
	}
	for i := 0; i < length; i++ {
		want := dayKey(aligned[0][i].Timestamp)
		for j, symbol := range symbols {
			if got := dayKey(aligned[j][i].Timestamp); got != want {
				t.Errorf("at index %d, %s is on %s but %s is on %s -- index must mean one trading day",
					i, symbol, got, symbols[0], want)
			}
		}
	}
}

// TestAlignBars_DisjointCalendarsAlignToNothing: the empty intersection is a
// legitimate, non-error outcome here. RunBacktest maps it to
// ErrDateRangeUnavailable; alignBars just reports emptiness (§2.1).
func TestAlignBars_DisjointCalendarsAlignToNothing(t *testing.T) {
	in := map[string][]Bar{
		"AAPL": barsOn(0, 1, 2),
		"MSFT": barsOn(3, 4, 5),
	}

	symbols, aligned := alignBars(in, rangeStart, rangeEnd)

	for i, symbol := range symbols {
		if len(aligned[i]) != 0 {
			t.Errorf("%s aligned to %d bars, want 0 (no shared day)", symbol, len(aligned[i]))
		}
	}
}

// TestAlignBars_RangeIsAppliedBeforeIntersection: a day both symbols have but
// the request excluded must not survive, and a day inside the range that only
// one symbol has must not resurrect it.
func TestAlignBars_RangeIsAppliedBeforeIntersection(t *testing.T) {
	in := map[string][]Bar{
		"AAPL": barsOn(0, 1, 2, 3, 4),
		"MSFT": barsOn(0, 1, 2, 3, 4),
	}

	symbols, aligned := alignBars(in, day(1), day(3))

	want := []int{1, 2, 3}
	for i, symbol := range symbols {
		if got := dayOffsets(aligned[i]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s aligned to days %v, want %v (end date inclusive)", symbol, got, want)
		}
	}
}

// TestAlignBars_EmptyAlignmentIsNonNil: SimulationResult.EquityCurve and the
// trade log both have to marshal as [] rather than null (the Step 17 bug), and
// the cheapest place to guarantee an empty run stays non-nil is here, at the
// source of the series everything downstream is sized from.
func TestAlignBars_EmptyAlignmentIsNonNil(t *testing.T) {
	in := map[string][]Bar{
		"AAPL": barsOn(0),
		"MSFT": barsOn(5),
	}

	_, aligned := alignBars(in, rangeStart, rangeEnd)

	for i, series := range aligned {
		if series == nil {
			t.Errorf("aligned[%d] is nil, want an empty non-nil slice", i)
		}
	}
}
