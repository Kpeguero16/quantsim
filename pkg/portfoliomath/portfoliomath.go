// Package portfoliomath holds the statistical primitives shared by anything
// in QuantSim that measures an equity curve.
//
// It exists because there is more than one such thing. Phase 3's backtesting
// engine computes a Sharpe ratio and a max drawdown over a simulated curve;
// Phase 4's ai-insights service computes the same two quantities over a curve
// reconstructed from a real trade log, and derives volatility from
// StdevPopulation and DailyReturns here rather than re-deriving either. Two
// implementations of "Sharpe ratio" in one system are two things that are
// supposed to agree forever and are free to drift, verified by nothing --
// the shape of duplication Step 19 deleted rather than tested around.
//
// Everything here takes and returns plain []float64 and float64. It knows
// nothing about backtests, portfolios, trades or symbols, and imports no
// service package; a caller's domain types stop at its boundary.
package portfoliomath

import "math"

// TradingDaysPerYear annualizes a daily figure. 252 is the standard
// convention for US equities (roughly the number of trading days in a
// calendar year) -- not derived from this project's own data, which is
// daily bars over a fixed historical window rather than a live calendar.
const TradingDaysPerYear = 252

// DailyReturns converts an equity curve into its period-over-period returns.
//
// A period whose *opening* value is zero is skipped rather than producing a
// division by zero, so the result can be shorter than len(equity)-1. That
// case means an account that was momentarily worth nothing, which has no
// meaningful return to report -- not a return of zero, which would be a claim
// that nothing happened.
func DailyReturns(equity []float64) []float64 {
	if len(equity) < 2 {
		return nil
	}

	returns := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		if equity[i-1] == 0 {
			continue
		}
		returns = append(returns, (equity[i]-equity[i-1])/equity[i-1])
	}
	return returns
}

// Sharpe annualizes the mean/stdev of an equity curve's daily returns,
// against a risk-free rate of zero.
//
// Population standard deviation (divide by n, not n-1) is used deliberately:
// a curve with exactly one usable return would make a sample stdev's n-1
// denominator zero, turning "not enough data" into a divide-by-zero instead
// of the flat, defined 0 this function returns for that case.
func Sharpe(equity []float64) float64 {
	returns := DailyReturns(equity)
	if len(returns) == 0 {
		return 0
	}

	mean := Mean(returns)
	stdev := StdevPopulation(returns, mean)
	if stdev == 0 {
		return 0
	}
	return mean / stdev * math.Sqrt(TradingDaysPerYear)
}

// Mean is the arithmetic mean of xs. Callers must not pass an empty slice --
// the mean of nothing is not zero, and every caller here has already
// established that xs is non-empty for its own reasons.
func Mean(xs []float64) float64 {
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// StdevPopulation is the population standard deviation of xs about mean --
// divided by n, not n-1. See Sharpe for why that choice is deliberate rather
// than an oversight.
//
// mean is a parameter rather than recomputed because every caller has already
// needed it for something else.
func StdevPopulation(xs []float64, mean float64) float64 {
	sumSq := 0.0
	for _, x := range xs {
		d := x - mean
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(xs)))
}

// MaxDrawdownPct is the largest peak-to-trough decline anywhere in the
// curve, as a positive percentage. An equity curve that only ever rises
// (or is empty/flat) has a max drawdown of 0, not a negative number.
func MaxDrawdownPct(equity []float64) float64 {
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
