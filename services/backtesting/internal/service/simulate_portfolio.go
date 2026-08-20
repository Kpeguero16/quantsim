package service

// SimulatePortfolio turns N symbols' aligned bars and their signals into one
// trade log and one combined equity curve, drawn from a single shared pool of
// capital (Step 19 SPEC.md §2.2).
//
// symbols, bars and signals are parallel: symbols[s] owns bars[s] and
// signals[s], every bars[s] is the same length, and bar index i is the same
// trading day for every symbol -- all of which alignBars guarantees (§2.1).
// symbols arrives sorted alphabetically, which is what makes same-bar cash
// contention deterministic regardless of the order a request happened to name
// them in.
//
// Fills happen at bar i's OPEN in response to the signal from bar i-1's close,
// never bar i's own signal -- the one-bar lag that avoids lookahead bias,
// unchanged from the single-symbol engine this replaces. Equity is marked to
// market at every bar's CLOSE.
//
// Position sizing, per bar, in this order:
//
//  1. Every sell settles first. A sell liquidates that symbol's whole position
//     and its proceeds return to the SHARED cash balance -- the one rule that
//     makes this a pool rather than N sub-accounts. Sells precede buys so the
//     pool is genuinely shared *within* a bar, and so that behavior does not
//     depend on where the seller happens to fall in the alphabet.
//
//  2. The bar's target is computed once: equity at the open, divided by N.
//     Once per bar is exact rather than an approximation: a buy converts x of
//     cash into x worth of shares at the same open price it is priced at, so
//     no buy can change total equity at the open. Recomputing after each fill
//     would produce the identical number.
//
//  3. Buys, in symbol order, each capped at min(cash, target). A symbol that
//     finds the pool already exhausted simply does not fill -- the same "a
//     signal that doesn't result in a trade is a legitimate outcome"
//     convention the engine already had for "buy while already holding."
//
// Reading the target off *current* equity rather than off starting capital is
// what preserves compounding, and it is why a single-symbol run through this
// function is byte-identical to the old all-in Simulate: at N=1 a buy only
// ever happens while flat, so equity at the open is exactly the cash balance,
// target is that whole balance, and the fill spends all of it (§2.2, §5).
func SimulatePortfolio(symbols []string, bars [][]Bar, signals [][]Signal, startingCapital float64) SimulationResult {
	n := len(symbols)

	barCount := 0
	if n > 0 {
		barCount = len(bars[0])
	}

	cash := startingCapital
	quantity := make([]float64, n)
	avgCost := make([]float64, n)
	// Non-nil even with zero trades: BacktestDetail.Trades is a JSON array
	// field, and a nil slice marshals as `null`, not `[]`.
	trades := []TradeRecord{}
	equity := make([]float64, barCount)

	for i := 0; i < barCount; i++ {
		if i > 0 {
			// 1. Sells settle first, into the shared pool.
			for s := 0; s < n; s++ {
				if signals[s][i-1] != SignalSell || quantity[s] == 0 {
					continue
				}
				bar := bars[s][i]
				// Captured now, from the basis this position was bought at --
				// the same "read it at the instant of the sell, never
				// recompute it later" rule trading-engine's realized_pl
				// already follows.
				pl := (bar.Open - avgCost[s]) * quantity[s]
				cash += quantity[s] * bar.Open
				trades = append(trades, TradeRecord{
					Symbol: symbols[s], Side: SideSell, BarTimestamp: bar.Timestamp,
					Price: bar.Open, Quantity: quantity[s], RealizedPL: &pl,
				})
				quantity[s], avgCost[s] = 0, 0
			}

			// 2. One target for the whole bar, off equity at the open.
			equityAtOpen := cash
			for s := 0; s < n; s++ {
				equityAtOpen += quantity[s] * bars[s][i].Open
			}
			target := equityAtOpen / float64(n)

			// 3. Buys, in symbol order, capped by what the pool still holds.
			for s := 0; s < n; s++ {
				if signals[s][i-1] != SignalBuy || quantity[s] != 0 {
					continue
				}
				spend := min(cash, target)
				if spend <= 0 {
					continue
				}
				bar := bars[s][i]
				quantity[s] = spend / bar.Open
				avgCost[s] = bar.Open
				cash -= spend
				trades = append(trades, TradeRecord{
					Symbol: symbols[s], Side: SideBuy, BarTimestamp: bar.Timestamp,
					Price: bar.Open, Quantity: quantity[s],
				})
			}
		}

		// 4. Mark to market at the close.
		total := cash
		for s := 0; s < n; s++ {
			total += quantity[s] * bars[s][i].Close
		}
		equity[i] = total
	}

	final := startingCapital
	if len(equity) > 0 {
		final = equity[len(equity)-1]
	}
	return SimulationResult{Trades: trades, EquityCurve: equity, FinalEquity: final}
}
