package service_test

import (
	"errors"
	"math"
	"testing"

	"github.com/kpeguero/quantsim/pkg/portfoliomath"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// priced builds a bar series with explicit closes, one per day number, so a
// test can state the exact return it expects. The shared `bars` helper derives
// close from the day number, which is fine for calendar tests and useless for
// arithmetic ones.
func priced(days []int, closes []float64) []service.Bar {
	if len(days) != len(closes) {
		panic("priced: days and closes must be the same length")
	}
	out := make([]service.Bar, len(days))
	for i, d := range days {
		out[i] = service.Bar{Timestamp: day(d), Close: closes[i]}
	}
	return out
}

func benchmarking(t *testing.T, trades []service.Trade, barsBySymbol map[string][]service.Bar) service.BenchmarkingSection {
	t.Helper()
	got, err := service.ComputeBenchmarking(reconstruct(t, trades, barsBySymbol), barsBySymbol)
	if err != nil {
		t.Fatalf("ComputeBenchmarking: %v", err)
	}
	return got
}

func benchmarkFor(t *testing.T, section service.BenchmarkingSection, symbol string) service.Benchmark {
	t.Helper()
	for _, b := range section.Benchmarks {
		if b.Symbol == symbol {
			return b
		}
	}
	t.Fatalf("no %s in %+v", symbol, section.Benchmarks)
	return service.Benchmark{}
}

func assertClose(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

// Buy-and-hold return is the first close to the last, and the share count
// cancels out of it -- so 100 -> 110 is 10% whatever the starting balance is.
func TestComputeBenchmarking_ReturnIsFirstCloseToLast(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2, 3}, []float64{100, 100, 100}),
		"SPY":  priced([]int{1, 2, 3}, []float64{100, 105, 110}),
		"QQQ":  priced([]int{1, 2, 3}, []float64{200, 190, 180}),
	}
	got := benchmarking(t, []service.Trade{buy("AAPL", 10, 100, 1)}, barsBySymbol)

	if got.State != service.StateOK {
		t.Fatalf("state: got %q", got.State)
	}
	assertClose(t, "SPY return", benchmarkFor(t, got, "SPY").ReturnPct, 10)
	assertClose(t, "QQQ return", benchmarkFor(t, got, "QQQ").ReturnPct, -10)
}

// THE ACCEPTANCE CRITERION THAT MATTERS: the benchmark is measured over the
// PORTFOLIO's calendar, not over its own bar range.
//
// SPY is priced through day 6; AAPL stops at day 3, so the calendar does too.
// Reading SPY's own first and last bar would report its day 1->6 return of
// 50%, which is a wrong number about a window the portfolio was never measured
// over -- and it would look perfectly reasonable in the response.
func TestComputeBenchmarking_UsesThePortfolioCalendarNotTheBenchmarksOwnRange(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2, 3}, []float64{100, 100, 100}),
		"SPY":  priced([]int{1, 2, 3, 4, 5, 6}, []float64{100, 105, 110, 120, 130, 150}),
		"QQQ":  priced([]int{1, 2, 3, 4, 5, 6}, []float64{100, 100, 100, 100, 100, 100}),
	}
	r := reconstruct(t, []service.Trade{buy("AAPL", 10, 100, 1)}, barsBySymbol)

	// Precondition: the calendar really is the short one.
	if len(r.Dates) != 3 {
		t.Fatalf("fixture: calendar has %d dates, want 3", len(r.Dates))
	}

	got, err := service.ComputeBenchmarking(r, barsBySymbol)
	if err != nil {
		t.Fatalf("ComputeBenchmarking: %v", err)
	}

	spy := benchmarkFor(t, got, "SPY")
	assertClose(t, "SPY return over the portfolio's 3 days", spy.ReturnPct, 10)
	if math.Abs(spy.ReturnPct-50) < 1e-9 {
		t.Error("SPY was measured over its own bar range, not the portfolio's calendar")
	}
}

