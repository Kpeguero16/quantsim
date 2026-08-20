package service

import "github.com/kpeguero/quantsim/pkg/portfoliomath"

// ComputeMetrics derives the five agents.md §3 values from one simulation's
// equity curve and trade log (SPEC.md §2.5).
//
// The Sharpe ratio and max drawdown are computed by pkg/portfoliomath, which
// is where they live now: Phase 4's ai-insights service measures a real
// portfolio's reconstructed curve with the same two functions, and a second
// implementation of either would be free to drift from this one
// (Step 20 SPEC.md §2.3). The move was behaviour-free -- this file's tests
// needed no edit to prove it.
func ComputeMetrics(result SimulationResult, startingCapital float64) Metrics {
	totalReturnPct := 0.0
	if startingCapital > 0 {
		totalReturnPct = (result.FinalEquity - startingCapital) / startingCapital * 100
	}

	winRatePct, profitFactor := tradeStats(result.Trades)

	return Metrics{
		TotalReturnPct: totalReturnPct,
		SharpeRatio:    portfoliomath.Sharpe(result.EquityCurve),
		MaxDrawdownPct: portfoliomath.MaxDrawdownPct(result.EquityCurve),
		WinRatePct:     winRatePct,
		ProfitFactor:   profitFactor,
	}
}

// tradeStats derives win rate and profit factor from the sell side of the
// trade log -- each sell is one closed round trip (§2.3's all-in rule means
// there is never a partial close), and only sells carry RealizedPL. An open
// position at the end of the range was never sold and is excluded from both
// numbers rather than counted as a loss (SPEC.md §2.5).
func tradeStats(trades []TradeRecord) (winRatePct float64, profitFactor *float64) {
	var closed, wins int
	var grossGain, grossLoss float64

	for _, t := range trades {
		if t.Side != SideSell || t.RealizedPL == nil {
			continue
		}
		closed++
		switch pl := *t.RealizedPL; {
		case pl > 0:
			wins++
			grossGain += pl
		case pl < 0:
			grossLoss += -pl
		}
	}

	if closed == 0 {
		return 0, nil
	}
	winRatePct = float64(wins) / float64(closed) * 100

	if grossLoss == 0 {
		// No losing trade to divide by -- the ratio is undefined, not 0 and
		// not +Inf (SPEC.md §2.5).
		return winRatePct, nil
	}
	pf := grossGain / grossLoss
	return winRatePct, &pf
}
