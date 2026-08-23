package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service/mock"
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

// Hashing ONE struct value twice, which is a purity check and nothing more.
//
// Worth being precise about, because the name reads like a guarantee this does
// not give. It says the function is deterministic; it says nothing about
// whether computing a report twice from the same account yields the same
// struct to hash, which is the property that was actually broken and is where
// the narrative cache key lives. TestReportHash_IsStableAcrossRecomputes owns
// that one, and it drives the service to get it.
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

// The defect Step 22 reported, as reported: twelve recomputes of one untouched
// account gave six distinct hashes.
//
// This drives the whole service rather than hashing a literal, because the
// instability was never in ReportHash. It was in the report handed to it: two
// float64 accumulations running in Go map iteration order, which randomizes
// per pass (SPEC.md Step 23 §3). A test over a hand-built PortfolioInsights
// would hash the same struct twice and pass against the unfixed code.
//
// A fresh cache per run is what makes each call recompute. Reusing one would
// serve the first run's report back eleven times and prove only that a map
// lookup is deterministic.
//
// Cost, not just correctness: narrative:{user_id}:{report_hash} is the
// narrative cache key, so an unstable hash means a cache that never hits and
// a billed generation on every view of an unchanged account.
//
// Verified to fail against the unfixed code: 11 distinct hashes over 12 runs.
func TestReportHash_IsStableAcrossRecomputes(t *testing.T) {
	trades, barsBySymbol := driftFixture()

	// Cash and positions are derived from the same trades the reconstruction
	// replays, so the account reconciles (SPEC.md §2.12). Building the live
	// side by hand instead is the reliable way to blank every section.
	cash := service.StartingBalance
	live := make([]service.LivePosition, 0, len(trades))
	for _, tr := range trades {
		cash -= tr.Quantity * tr.Price
		live = append(live, service.LivePosition{Symbol: tr.Symbol, Quantity: tr.Quantity})
	}

	seen := make(map[string]int)
	const runs = 12
	for i := 0; i < runs; i++ {
		svc := service.NewService(
			&mock.TradingClient{
				TradesResult:    trades,
				PortfolioResult: service.LivePortfolio{Balance: cash, Positions: live},
			},
			&mock.HistoryClient{Bars: barsBySymbol},
			&mock.InsightsCache{},
		)

		report, err := svc.PortfolioInsights(context.Background(), "user-1", "Bearer t")
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if report.Risk.State != service.StateOK {
			t.Fatalf("run %d: risk state %q, want ok -- a degraded section "+
				"carries no figures and cannot exercise hash stability",
				i, report.Risk.State)
		}
		seen[service.ReportHash(report)]++
	}

	if len(seen) != 1 {
		t.Errorf("%d recomputes of identical data produced %d distinct hashes, want 1: %v",
			runs, len(seen), seen)
	}
}