// A portfolio that IS the benchmark has no excess return. Fully invested in
// SPY at the first close, so the reconstructed equity curve and the
// buy-and-hold curve are the same series.
func TestComputeBenchmarking_APortfolioTrackingSPYHasZeroExcess(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"SPY": priced([]int{1, 2, 3}, []float64{100, 105, 110}),
		"QQQ": priced([]int{1, 2, 3}, []float64{50, 50, 50}),
	}
	// Exactly StartingBalance worth of SPY at day 1's close, so cash is zero.
	shares := service.StartingBalance / 100
	got := benchmarking(t, []service.Trade{buy("SPY", shares, 100, 1)}, barsBySymbol)

	spy := benchmarkFor(t, got, "SPY")
	assertClose(t, "portfolio return", got.PortfolioReturnPct, 10)
	assertClose(t, "SPY return", spy.ReturnPct, 10)
	assertClose(t, "excess", spy.ExcessReturnPct, 0)
	assertClose(t, "portfolio sharpe equals SPY's", got.PortfolioSharpe, spy.Sharpe)
}

// Excess is a simple difference, in the direction a reader expects: beating
// the benchmark is positive.
func TestComputeBenchmarking_ExcessIsPortfolioMinusBenchmark(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"SPY": priced([]int{1, 2}, []float64{100, 105}), // +5%
		"QQQ": priced([]int{1, 2}, []float64{100, 120}), // +20%
	}
	shares := service.StartingBalance / 100
	got := benchmarking(t, []service.Trade{buy("SPY", shares, 100, 1)}, barsBySymbol)

	// The portfolio is SPY, so it beats QQQ by -15 and matches SPY by 0.
	assertClose(t, "vs SPY", benchmarkFor(t, got, "SPY").ExcessReturnPct, 0)
	assertClose(t, "vs QQQ", benchmarkFor(t, got, "QQQ").ExcessReturnPct, 5-20)
}

// Sharpe comes from the shared primitive over the benchmark's own curve, not
// from a second implementation that could drift from backtesting's.
//
// The curve is built here WITH the share multiplier that buyAndHold omits,
// which makes this a check on the scale-invariance that omission relies on as
// well: if Sharpe ever stopped being invariant to a constant factor, dropping
// the multiply would become a behaviour change and this would fail.
func TestComputeBenchmarking_SharpeMatchesThePrimitive(t *testing.T) {
	closes := []float64{100, 102, 101, 106}
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2, 3, 4}, []float64{100, 100, 100, 100}),
		"SPY":  priced([]int{1, 2, 3, 4}, closes),
		"QQQ":  priced([]int{1, 2, 3, 4}, []float64{50, 50, 50, 50}),
	}
	got := benchmarking(t, []service.Trade{buy("AAPL", 10, 100, 1)}, barsBySymbol)

	// The curve buy-and-hold builds, computed here independently.
	shares := service.StartingBalance / closes[0]
	curve := make([]float64, len(closes))
	for i, c := range closes {
		curve[i] = shares * c
	}
	assertClose(t, "SPY sharpe", benchmarkFor(t, got, "SPY").Sharpe, portfoliomath.Sharpe(curve))
}

// Both benchmarks always appear, in a fixed order, with their labels. A
// shortened array is what §2.6 forbids -- the caller cannot see why one is
// missing.
func TestComputeBenchmarking_BothBenchmarksAlwaysPresentAndLabelled(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2}, []float64{100, 100}),
		"SPY":  priced([]int{1, 2}, []float64{100, 100}),
		"QQQ":  priced([]int{1, 2}, []float64{100, 100}),
	}
	got := benchmarking(t, []service.Trade{buy("AAPL", 10, 100, 1)}, barsBySymbol)

	if len(got.Benchmarks) != 2 {
		t.Fatalf("got %d benchmarks, want 2: %+v", len(got.Benchmarks), got.Benchmarks)
	}
	if got.Benchmarks[0].Symbol != "SPY" || got.Benchmarks[1].Symbol != "QQQ" {
		t.Errorf("order: got %s then %s, want SPY then QQQ",
			got.Benchmarks[0].Symbol, got.Benchmarks[1].Symbol)
	}
	if got.Benchmarks[0].Label != "S&P 500" || got.Benchmarks[1].Label != "NASDAQ 100" {
		t.Errorf("labels: got %q and %q", got.Benchmarks[0].Label, got.Benchmarks[1].Label)
	}
}

