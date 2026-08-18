package service

// GenerateSignals computes a moving-average crossover signal for every bar
// (SPEC.md §2.3). Bar i's signal is decided from bars[0..i] only -- never a
// later bar -- which is what makes Simulate's next-bar-open fill (§2.4) an
// honest simulation instead of trading on the future.
//
// A signal fires only on the crossing bar itself: the first bar where
// short > long after having been <=, or vice versa. Every bar the MAs spend
// on the same side afterward is SignalNone -- Simulate's own state (already
// in a position, or already flat) is what makes a repeated signal a no-op,
// but computing it here as a repeat would also require Simulate to guess
// which repeats to ignore.
//
// The first long_window-1 bars have no long MA yet and are always
// SignalNone -- there is nothing to cross until both averages exist.
func GenerateSignals(bars []Bar, shortWindow, longWindow int) []Signal {
	signals := make([]Signal, len(bars))
	if shortWindow <= 0 || longWindow <= shortWindow || longWindow > len(bars) {
		return signals
	}

	shortMA := movingAverages(bars, shortWindow)
	longMA := movingAverages(bars, longWindow)

	var wasAbove, haveState bool
	for i := longWindow - 1; i < len(bars); i++ {
		above := shortMA[i] > longMA[i]
		if haveState {
			switch {
			case above && !wasAbove:
				signals[i] = SignalBuy
			case !above && wasAbove:
				signals[i] = SignalSell
			}
		}
		wasAbove = above
		haveState = true
	}
	return signals
}

// movingAverages returns the trailing `window`-bar mean of Close, indexed the
// same as bars. Indices before window-1 are 0 and must not be read by the
// caller -- GenerateSignals never does, since it only starts at longWindow-1
// and longWindow >= shortWindow.
//
// A running sum keeps this one pass over bars rather than one pass per bar,
// which matters at MaxHistoryLimit-sized inputs (2000 bars) more than it
// would at the ~500 this project's watchlist actually has today.
func movingAverages(bars []Bar, window int) []float64 {
	result := make([]float64, len(bars))
	sum := 0.0
	for i, b := range bars {
		sum += b.Close
		if i >= window {
			sum -= bars[i-window].Close
		}
		if i >= window-1 {
			result[i] = sum / float64(window)
		}
	}
	return result
}
