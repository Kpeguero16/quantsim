package service

import (
	"time"

	"github.com/kpeguero/quantsim/pkg/portfoliomath"
)

// benchmarkLabels are the human names for §2.6's two comparisons. A symbol
// with no entry falls back to the symbol itself rather than an empty string,
// so adding a benchmark to BenchmarkSymbols and forgetting the label degrades
// to something readable instead of a blank in the UI.
var benchmarkLabels = map[string]string{
	"SPY": "S&P 500",
	"QQQ": "NASDAQ 100",
}

// Benchmark is one buy-and-hold comparison (SPEC.md §2.6).
//
// ExcessReturnPct is a simple difference, not a regression alpha. A
// beta-adjusted alpha over a handful of positions and a window this short
// would be a number with more decimal places and less meaning.
type Benchmark struct {
	Symbol          string  `json:"symbol"`
	Label           string  `json:"label"`
	ReturnPct       float64 `json:"return_pct"`
	ExcessReturnPct float64 `json:"excess_return_pct"`
	Sharpe          float64 `json:"sharpe"`
}

// BenchmarkingSection is SPEC.md §2.6.
//
// Like RiskSection, the figures carry no omitempty: a benchmark that returned
// exactly 0.0% over the window, or a portfolio that matched its benchmark
// precisely (ExcessReturnPct 0), are measurements, and an omitted key reads as
// "not computed".
type BenchmarkingSection struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`

	PortfolioReturnPct float64     `json:"portfolio_return_pct"`
	PortfolioSharpe    float64     `json:"portfolio_sharpe"`
	Benchmarks         []Benchmark `json:"benchmarks"`
}

// ComputeBenchmarking compares the portfolio against buy-and-hold SPY and QQQ
// over the identical trading days (SPEC.md §2.6).
//
// "Identical" is guaranteed by construction rather than by a check here: both
// benchmarks are members of the calendar intersection (see BenchmarkSymbols),
// so every date in r.Dates is a date on which both have a real close. The
// benchmark series is built by indexing r.Dates directly for exactly that
// reason -- reading each benchmark's own first and last bar instead would
// measure it over a window the portfolio was never measured over, and
// comparing two series sampled on different dates is not a comparison.
//
// Both benchmarks are non-optional. A missing one is ErrSymbolUnavailable at
// the fetch, long before here; reaching ErrMissingClose instead means the
// calendar was built from a different symbol set than this was called with.
func ComputeBenchmarking(r Reconstruction, barsBySymbol map[string][]Bar) (BenchmarkingSection, error) {
	if len(r.Dates) < minTradingDays {
		return BenchmarkingSection{
			State:      StateInsufficientData,
			Reason:     "fewer than two trading days of history",
			Benchmarks: []Benchmark{},
		}, nil
	}

	closes := closesBySymbolAndDay(barsBySymbol)
	portfolioReturn := totalReturnPct(r.Equity)

	benchmarks := make([]Benchmark, 0, len(BenchmarkSymbols))
	for _, symbol := range BenchmarkSymbols {
		curve, err := buyAndHold(symbol, r.Dates, closes)
		if err != nil {
			return BenchmarkingSection{}, err
		}

		label, ok := benchmarkLabels[symbol]
		if !ok {
			label = symbol
		}

		benchmarkReturn := totalReturnPct(curve)
		benchmarks = append(benchmarks, Benchmark{
			Symbol:          symbol,
			Label:           label,
			ReturnPct:       benchmarkReturn,
			ExcessReturnPct: portfolioReturn - benchmarkReturn,
			Sharpe:          portfoliomath.Sharpe(curve),
		})
	}

	return BenchmarkingSection{
		State:              StateOK,
		PortfolioReturnPct: portfolioReturn,
		PortfolioSharpe:    portfoliomath.Sharpe(r.Equity),
		Benchmarks:         benchmarks,
	}, nil
}

// buyAndHold returns the value series of holding one symbol across exactly the
// dates given -- §2.6's "place the whole StartingBalance in at the first close
// and hold to the last".
//
// The series is the closes themselves rather than closes scaled by a share
// count, and the omission is deliberate rather than a shortcut. Buying
// StartingBalance/first shares multiplies every point by one constant, and
// BOTH figures taken from this curve are scale-invariant: a total return is a
// ratio of two points, and Sharpe is built from day-over-day ratios. The share
// count therefore cancels out of everything reported. Writing the multiply
// anyway would put a line in the arithmetic that looks load-bearing, is not,
// and quietly invites a reader to believe the numbers depend on the starting
// capital -- they do not, and that is worth being obvious about.
//
// (It also sidesteps a question §2.6 would otherwise have to answer: whole
// shares would leave a stub of uninvested cash and make each benchmark's
// return depend on its own share PRICE, so QQQ would differ from SPY for a
// reason having nothing to do with either index.)
func buyAndHold(symbol string, dates []time.Time, closes map[string]map[time.Time]float64) ([]float64, error) {
	// An absent close and a zero close are the same condition -- neither is a
	// price the window can be opened at. A zero is not reachable from real
	// data (a listed instrument does not close at nothing), but it would make
	// every return in the section an infinity rather than an error.
	if first, ok := closes[symbol][dates[0]]; !ok || first == 0 {
		return nil, missingCloseError(symbol, dates[0])
	}

	curve := make([]float64, len(dates))
	for i, date := range dates {
		px, ok := closes[symbol][date]
		if !ok {
			return nil, missingCloseError(symbol, date)
		}
		curve[i] = px
	}
	return curve, nil
}

// totalReturnPct is the simple return from a curve's first point to its last.
//
// A zero opening value returns 0 rather than dividing by it, matching
// portfoliomath.DailyReturns' own guard: a portfolio worth nothing on day one
// has no percentage to grow by, and an infinity here would propagate into
// every excess-return figure in the section.
func totalReturnPct(curve []float64) float64 {
	if len(curve) == 0 || curve[0] == 0 {
		return 0
	}
	return (curve[len(curve)-1]/curve[0] - 1) * 100
}
