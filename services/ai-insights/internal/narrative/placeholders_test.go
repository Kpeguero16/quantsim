package narrative_test

import (
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// fullReport is a report with all three sections computed, which is the case
// every token has to be offerable in.
func fullReport() service.PortfolioInsights {
	return service.PortfolioInsights{
		AsOfDate: "2026-08-14",
		Window:   service.Window{StartDate: "2026-06-01", TradingDays: 52},
		Risk: service.RiskSection{
			State: service.StateOK,
			Positions: []service.PositionWeight{
				{Symbol: "AAPL", Quantity: 10, MarketValue: 1750.50, WeightPct: 62.5},
				{Symbol: "MSFT", Quantity: 5, MarketValue: 1050, WeightPct: 37.5},
			},
			CashWeightPct:           12.5,
			ConcentrationHHI:        0.531,
			LargestPositionPct:      62.5,
			AnnualizedVolatilityPct: 18.25,
			MaxDrawdownPct:          12.4, // positive: portfoliomath reports drawdown as a magnitude
		},
		Benchmarking: service.BenchmarkingSection{
			State:              service.StateOK,
			PortfolioReturnPct: 7.25,
			PortfolioSharpe:    0.84,
			Benchmarks: []service.Benchmark{
				{Symbol: "SPY", Label: "S&P 500", ReturnPct: 8.1, ExcessReturnPct: -0.85, Sharpe: 1.02},
				{Symbol: "QQQ", Label: "Nasdaq 100", ReturnPct: 11.3, ExcessReturnPct: -4.05, Sharpe: 1.31},
			},
		},
		Behavior: service.BehaviorSection{
			State:      service.StateOK,
			TradeCount: 34,
			Findings: []service.Finding{
				{Code: "overtrading", Severity: "medium", TurnoverRatio: 3.4},
				{Code: "panic_selling", Severity: "high", Occurrences: 2},
			},
		},
	}
}

func TestPlaceholders_OffersEveryStaticTokenForAComputedReport(t *testing.T) {
	got := narrative.Placeholders(fullReport())

	want := []string{
		"as_of_date", "window.start_date", "window.trading_days",
		"risk.position_count", "risk.cash_weight_pct", "risk.concentration_hhi",
		"risk.largest_position_pct", "risk.largest_position_symbol",
		"risk.annualized_volatility_pct", "risk.max_drawdown_pct",
		"benchmarking.portfolio_return_pct", "benchmarking.portfolio_sharpe",
		"behavior.trade_count", "behavior.finding_count",
	}
	for _, token := range want {
		if _, ok := got[token]; !ok {
			t.Errorf("token %q is not offered", token)
		}
	}
}

// The symbol-keyed tokens must come from the report's own holdings and
// benchmarks. A constant list would be right until the day someone holds
// something that is not on it.
func TestPlaceholders_SymbolTokensFollowTheReportNotAConstantList(t *testing.T) {
	r := fullReport()
	r.Risk.Positions = []service.PositionWeight{{Symbol: "ZZZZ", Quantity: 1, MarketValue: 10, WeightPct: 100}}
	r.Benchmarking.Benchmarks = []service.Benchmark{{Symbol: "WXYZ", ReturnPct: 1, Sharpe: 0.1}}

	got := narrative.Placeholders(r)

	for _, token := range []string{
		"risk.positions.ZZZZ.weight_pct", "risk.positions.ZZZZ.market_value",
		"risk.positions.ZZZZ.quantity", "benchmarking.WXYZ.return_pct",
		"benchmarking.WXYZ.excess_return_pct", "benchmarking.WXYZ.sharpe",
	} {
		if _, ok := got[token]; !ok {
			t.Errorf("token %q is not offered; the vocabulary is not derived from the report", token)
		}
	}
	for token := range got {
		if strings.Contains(token, "AAPL") || strings.Contains(token, "SPY") {
			t.Errorf("token %q survives from a constant list; nothing in the report mentions it", token)
		}
	}
}

// A token whose underlying figure was never computed must not be offerable.
// An insufficient_data section renders zeros beside its state (service
// risk.go:51), and offering those zeros as figures would let the model phrase
// a measurement that does not exist.
func TestPlaceholders_DegradedSectionOffersNoneOfItsTokens(t *testing.T) {
	for _, tc := range []struct{ name, prefix string }{
		{"risk", "risk."},
		{"benchmarking", "benchmarking."},
		{"behavior", "behavior."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fullReport()
			switch tc.name {
			case "risk":
				r.Risk = service.RiskSection{State: service.StateInsufficientData, Reason: "no trades yet"}
			case "benchmarking":
				r.Benchmarking = service.BenchmarkingSection{State: service.StateInsufficientData, Reason: "no trades yet"}
			case "behavior":
				r.Behavior = service.BehaviorSection{State: service.StateInsufficientData, Reason: "no trades yet"}
			}

			for token := range narrative.Placeholders(r) {
				if strings.HasPrefix(token, tc.prefix) {
					t.Errorf("degraded %s section still offers %q", tc.name, token)
				}
			}
		})
	}
}

