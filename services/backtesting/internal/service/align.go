package service

import (
	"sort"
	"time"
)

// alignBars turns N symbols' independently-fetched bars into one synchronized
// timeline (SPEC.md Step 19 §2.1): the symbols sorted alphabetically, and one
// equal-length []Bar per symbol covering only the dates *every* symbol has a
// bar for.
//
// Intersection, not union. A bar index i has to mean "the same trading day"
// for every symbol simultaneously, or SimulatePortfolio's mark-to-market
// equity and same-bar cash contention have no coherent meaning. The
// alternative -- simulate every date any symbol has, carrying a last-known
// price forward for the ones missing it -- would put staleness of an unknown,
// unbounded age into both fill prices and the equity curve. Here every bar in
// the aligned timeline is a real, same-day price for every symbol.
//
// The returned symbol order is what SimulatePortfolio uses to break same-bar
// cash contention (§2.2), and what the run persists (plan D4), so it is
// derived here rather than taken from the caller: two runs configured with the
// same symbol *set* align identically no matter what order they arrived in.
//
// An empty intersection returns empty slices, not an error -- RunBacktest is
// what maps that to ErrDateRangeUnavailable, the same way it already does for
// an empty sliceRange result.
func alignBars(bars map[string][]Bar, start, end time.Time) ([]string, [][]Bar) {
	symbols := make([]string, 0, len(bars))
	for symbol := range bars {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)

	if len(symbols) == 0 {
		return symbols, nil
	}

	// Ranged first, per symbol, so the intersection below only ever considers
	// dates the request actually asked for. At N=1 this is the whole function:
	// one symbol intersected with nothing is exactly sliceRange's output.
	ranged := make([][]Bar, len(symbols))
	for i, symbol := range symbols {
		ranged[i] = sliceRange(bars[symbol], start, end)
	}

	// Every symbol but the first, indexed by day, so membership is a lookup
	// rather than a scan. The first symbol stays a slice and drives the output
	// order, which keeps the result in the chronological order market-data
	// returned rather than a map's arbitrary one.
	byDay := make([]map[string]Bar, len(symbols))
	for i := 1; i < len(symbols); i++ {
		day := make(map[string]Bar, len(ranged[i]))
		for _, b := range ranged[i] {
			day[dayKey(b.Timestamp)] = b
		}
		byDay[i] = day
	}

	aligned := make([][]Bar, len(symbols))
	for i := range aligned {
		aligned[i] = []Bar{}
	}

	for _, driver := range ranged[0] {
		key := dayKey(driver.Timestamp)
		matches := make([]Bar, len(symbols))
		matches[0] = driver

		complete := true
		for i := 1; i < len(symbols); i++ {
			b, ok := byDay[i][key]
			if !ok {
				complete = false
				break
			}
			matches[i] = b
		}
		if !complete {
			continue
		}

		for i, b := range matches {
			aligned[i] = append(aligned[i], b)
		}
	}

	return symbols, aligned
}

// dayKey identifies the trading day a bar belongs to. These are daily bars, so
// the calendar date is the identity -- and a formatted date is deliberately
// used in preference to the time.Time itself, which is unsafe as a map key: two
// time.Time values naming the same instant compare unequal if their monotonic
// readings or *Location pointers differ, which would silently drop real
// matches. UTC matches how sliceRange already compares bars against
// dateLayout-parsed (and therefore UTC-midnight) range bounds.
//
// DAILY BARS ONLY, and alignBars fails silently if that ever stops being true.
// historical_prices already carries a timeframe column; every row is 1Day
// today and the client asks for nothing else. Were intraday bars to reach
// here, several bars would share one key: the byDay map keeps only the last
// of them, while the driver symbol still contributes one output row per bar,
// so the series would come back mismatched with no error raised. Anything
// introducing a second timeframe needs to key by the bar's actual granularity
// -- or reject the mismatch outright -- before it reaches this function.
func dayKey(t time.Time) string {
	return t.UTC().Format(dateLayout)
}