// One trading day is one point and no return, the same threshold risk uses.
func TestComputeBenchmarking_TooFewDaysIsInsufficientDataNotZeros(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1}, []float64{100}),
		"SPY":  priced([]int{1}, []float64{100}),
		"QQQ":  priced([]int{1}, []float64{100}),
	}
	got := benchmarking(t, []service.Trade{buy("AAPL", 10, 100, 1)}, barsBySymbol)

	if got.State != service.StateInsufficientData {
		t.Fatalf("state: got %q, want insufficient_data", got.State)
	}
	if got.Benchmarks == nil {
		t.Error("benchmarks is nil; it must marshal as [] rather than null")
	}
}

// A benchmark absent from the bars the calendar was built from is an internal
// inconsistency, not a user-facing condition -- so it is an error rather than
// a section quietly one entry short.
func TestComputeBenchmarking_AMissingBenchmarkCloseIsAnError(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2}, []float64{100, 100}),
		"SPY":  priced([]int{1, 2}, []float64{100, 110}),
		"QQQ":  priced([]int{1, 2}, []float64{100, 110}),
	}
	r := reconstruct(t, []service.Trade{buy("AAPL", 10, 100, 1)}, barsBySymbol)

	// The calendar was built with QQQ; this call is not.
	delete(barsBySymbol, "QQQ")

	_, err := service.ComputeBenchmarking(r, barsBySymbol)
	if !errors.Is(err, service.ErrMissingClose) {
		t.Fatalf("got %v, want ErrMissingClose", err)
	}
}

// The loop's own missing-close guard, which the whole-symbol-absent case above
// never reaches -- that one fails at the first-close check before the loop
// starts. Here SPY has a first close and is missing a LATER calendar date, so
// only the guard inside the loop can catch it.
//
// Without it the gap reads as a close of 0, and the curve carries a day the
// benchmark was worth nothing -- which would show up as a catastrophic
// drawdown in a perfectly healthy index.
func TestComputeBenchmarking_AGapMidWindowIsAnError(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2, 3}, []float64{100, 100, 100}),
		"SPY":  priced([]int{1, 2, 3}, []float64{100, 105, 110}),
		"QQQ":  priced([]int{1, 2, 3}, []float64{100, 105, 110}),
	}
	r := reconstruct(t, []service.Trade{buy("AAPL", 10, 100, 1)}, barsBySymbol)
	if len(r.Dates) != 3 {
		t.Fatalf("fixture: calendar has %d dates, want 3", len(r.Dates))
	}

	// SPY keeps day 1 (so the first-close check passes) but loses day 2.
	barsBySymbol["SPY"] = priced([]int{1, 3}, []float64{100, 110})

	_, err := service.ComputeBenchmarking(r, barsBySymbol)
	if !errors.Is(err, service.ErrMissingClose) {
		t.Fatalf("got %v, want ErrMissingClose", err)
	}
}

// A benchmark whose first close is zero cannot open the window. Not reachable
// from real market data, but dividing by it would turn every return in the
// section into an infinity, so it is refused rather than propagated.
func TestComputeBenchmarking_AZeroFirstCloseIsAnError(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{
		"AAPL": priced([]int{1, 2}, []float64{100, 100}),
		"SPY":  priced([]int{1, 2}, []float64{0, 110}),
		"QQQ":  priced([]int{1, 2}, []float64{100, 110}),
	}
	r := reconstruct(t, []service.Trade{buy("AAPL", 10, 100, 1)}, barsBySymbol)

	_, err := service.ComputeBenchmarking(r, barsBySymbol)
	if !errors.Is(err, service.ErrMissingClose) {
		t.Fatalf("got %v, want ErrMissingClose", err)
	}
}
