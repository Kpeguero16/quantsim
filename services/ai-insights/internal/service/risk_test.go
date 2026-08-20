package service_test

import (
	"math"
	"testing"

	"github.com/kpeguero/quantsim/pkg/portfoliomath"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

func computeRisk(t *testing.T, trades []service.Trade, barsBySymbol map[string][]service.Bar) service.RiskSection {
	t.Helper()
	got, err := service.ComputeRisk(reconstruct(t, trades, barsBySymbol), barsBySymbol)
	if err != nil {
		t.Fatalf("ComputeRisk: %v", err)
	}
	return got
}

// A single holding is maximum concentration by definition.
func TestComputeRisk_HHIOfOneHoldingIsOne(t *testing.T) {
	got := computeRisk(t,
		[]service.Trade{buy("AAPL", 10, 100, 1)},
		map[string][]service.Bar{"AAPL": bars(1, 2, 3), "SPY": bars(1, 2, 3)})

	if got.State != service.StateOK {
		t.Fatalf("state = %q, want ok", got.State)
	}
	if math.Abs(got.ConcentrationHHI-1.0) > eps {
		t.Errorf("HHI = %v, want 1.0", got.ConcentrationHHI)
	}
}

// n equal holdings give 1/n. bars() prices every symbol identically, so equal
// quantities are equal market values.
func TestComputeRisk_HHIOfNEqualHoldingsIsOneOverN(t *testing.T) {
	tests := []struct {
		n       int
		symbols []string
	}{
		{2, []string{"AAPL", "MSFT"}},
		{4, []string{"AAPL", "MSFT", "GOOGL", "AMZN"}},
	}

	for _, tt := range tests {
		barsBySymbol := map[string][]service.Bar{"SPY": bars(1, 2, 3)}
		var trades []service.Trade
		for _, s := range tt.symbols {
			barsBySymbol[s] = bars(1, 2, 3)
			trades = append(trades, buy(s, 10, 100, 1))
		}

		got := computeRisk(t, trades, barsBySymbol)
		want := 1 / float64(tt.n)
		if math.Abs(got.ConcentrationHHI-want) > eps {
			t.Errorf("n=%d: HHI = %v, want %v", tt.n, got.ConcentrationHHI, want)
		}
	}
}

// The failure §2.5 names explicitly. Folding cash into the index would make an
// all-cash portfolio report perfect concentration and perfect safety from the
// same number.
func TestComputeRisk_AnAllCashPortfolioIsNotMaximallyConcentrated(t *testing.T) {
	got := computeRisk(t,
		[]service.Trade{buy("AAPL", 10, 100, 1), sell("AAPL", 10, 110, 2)},
		map[string][]service.Bar{"AAPL": bars(1, 2, 3), "SPY": bars(1, 2, 3)})

	if len(got.Positions) != 0 {
		t.Fatalf("positions = %v, want none held", got.Positions)
	}
	if got.ConcentrationHHI != 0 {
		t.Errorf("HHI = %v, want 0 -- holding nothing is not concentrated risk", got.ConcentrationHHI)
	}
	if math.Abs(got.CashWeightPct-100) > eps {
		t.Errorf("cash weight = %v%%, want 100", got.CashWeightPct)
	}
}

// Positions and cash are shares of the same total, so they sum to 100.
func TestComputeRisk_WeightsAndCashSumToOneHundred(t *testing.T) {
	got := computeRisk(t,
		[]service.Trade{buy("AAPL", 10, 100, 1), buy("MSFT", 25, 100, 1)},
		map[string][]service.Bar{"AAPL": bars(1, 2, 3), "MSFT": bars(1, 2, 3), "SPY": bars(1, 2, 3)})

	total := got.CashWeightPct
	for _, p := range got.Positions {
		total += p.WeightPct
	}
	if math.Abs(total-100) > 1e-6 {
		t.Errorf("weights sum to %v%%, want 100", total)
	}
}

// Largest first, so the reader sees the concentration before the tail, and so
// the order does not vary with map iteration.
func TestComputeRisk_PositionsAreSortedLargestFirst(t *testing.T) {
	got := computeRisk(t,
		[]service.Trade{buy("AAPL", 5, 100, 1), buy("MSFT", 50, 100, 1), buy("TSLA", 20, 100, 1)},
		map[string][]service.Bar{
			"AAPL": bars(1, 2, 3), "MSFT": bars(1, 2, 3),
			"TSLA": bars(1, 2, 3), "SPY": bars(1, 2, 3),
		})

	if len(got.Positions) != 3 {
		t.Fatalf("got %d positions, want 3", len(got.Positions))
	}
	want := []string{"MSFT", "TSLA", "AAPL"}
	for i, symbol := range want {
		if got.Positions[i].Symbol != symbol {
			t.Errorf("position %d = %s, want %s (order: %v)", i, got.Positions[i].Symbol, symbol, got.Positions)
		}
	}
	if math.Abs(got.LargestPositionPct-got.Positions[0].WeightPct) > eps {
		t.Errorf("largest_position_pct = %v, want the top position's weight %v",
			got.LargestPositionPct, got.Positions[0].WeightPct)
	}
}

// Volatility and drawdown are pkg/portfoliomath's, wired to the reconstructed
// curve. The MATHS is covered by that package's own hand-computed tests; what
// is asserted here is the wiring -- that this section reports the curve it was
// given, annualized, rather than something subtly else.
func TestComputeRisk_VolatilityAndDrawdownComeFromTheCurve(t *testing.T) {
	barsBySymbol := map[string][]service.Bar{"AAPL": bars(1, 2, 3, 4), "SPY": bars(1, 2, 3, 4)}
	trades := []service.Trade{buy("AAPL", 500, 100, 1)}

	r := reconstruct(t, trades, barsBySymbol)
	got := computeRisk(t, trades, barsBySymbol)

	returns := portfoliomath.DailyReturns(r.Equity)
	wantVol := portfoliomath.StdevPopulation(returns, portfoliomath.Mean(returns)) *
		math.Sqrt(portfoliomath.TradingDaysPerYear) * 100
	if math.Abs(got.AnnualizedVolatilityPct-wantVol) > eps {
		t.Errorf("volatility = %v, want %v", got.AnnualizedVolatilityPct, wantVol)
	}
	if math.Abs(got.MaxDrawdownPct-portfoliomath.MaxDrawdownPct(r.Equity)) > eps {
		t.Errorf("drawdown = %v, want %v", got.MaxDrawdownPct, portfoliomath.MaxDrawdownPct(r.Equity))
	}
	// A rising curve has no drawdown -- never a negative number.
	if got.MaxDrawdownPct < 0 {
		t.Errorf("drawdown = %v, must never be negative", got.MaxDrawdownPct)
	}
}

// Zeros are values. A one-day curve reported as 0.0% volatility would read as
// a measurement of a portfolio that has barely traded (SPEC.md §2.10).
func TestComputeRisk_TooFewDaysIsInsufficientDataNotZeros(t *testing.T) {
	tests := []struct {
		name      string
		barDays   []int
		wantState string
	}{
		{"no trades at all", []int{1, 2, 3}, service.StateInsufficientData},
		{"one trading day", []int{1}, service.StateInsufficientData},
		{"two trading days", []int{1, 2}, service.StateOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			barsBySymbol := map[string][]service.Bar{
				"AAPL": bars(tt.barDays...), "SPY": bars(tt.barDays...),
			}
			var trades []service.Trade
			if tt.name != "no trades at all" {
				trades = []service.Trade{buy("AAPL", 10, 100, 1)}
			}

			got := computeRisk(t, trades, barsBySymbol)

			if got.State != tt.wantState {
				t.Fatalf("state = %q, want %q", got.State, tt.wantState)
			}
			if got.State == service.StateInsufficientData {
				if got.Reason == "" {
					t.Error("insufficient_data with no reason")
				}
				if got.AnnualizedVolatilityPct != 0 || got.ConcentrationHHI != 0 || len(got.Positions) != 0 {
					t.Errorf("insufficient_data section carries figures: %+v", got)
				}
			}
		})
	}
}
