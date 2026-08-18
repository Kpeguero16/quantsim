package service

// Simulate turns a bar series and its signals into a trade log and an
// equity curve (SPEC.md §2.3-2.4).
//
// Fills happen at bar i's OPEN in response to the signal from bar i-1's
// close, never bar i's own signal -- that one-bar lag is what avoids
// lookahead bias (§2.4). The signal on the very last bar therefore never
// fills; there is no bar after it to open at. Equity is marked to market at
// every bar's CLOSE, including the fill bar itself, so the curve reflects
// the fill that just happened rather than lagging it by another bar.
//
// All-in, long-only, one position (§2.3): a buy spends the entire cash
// balance; a sell liquidates the entire position. A buy signal while already
// holding, or a sell signal while flat, is a no-op -- both are reachable in
// principle (a strategy re-confirming its own position) even though
// GenerateSignals' edge-only firing makes them rare in practice.
func Simulate(bars []Bar, signals []Signal, startingCapital float64) SimulationResult {
	cash := startingCapital
	var quantity, avgCost float64
	var trades []TradeRecord
	equity := make([]float64, len(bars))

	for i, bar := range bars {
		if i > 0 {
			switch signals[i-1] {
			case SignalBuy:
				if quantity == 0 && cash > 0 {
					quantity = cash / bar.Open
					avgCost = bar.Open
					cash = 0
					trades = append(trades, TradeRecord{
						Side: SideBuy, BarTimestamp: bar.Timestamp,
						Price: bar.Open, Quantity: quantity,
					})
				}
			case SignalSell:
				if quantity > 0 {
					// Captured now, from the basis this position was bought
					// at -- the same "read it at the instant of the sell,
					// never recompute it later" rule trading-engine's
					// realized_pl already follows.
					pl := (bar.Open - avgCost) * quantity
					cash = quantity * bar.Open
					trades = append(trades, TradeRecord{
						Side: SideSell, BarTimestamp: bar.Timestamp,
						Price: bar.Open, Quantity: quantity, RealizedPL: &pl,
					})
					quantity, avgCost = 0, 0
				}
			}
		}
		equity[i] = cash + quantity*bar.Close
	}

	final := startingCapital
	if len(equity) > 0 {
		final = equity[len(equity)-1]
	}
	return SimulationResult{Trades: trades, EquityCurve: equity, FinalEquity: final}
}