func TestPlaceholders_EveryValueCarriesAKind(t *testing.T) {
	for token, v := range narrative.Placeholders(fullReport()) {
		if v.Kind == 0 {
			t.Errorf("token %q has no kind; it would render through a default branch", token)
		}
	}
}

func TestPlaceholders_AllCashPortfolioOffersNoPositionTokens(t *testing.T) {
	r := fullReport()
	r.Risk.Positions = nil
	r.Risk.CashWeightPct = 100

	got := narrative.Placeholders(r)

	for token := range got {
		if strings.HasPrefix(token, "risk.positions.") {
			t.Errorf("token %q offered for a portfolio holding nothing", token)
		}
	}
	// Nothing is the largest of nothing. Offering the token would make the
	// model name a holding that does not exist.
	if _, ok := got["risk.largest_position_symbol"]; ok {
		t.Error("risk.largest_position_symbol offered with no positions")
	}
	if v, ok := got["risk.position_count"]; !ok || v.Num != 0 {
		t.Errorf("risk.position_count = %v, want an offered 0 -- a count of zero is a real measurement", v)
	}
}

// TurnoverRatio and Occurrences carry omitempty: their zero is "absent", not
// "measured as zero". A token for a figure the rule never computed is exactly
// the hallucinated reference the validator exists to catch, so it must not be
// offered in the first place.
func TestPlaceholders_FindingTokensOnlyWhereTheFigureWasComputed(t *testing.T) {
	got := narrative.Placeholders(fullReport())

	if _, ok := got["behavior.findings.overtrading.turnover_ratio"]; !ok {
		t.Error("overtrading has a turnover ratio and offers no token for it")
	}
	if _, ok := got["behavior.findings.overtrading.occurrences"]; ok {
		t.Error("overtrading has no occurrence count, but a token was offered for it")
	}
	if _, ok := got["behavior.findings.panic_selling.occurrences"]; !ok {
		t.Error("panic_selling has an occurrence count and offers no token for it")
	}
	if _, ok := got["behavior.findings.panic_selling.turnover_ratio"]; ok {
		t.Error("panic_selling has no turnover ratio, but a token was offered for it")
	}
	if v, ok := got["behavior.finding_count"]; !ok || v.Num != 2 {
		t.Errorf("behavior.finding_count = %v, want 2", v)
	}
}

// A fully degraded report has nothing to phrase at all (SPEC 2.12), and the
// caller uses an empty vocabulary as that signal.
func TestPlaceholders_FullyDegradedReportOffersNothing(t *testing.T) {
	r := service.PortfolioInsights{
		Risk:         service.RiskSection{State: service.StateInsufficientData},
		Benchmarking: service.BenchmarkingSection{State: service.StateInsufficientData},
		Behavior:     service.BehaviorSection{State: service.StateInsufficientData},
	}
	if got := narrative.Placeholders(r); len(got) != 0 {
		t.Errorf("a report with nothing computed offers %d tokens: %v", len(got), got)
	}
}

// realShapedReport mirrors an actual response from the running service, with
// the sign conventions the service really produces: drawdown positive,
// returns signed, weights positive.
func realShapedReport() service.PortfolioInsights {
	return service.PortfolioInsights{
		AsOfDate: "2026-07-28",
		Window:   service.Window{StartDate: "2026-06-01", TradingDays: 40},
		Risk: service.RiskSection{
			State: service.StateOK,
			Positions: []service.PositionWeight{
				{Symbol: "AAPL", Quantity: 12, MarketValue: 4080.06, WeightPct: 4.094816901148358},
				{Symbol: "MSFT", Quantity: 10, MarketValue: 3934.4, WeightPct: 3.948630073057284},
			},
			CashWeightPct:           91.95655302579436,
			ConcentrationHHI:        0.5001651589389771,
			LargestPositionPct:      4.094816901148358,
			AnnualizedVolatilityPct: 2.554979098924841,
			MaxDrawdownPct:          1.701751701751716,
		},
		Benchmarking: service.BenchmarkingSection{
			State:              service.StateOK,
			PortfolioReturnPct: -0.3602803602803739,
			PortfolioSharpe:    -0.8999691686797252,
			Benchmarks: []service.Benchmark{
				{Symbol: "SPY", Label: "S&P 500", ReturnPct: -2.3264859448341446, ExcessReturnPct: 1.9662055845537707, Sharpe: -1.012150829654494},
			},
		},
		Behavior: service.BehaviorSection{State: service.StateOK, TradeCount: 3, RiskProfile: "aggressive"},
	}
}
