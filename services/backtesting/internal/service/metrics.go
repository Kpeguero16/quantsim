package service

import "math"

// tradingDaysPerYear annualizes the Sharpe ratio. 252 is the standard
// convention for US equities (roughly the number of trading days in a
// calendar year) -- not derived from this project's own data, which is
// daily bars over a fixed historical window rather than a live calendar.
const tradingDaysPerYear = 252

// ComputeMetrics derives the five agents.md §3 values from one simulation's
// equity curve and trade log (SPEC.md §2.5).
func ComputeMetrics(result SimulationResult, startingCapital float64) Metrics {
	totalReturnPct := 0.0
	if startingCapital > 0 {
		totalReturnPct = (result.FinalEquity - startingCapital) / startingCapital * 100
	}

	winRatePct, profitFactor := tradeStats(result.Trades)

	return Metrics{
		TotalReturnPct: totalReturnPct,
		SharpeRatio:    sharpeRatio(result.EquityCurve),
		MaxDrawdownPct: maxDrawdownPct(result.EquityCurve),
		WinRatePct:     winRatePct,
		ProfitFactor:   profitFactor,
	}
}

// sharpeRatio annualizes the mean/stdev of the equity curve's daily returns.
// Population standard deviation (divide by n, not n-1) is used deliberately:
// a curve with exactly one usable return would make a sample stdev's n-1
// denominator zero, turning "not enough data" into a divide-by-zero instead
// of the flat, defined 0 this function returns for that case (SPEC.md §2.5 --
// "zero-variance ... returns 0, not NaN or a divide-by-zero").
func sharpeRatio(equity []float64) float64 {
	if len(equity) < 2 {
		return 0
	}

	returns := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		if equity[i-1] == 0 {
			continue
		}
		returns = append(returns, (equity[i]-equity[i-1])/equity[i-1])
	}
	if len(returns) == 0 {
		return 0
	}

	mean := meanOf(returns)
	stdev := stdevOf(returns, mean)
	if stdev == 0 {
		return 0
	}
	return mean / stdev * math.Sqrt(tradingDaysPerYear)
}

func meanOf(xs []float64) float64 {
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func stdevOf(xs []float64, mean float64) float64 {
	sumSq := 0.0
	for _, x := range xs {
		d := x - mean
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(xs)))
}

// maxDrawdownPct is the largest peak-to-trough decline anywhere in the
// curve, as a positive percentage. An equity curve that only ever rises
// (or is empty/flat) has a max drawdown of 0, not a negative number.
func maxDrawdownPct(equity []float64) float64 {
	if len(equity) == 0 {
		return 0
	}

	peak := equity[0]
	maxDD := 0.0
	for _, v := range equity {
		if v > peak {
			peak = v
		}
		if peak <= 0 {
			continue
		}
		if dd := (peak - v) / peak * 100; dd > maxDD {
			maxDD = dd
		}
	}
	return maxDD
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
