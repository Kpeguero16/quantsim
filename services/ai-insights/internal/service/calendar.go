package service

import (
	"sort"
	"time"
)

// Day normalizes a timestamp to UTC midnight on its own calendar date.
//
// The returned value is canonical -- fixed clock, fixed location, no monotonic
// reading -- which is what makes it safe as a map key. A bare time.Time is
// not: two values denoting the same instant compare unequal as keys if they
// carry different locations or a monotonic reading, so a map of them would
// silently grow a second entry for a date it already had.
func Day(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Calendar returns the dates, ascending, for which EVERY symbol in
// barsBySymbol has a bar.
//
// Callers pass every symbol the reconstruction depends on: each symbol the
// account has ever held, plus the benchmarks. SPY is in that set because it is
// the benchmark and must be measured on exactly the days the portfolio is
// (SPEC.md §2.1, §2.6) -- comparing two series sampled on different dates is
// not a comparison.
//
// Intersection, and specifically NO carry-forward of a missing bar, is Step
// 19's alignBars rule applied unchanged. A carry-forward would invent a "last
// known price" concept this codebase has never had, and let staleness of
// unknown, unbounded age enter the equity curve without ever being visible in
// it. Every date returned here is a date on which a real close exists for
// every symbol the caller named.
//
// An empty result is a legitimate return, not an error: this function reports
// what the data supports, and it is the caller that knows whether "no dates"
// means ErrDateRangeUnavailable or an insufficient_data section. Passing no
// symbols at all likewise returns empty rather than "every date", which is the
// answer a set intersection over nothing would technically justify and no
// caller could use.
func Calendar(barsBySymbol map[string][]Bar) []time.Time {
	if len(barsBySymbol) == 0 {
		return nil
	}

	// How many distinct symbols have a bar on each date. A date is in the
	// intersection exactly when that count reaches the number of symbols.
	//
	// Counting distinct SYMBOLS rather than bars is what makes this correct if
	// a symbol ever arrives with more than one bar for a day: market-data
	// ingests only "1Day" bars today, so it does not happen, but two intraday
	// bars counted twice would let a single symbol satisfy the intersection on
	// its own. That is the silent failure Step 19 recorded against alignBars'
	// daily-bar assumption, and it costs one map to not inherit.
	seen := make(map[time.Time]map[string]struct{})
	for symbol, bars := range barsBySymbol {
		for _, b := range bars {
			day := Day(b.Timestamp)
			if seen[day] == nil {
				seen[day] = make(map[string]struct{})
			}
			seen[day][symbol] = struct{}{}
		}
	}

	dates := make([]time.Time, 0, len(seen))
	for day, symbols := range seen {
		if len(symbols) == len(barsBySymbol) {
			dates = append(dates, day)
		}
	}

	// Sorted explicitly, never left to map iteration order -- which Go
	// randomizes per run, so an unsorted result would be correct on most runs
	// and wrong on some.
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	return dates
}
