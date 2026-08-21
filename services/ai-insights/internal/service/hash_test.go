package service_test

import (
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

func hashFixture() service.PortfolioInsights {
	return service.PortfolioInsights{
		ComputedAt: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
		AsOfDate:   "2026-08-14",
		Window:     service.Window{StartDate: "2026-06-01", TradingDays: 52},
		Risk: service.RiskSection{
			State:            service.StateOK,
			Positions:        []service.PositionWeight{{Symbol: "AAPL", Quantity: 10, MarketValue: 1750, WeightPct: 62.5}},
			ConcentrationHHI: 0.531,
			MaxDrawdownPct:   -12.4,
		},
		Benchmarking: service.BenchmarkingSection{
			State:      service.StateOK,
			Benchmarks: []service.Benchmark{{Symbol: "SPY", ReturnPct: 8.1, Sharpe: 1.02}},
		},
		Behavior: service.BehaviorSection{
			State:      service.StateOK,
			TradeCount: 34,
			Findings:   []service.Finding{{Code: "overtrading", Occurrences: 2}},
		},
	}
}

// The task's real proof. computed_at is cache age, not a measurement: it
// changes on every recomputation. Hashing the report as serialized would mint
// a fresh key every five minutes and produce a cache that never hit once --
// a defect that looks like working code and shows up only on the bill.
func TestReportHash_IgnoresComputedAt(t *testing.T) {
	a := hashFixture()
	b := hashFixture()
	b.ComputedAt = a.ComputedAt.Add(37 * time.Minute)

	if service.ReportHash(a) != service.ReportHash(b) {
		t.Fatal("two reports differing only in computed_at hash differently: " +
			"the cache would never hit, and nothing but the bill would say so")
	}
}

// Everything that IS a measurement participates. Each case below changes one
// figure and must change the hash, or a stale narrative outlives the numbers
// it describes.
func TestReportHash_ChangesWithEveryMeasurement(t *testing.T) {
	base := service.ReportHash(hashFixture())

	for _, tc := range []struct {
		name   string
		mutate func(*service.PortfolioInsights)
	}{
		{"as_of_date", func(r *service.PortfolioInsights) { r.AsOfDate = "2026-08-15" }},
		{"trading days", func(r *service.PortfolioInsights) { r.Window.TradingDays = 53 }},
		{"a section's state", func(r *service.PortfolioInsights) { r.Risk.State = service.StateInsufficientData }},
		{"concentration", func(r *service.PortfolioInsights) { r.Risk.ConcentrationHHI = 0.532 }},
		{"drawdown", func(r *service.PortfolioInsights) { r.Risk.MaxDrawdownPct = -12.5 }},
		{"a position's quantity", func(r *service.PortfolioInsights) { r.Risk.Positions[0].Quantity = 11 }},
		{"a position's symbol", func(r *service.PortfolioInsights) { r.Risk.Positions[0].Symbol = "MSFT" }},
		{"a benchmark's return", func(r *service.PortfolioInsights) { r.Benchmarking.Benchmarks[0].ReturnPct = 8.2 }},
		{"trade count", func(r *service.PortfolioInsights) { r.Behavior.TradeCount = 35 }},
		{"a finding's occurrences", func(r *service.PortfolioInsights) { r.Behavior.Findings[0].Occurrences = 3 }},
		{"a finding's code", func(r *service.PortfolioInsights) { r.Behavior.Findings[0].Code = "panic_selling" }},
		{"a position appearing", func(r *service.PortfolioInsights) {
			r.Risk.Positions = append(r.Risk.Positions, service.PositionWeight{Symbol: "MSFT", Quantity: 1})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := hashFixture()
			tc.mutate(&r)
			if service.ReportHash(r) == base {
				t.Errorf("changing %s did not change the hash -- the narrative would outlive the figure", tc.name)
			}
		})
	}
}

func TestReportHash_IsStableAcrossCalls(t *testing.T) {
	r := hashFixture()
	if service.ReportHash(r) != service.ReportHash(r) {
		t.Fatal("the hash is not deterministic")
	}
}

func TestReportHash_IsAShortHexString(t *testing.T) {
	got := service.ReportHash(hashFixture())
	if len(got) != 16 {
		t.Errorf("hash %q is %d characters, want a 16-character prefix", got, len(got))
	}
	for _, c := range got {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("hash %q is not lowercase hex", got)
		}
	}
}
