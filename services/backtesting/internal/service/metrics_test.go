package service

import "testing"

func pl(v float64) *float64 { return &v }

// TestComputeMetrics_ZeroVarianceSharpeIsZero: a flat equity curve (no
// trades ever executed, or a strategy that never crossed) has zero variance
// in its daily returns. SPEC.md §2.5 requires this to come back as a defined
// 0, not NaN from a 0/0 division.
func TestComputeMetrics_ZeroVarianceSharpeIsZero(t *testing.T) {
	result := SimulationResult{EquityCurve: []float64{1000, 1000, 1000, 1000}, FinalEquity: 1000}

	m := ComputeMetrics(result, 1000)

	if m.SharpeRatio != 0 {
		t.Errorf("Sharpe = %v, want 0 for a flat equity curve", m.SharpeRatio)
	}
}

// TestComputeMetrics_TooFewPointsSharpeIsZero: a single-bar curve has no
// return to compute at all -- also 0, not a divide-by-zero.
func TestComputeMetrics_TooFewPointsSharpeIsZero(t *testing.T) {
	result := SimulationResult{EquityCurve: []float64{1000}, FinalEquity: 1000}

	m := ComputeMetrics(result, 1000)

	if m.SharpeRatio != 0 {
		t.Errorf("Sharpe = %v, want 0 for a single-point curve", m.SharpeRatio)
	}
}

// TestComputeMetrics_NoTradesProfitFactorIsNil: a run with no closed trades
// at all has no gains and no losses -- the ratio is undefined, not 0.
func TestComputeMetrics_NoTradesProfitFactorIsNil(t *testing.T) {
	result := SimulationResult{EquityCurve: []float64{1000, 1000}, FinalEquity: 1000}

	m := ComputeMetrics(result, 1000)

	if m.ProfitFactor != nil {
		t.Errorf("ProfitFactor = %v, want nil (no closed trades)", *m.ProfitFactor)
	}
	if m.WinRatePct != 0 {
		t.Errorf("WinRatePct = %v, want 0 (no closed trades)", m.WinRatePct)
	}
}

// TestComputeMetrics_NoLosingTradesProfitFactorIsNil: SPEC.md §2.5's named
// edge case. Every closed trade won, so grossLoss is 0 and the ratio has no
// denominator -- nil, not 0 and not +Inf.
func TestComputeMetrics_NoLosingTradesProfitFactorIsNil(t *testing.T) {
	trades := []TradeRecord{
		{Side: SideSell, RealizedPL: pl(50)},
		{Side: SideSell, RealizedPL: pl(25)},
	}
	result := SimulationResult{EquityCurve: []float64{1000, 1075}, FinalEquity: 1075, Trades: trades}

	m := ComputeMetrics(result, 1000)

	if m.ProfitFactor != nil {
		t.Errorf("ProfitFactor = %v, want nil (no losing trades)", *m.ProfitFactor)
	}
	if m.WinRatePct != 100 {
		t.Errorf("WinRatePct = %v, want 100", m.WinRatePct)
	}
}

// TestComputeMetrics_ProfitFactorAndWinRate exercises the ordinary case: a
// mix of winning and losing closed trades produces a real, computed ratio.
func TestComputeMetrics_ProfitFactorAndWinRate(t *testing.T) {
	trades := []TradeRecord{
		{Side: SideSell, RealizedPL: pl(100)}, // win
		{Side: SideSell, RealizedPL: pl(-40)}, // loss
		{Side: SideSell, RealizedPL: pl(60)},  // win
		{Side: SideBuy},                       // buys never carry RealizedPL and must be ignored
	}
	result := SimulationResult{EquityCurve: []float64{1000, 1120}, FinalEquity: 1120, Trades: trades}

	m := ComputeMetrics(result, 1000)

	wantWinRate := 2.0 / 3.0 * 100
	if !approxEqual(m.WinRatePct, wantWinRate) {
		t.Errorf("WinRatePct = %v, want %v", m.WinRatePct, wantWinRate)
	}
	if m.ProfitFactor == nil {
		t.Fatal("ProfitFactor is nil, want a computed ratio")
	}
	wantPF := 160.0 / 40.0
	if !approxEqual(*m.ProfitFactor, wantPF) {
		t.Errorf("ProfitFactor = %v, want %v", *m.ProfitFactor, wantPF)
	}
}

// TestComputeMetrics_TotalReturnPct is a direct arithmetic check.
func TestComputeMetrics_TotalReturnPct(t *testing.T) {
	result := SimulationResult{EquityCurve: []float64{1000, 1250}, FinalEquity: 1250}

	m := ComputeMetrics(result, 1000)

	if !approxEqual(m.TotalReturnPct, 25) {
		t.Errorf("TotalReturnPct = %v, want 25", m.TotalReturnPct)
	}
}

// TestComputeMetrics_MaxDrawdownPct: the curve rises to a peak, falls to a
// trough, then partially recovers. Max drawdown must be measured from the
// peak to the trough that follows it, not from the start or the end.
func TestComputeMetrics_MaxDrawdownPct(t *testing.T) {
	result := SimulationResult{
		EquityCurve: []float64{1000, 1200, 900, 1100},
		FinalEquity: 1100,
	}

	m := ComputeMetrics(result, 1000)

	want := (1200.0 - 900.0) / 1200.0 * 100
	if !approxEqual(m.MaxDrawdownPct, want) {
		t.Errorf("MaxDrawdownPct = %v, want %v", m.MaxDrawdownPct, want)
	}
}

// TestComputeMetrics_MonotonicallyRisingCurveHasZeroDrawdown: a curve that
// only ever climbs never had a peak-to-trough decline.
func TestComputeMetrics_MonotonicallyRisingCurveHasZeroDrawdown(t *testing.T) {
	result := SimulationResult{EquityCurve: []float64{1000, 1100, 1200, 1300}, FinalEquity: 1300}

	m := ComputeMetrics(result, 1000)

	if m.MaxDrawdownPct != 0 {
		t.Errorf("MaxDrawdownPct = %v, want 0", m.MaxDrawdownPct)
	}
}
