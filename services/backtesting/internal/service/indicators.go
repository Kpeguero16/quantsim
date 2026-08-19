package service

// ema returns the period-length exponential moving average of values,
// indexed the same as values. Seeded with the simple mean of the first
// `period` values (the standard construction -- SPEC.md Step 18 §2.3);
// seeding with the first value alone converges to the same series but is
// materially wrong for the first several periods, which is exactly the
// region a short backtest range lives in.
//
// result[i] for i < period-1 is 0 and must not be read by the caller,
// matching movingAverages' own convention. An all-zero slice is returned if
// period is non-positive or exceeds len(values), rather than panicking.
func ema(values []float64, period int) []float64 {
	result := make([]float64, len(values))
	if period <= 0 || period > len(values) {
		return result
	}

	sum := 0.0
	for i := 0; i < period; i++ {
		sum += values[i]
	}
	seed := sum / float64(period)
	result[period-1] = seed

	alpha := 2.0 / float64(period+1)
	prev := seed
	for i := period; i < len(values); i++ {
		prev = values[i]*alpha + prev*(1-alpha)
		result[i] = prev
	}
	return result
}

// wilderRSI returns the period-length Wilder-smoothed Relative Strength
// Index of closes, indexed the same as closes -- Wilder's original
// smoothing (SPEC.md §2.2), not the simple-average "Cutler's RSI" variant:
// this is what every charting platform computes, and an RSI(14) that
// quietly disagreed with a user's own chart would be a correctness
// embarrassment.
//
// result[i] for i < period is 0 and must not be read by the caller -- the
// first value needs `period` deltas, which exist only once i >= period.
//
// Degenerate windows never produce NaN or Inf, deliberately (§2.2):
//   - all gains, no losses -> 100, the formula's own ceiling
//   - all losses, no gains -> 0
//   - no movement at all   -> 50, the honest neutral. A naive RS = 0/0
//     short-circuit typically returns 100 here by accident; a perfectly
//     flat series has no upward pressure whatsoever to justify that, so
//     this engine returns the neutral reading instead.
func wilderRSI(closes []float64, period int) []float64 {
	result := make([]float64, len(closes))
	if period <= 0 || period >= len(closes) {
		return result
	}

	var gainSum, lossSum float64
	for i := 1; i <= period; i++ {
		delta := closes[i] - closes[i-1]
		if delta > 0 {
			gainSum += delta
		} else {
			lossSum += -delta
		}
	}
	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)
	result[period] = rsiFromAverages(avgGain, avgLoss)

	for i := period + 1; i < len(closes); i++ {
		delta := closes[i] - closes[i-1]
		gain, loss := 0.0, 0.0
		if delta > 0 {
			gain = delta
		} else {
			loss = -delta
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
		result[i] = rsiFromAverages(avgGain, avgLoss)
	}
	return result
}

// rsiFromAverages turns one bar's smoothed gain/loss averages into an RSI
// reading, handling the three degenerate cases documented on wilderRSI
// before falling through to the standard 100 - 100/(1+RS) formula.
func rsiFromAverages(avgGain, avgLoss float64) float64 {
	switch {
	case avgGain == 0 && avgLoss == 0:
		return 50
	case avgLoss == 0:
		return 100
	case avgGain == 0:
		return 0
	default:
		rs := avgGain / avgLoss
		return 100 - 100/(1+rs)
	}
}
