package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service/mock"
)

// benchmarkBars covers the same days as everything else in these tests, so
// the benchmarks never accidentally become the thing that shortens a window.
func benchmarkBars(days ...int) map[string][]service.Bar {
	return map[string][]service.Bar{"SPY": bars(days...), "QQQ": bars(days...)}
}

func withBars(base map[string][]service.Bar, extra map[string][]service.Bar) map[string][]service.Bar {
	for symbol, b := range extra {
		base[symbol] = b
	}
	return base
}

// testUser is the cache key these tests run under. A fixed value rather than
// a fresh one per test, so a test that shares a cache between two calls is
// exercising a hit rather than accidentally missing on a different key.
const testUser = "6f1e0e5a-1b2c-4d3e-8f90-abcdef012345"

func newService(trading *mock.TradingClient, history *mock.HistoryClient) *service.Service {
	return service.NewService(trading, history, nil)
}

func insights(t *testing.T, trading *mock.TradingClient, history *mock.HistoryClient) service.PortfolioInsights {
	t.Helper()
	got, err := newService(trading, history).PortfolioInsights(context.Background(), testUser, "Bearer tok")
	if err != nil {
		t.Fatalf("PortfolioInsights: %v", err)
	}
	return got
}

// A brand-new account is a 200 that says so, and -- the part worth pinning --
// it costs zero bar fetches. Fetching the benchmarks here would let a missing
// SPY 404 a user who has no portfolio to report on at all.
func TestPortfolioInsights_ANeverTradedAccountFetchesNoBars(t *testing.T) {
	history := &mock.HistoryClient{}
	trading := &mock.TradingClient{TradesResult: []service.Trade{}}

	got := insights(t, trading, history)

	if got.Risk.State != service.StateInsufficientData {
		t.Errorf("state: got %q, want %q", got.Risk.State, service.StateInsufficientData)
	}
	if got.Risk.Reason != "no trades yet" {
		t.Errorf("reason: got %q", got.Risk.Reason)
	}
	if calls := history.Calls(); len(calls) != 0 {
		t.Errorf("fetched bars for a never-traded account: %v", calls)
	}
	if got.Window.TradingDays != 0 || got.Window.StartDate != "" {
		t.Errorf("window: got %+v, want zero", got.Window)
	}
	if got.AsOfDate != "" {
		t.Errorf("as_of_date: got %q, want empty -- there is no last calendar date", got.AsOfDate)
	}
	if got.Risk.Positions == nil {
		t.Error("positions is nil; it must marshal as [] rather than null")
	}
	if got.ComputedAt.IsZero() {
		t.Error("computed_at is zero")
	}
}

// The calendar must cover every symbol the account has EVER traded, plus both
// benchmarks -- not just what it currently holds. TSLA below was bought and
// fully sold, and the curve still spans the days it was held.
func TestPortfolioInsights_FetchesEveryTradedSymbolPlusBothBenchmarks(t *testing.T) {
	history := &mock.HistoryClient{Bars: withBars(benchmarkBars(1, 2, 3), map[string][]service.Bar{
		"AAPL": bars(1, 2, 3),
		"TSLA": bars(1, 2, 3),
	})}
	trading := &mock.TradingClient{
		TradesResult: []service.Trade{
			buy("AAPL", 10, 100, 1),
			buy("TSLA", 5, 200, 1),
			sell("TSLA", 5, 210, 2),
		},
		PortfolioResult: live(100050, pos("AAPL", 10)),
	}

	insights(t, trading, history)

	got := strings.Join(history.Calls(), ",")
	for _, want := range []string{"AAPL", "TSLA", "SPY", "QQQ"} {
		if !strings.Contains(got, want) {
			t.Errorf("did not fetch %s; fetched %s", want, got)
		}
	}
	if len(history.Calls()) != 4 {
		t.Errorf("fetched %d symbols (%s), want exactly 4", len(history.Calls()), got)
	}
}

// Both trading-engine calls carry the caller's own token. A client that
// dropped it would pass every test that used a mock ignoring the argument,
// and fail only against a real trading-engine (SPEC.md §6.5).
func TestPortfolioInsights_ForwardsTheCallersTokenToTradingEngine(t *testing.T) {
	history := &mock.HistoryClient{Bars: withBars(benchmarkBars(1, 2), map[string][]service.Bar{
		"AAPL": bars(1, 2),
	})}
	trading := &mock.TradingClient{
		TradesResult:    []service.Trade{buy("AAPL", 10, 100, 1)},
		PortfolioResult: live(99000, pos("AAPL", 10)),
	}

	if _, err := newService(trading, history).
		PortfolioInsights(context.Background(), testUser, "Bearer the-callers-token"); err != nil {
		t.Fatalf("PortfolioInsights: %v", err)
	}

	auths := trading.Authorizations()
	if len(auths) != 2 {
		t.Fatalf("made %d trading-engine calls, want 2 (trades and portfolio)", len(auths))
	}
	for i, got := range auths {
		if got != "Bearer the-callers-token" {
			t.Errorf("call %d forwarded %q", i, got)
		}
	}
}

// computed_at is cache age and as_of_date is data age; §2.4 requires both,
// and they are different facts. Here the last bar is in March 2026 and the
// clock is now, so a report that conflated them would be visibly wrong.
func TestPortfolioInsights_ReportsTheWindowAndBothDates(t *testing.T) {
	history := &mock.HistoryClient{Bars: withBars(benchmarkBars(2, 3, 4), map[string][]service.Bar{
		"AAPL": bars(2, 3, 4),
	})}
	trading := &mock.TradingClient{
		TradesResult:    []service.Trade{buy("AAPL", 10, 100, 2)},
		PortfolioResult: live(99000, pos("AAPL", 10)),
	}

	got := insights(t, trading, history)

	if got.Window.StartDate != "2026-03-02" {
		t.Errorf("start_date: got %q, want 2026-03-02", got.Window.StartDate)
	}
	if got.Window.TradingDays != 3 {
		t.Errorf("trading_days: got %d, want 3", got.Window.TradingDays)
	}
	if got.AsOfDate != "2026-03-04" {
		t.Errorf("as_of_date: got %q, want 2026-03-04 (the last calendar date)", got.AsOfDate)
	}
	if time.Since(got.ComputedAt) > time.Minute {
		t.Errorf("computed_at is %v old; it should be the moment of the request", time.Since(got.ComputedAt))
	}
	if got.AsOfDate == got.ComputedAt.Format("2006-01-02") {
		t.Error("as_of_date happens to equal today; the fixture must use a past date to keep the two distinguishable")
	}
	if got.Risk.State != service.StateOK {
		t.Errorf("risk state: got %q, want ok", got.Risk.State)
	}
}

// SPEC.md §2.12: a reconstruction that disagrees with the live account
// degrades the report rather than failing the request or -- much worse --
// reporting weights derived from holdings the account does not have.
func TestPortfolioInsights_AReconciliationFailureDegradesTheReport(t *testing.T) {
	history := &mock.HistoryClient{Bars: withBars(benchmarkBars(1, 2), map[string][]service.Bar{
		"AAPL": bars(1, 2),
	})}
	trading := &mock.TradingClient{
		TradesResult: []service.Trade{buy("AAPL", 10, 100, 1)},
		// The account holds 100, the trade log accounts for 10.
		PortfolioResult: live(99000, pos("AAPL", 100)),
	}

	got := insights(t, trading, history)

	if got.Risk.State != service.StateInsufficientData {
		t.Fatalf("state: got %q, want insufficient_data", got.Risk.State)
	}
	if len(got.Risk.Positions) != 0 {
		t.Errorf("reported %d positions from an unverified reconstruction", len(got.Risk.Positions))
	}
	if got.Window.TradingDays != 0 || got.AsOfDate != "" {
		t.Errorf("reported a window (%+v, as_of=%q) for a report it could not verify", got.Window, got.AsOfDate)
	}
}

// A held symbol with no stored bars fails the whole request; the handler maps
// it to 404. A curve quietly missing one of the account's positions is a
// different answer to a different question wearing the right shape.
func TestPortfolioInsights_AMissingSymbolFailsTheRequest(t *testing.T) {
	history := &mock.HistoryClient{Bars: benchmarkBars(1, 2)} // no AAPL
	trading := &mock.TradingClient{
		TradesResult:    []service.Trade{buy("AAPL", 10, 100, 1)},
		PortfolioResult: live(99000, pos("AAPL", 10)),
	}

	_, err := newService(trading, history).PortfolioInsights(context.Background(), testUser, "Bearer tok")
	if !errors.Is(err, service.ErrSymbolUnavailable) {
		t.Fatalf("got %v, want ErrSymbolUnavailable", err)
	}
}

// A missing benchmark is equally fatal (§2.6: both are non-optional), but
// only once the account has actually traded -- the never-traded case above
// never reaches the fetch.
func TestPortfolioInsights_AMissingBenchmarkFailsTheRequest(t *testing.T) {
	for _, missing := range []string{"SPY", "QQQ"} {
		t.Run(missing, func(t *testing.T) {
			bars := withBars(benchmarkBars(1, 2), map[string][]service.Bar{"AAPL": bars(1, 2)})
			delete(bars, missing)

			trading := &mock.TradingClient{
				TradesResult:    []service.Trade{buy("AAPL", 10, 100, 1)},
				PortfolioResult: live(99000, pos("AAPL", 10)),
			}

			_, err := newService(trading, &mock.HistoryClient{Bars: bars}).
				PortfolioInsights(context.Background(), testUser, "Bearer tok")
			if !errors.Is(err, service.ErrSymbolUnavailable) {
				t.Fatalf("%s missing: got %v, want ErrSymbolUnavailable", missing, err)
			}
		})
	}
}

// Upstream failures reach the handler unwrapped, so it can tell a refused
// credential from an outage.
func TestPortfolioInsights_UpstreamFailuresPropagate(t *testing.T) {
	tests := []struct {
		name    string
		trading *mock.TradingClient
		want    error
	}{
		{
			name:    "trades unauthorized",
			trading: &mock.TradingClient{TradesErr: service.ErrUnauthorized},
			want:    service.ErrUnauthorized,
		},
		{
			name:    "trades unavailable",
			trading: &mock.TradingClient{TradesErr: service.ErrUpstreamUnavailable},
			want:    service.ErrUpstreamUnavailable,
		},
		{
			name: "portfolio unavailable",
			trading: &mock.TradingClient{
				TradesResult: []service.Trade{buy("AAPL", 10, 100, 1)},
				PortfolioErr: service.ErrUpstreamUnavailable,
			},
			want: service.ErrUpstreamUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &mock.HistoryClient{Bars: withBars(benchmarkBars(1, 2), map[string][]service.Bar{
				"AAPL": bars(1, 2),
			})}
			_, err := newService(tt.trading, history).
				PortfolioInsights(context.Background(), testUser, "Bearer tok")
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

// The live portfolio is not fetched before it is known to be needed: a
// never-traded account makes exactly one trading-engine call, not two.
func TestPortfolioInsights_ANeverTradedAccountMakesOneUpstreamCall(t *testing.T) {
	trading := &mock.TradingClient{TradesResult: []service.Trade{}}
	insights(t, trading, &mock.HistoryClient{})

	if n := len(trading.Authorizations()); n != 1 {
		t.Errorf("made %d trading-engine calls, want 1 (trades only)", n)
	}
}

// The Checkpoint C scenario, end to end through the service: an account with
// real history that traded after the last stored close still gets a report.
//
// This is the whole point of §2.12's amendment. Bars end on day 2; the account
// bought again on day 4. Before the amendment the entire response became
// insufficient_data, and a user with two months of history saw a blank page
// because they placed one small order that morning.
func TestPortfolioInsights_ATradeSinceTheLastCloseStillReports(t *testing.T) {
	history := &mock.HistoryClient{Bars: withBars(benchmarkBars(1, 2), map[string][]service.Bar{
		"AAPL": bars(1, 2),
	})}
	trades := []service.Trade{
		buy("AAPL", 50, 100, 1),
		buy("AAPL", 1, 316.67, 4), // today; market-data has no bar for it yet
	}
	// The account as trading-engine holds it right now: both trades applied.
	trading := &mock.TradingClient{
		TradesResult:    trades,
		PortfolioResult: live(service.StartingBalance-5000-316.67, pos("AAPL", 51)),
	}

	got := insights(t, trading, history)

	if got.Risk.State != service.StateOK {
		t.Fatalf("state: got %q (%q), want ok -- one trade since the last close blanked the report",
			got.Risk.State, got.Risk.Reason)
	}
	// And the report is honest about what it describes: 50 shares as of day 2,
	// not the 51 the account holds now.
	if got.AsOfDate != "2026-03-02" {
		t.Errorf("as_of_date: got %q, want 2026-03-02", got.AsOfDate)
	}
	if len(got.Risk.Positions) != 1 || got.Risk.Positions[0].Quantity != 50 {
		t.Errorf("positions: got %+v, want 50 AAPL as of the last close", got.Risk.Positions)
	}
}

// The benchmarking section rides the same reconstruction as risk, so it is
// measured over the same days by construction rather than by a check.
func TestPortfolioInsights_IncludesBenchmarkingOverTheSameWindow(t *testing.T) {
	history := &mock.HistoryClient{Bars: map[string][]service.Bar{
		"AAPL": bars(2, 3, 4),
		"SPY":  bars(2, 3, 4),
		"QQQ":  bars(2, 3, 4),
	}}
	trading := &mock.TradingClient{
		TradesResult:    []service.Trade{buy("AAPL", 10, 100, 2)},
		PortfolioResult: live(99000, pos("AAPL", 10)),
	}

	got := insights(t, trading, history)

	if got.Benchmarking.State != service.StateOK {
		t.Fatalf("benchmarking state: got %q (%q)", got.Benchmarking.State, got.Benchmarking.Reason)
	}
	if len(got.Benchmarking.Benchmarks) != 2 {
		t.Fatalf("got %d benchmarks, want SPY and QQQ", len(got.Benchmarking.Benchmarks))
	}
	// Same window as risk: both sections describe the same three days.
	if got.Window.TradingDays != 3 {
		t.Errorf("window: got %d days", got.Window.TradingDays)
	}
}

// Both sections degrade together when there is no history at all -- the cause
// belongs to the report, not to either section.
func TestPortfolioInsights_BothSectionsDegradeTogether(t *testing.T) {
	got := insights(t, &mock.TradingClient{TradesResult: []service.Trade{}}, &mock.HistoryClient{})

	if got.Risk.State != service.StateInsufficientData ||
		got.Benchmarking.State != service.StateInsufficientData {
		t.Fatalf("risk=%q benchmarking=%q, want both insufficient_data",
			got.Risk.State, got.Benchmarking.State)
	}
	if got.Behavior.State != service.StateInsufficientData {
		t.Fatalf("behavior=%q, want insufficient_data", got.Behavior.State)
	}
	if got.Risk.Reason != got.Benchmarking.Reason || got.Risk.Reason != got.Behavior.Reason {
		t.Errorf("reasons differ: %q / %q / %q",
			got.Risk.Reason, got.Benchmarking.Reason, got.Behavior.Reason)
	}
	if got.Benchmarking.Benchmarks == nil {
		t.Error("benchmarks is nil; it must marshal as [] rather than null")
	}
	if got.Behavior.Findings == nil {
		t.Error("findings is nil; it must marshal as [] rather than null")
	}
}

// All three sections populate from one reconstruction, and behavior's profile
// band is derived from the risk section this same call produced -- not from a
// second, independently computed one that could disagree with what the
// response shows.
func TestPortfolioInsights_AllThreeSectionsAgree(t *testing.T) {
	history := &mock.HistoryClient{Bars: map[string][]service.Bar{
		"AAPL": bars(2, 3, 4),
		"SPY":  bars(2, 3, 4),
		"QQQ":  bars(2, 3, 4),
	}}
	trades := []service.Trade{{
		ID: "t1", Symbol: "AAPL", Side: service.SideBuy, Quantity: 10, Price: 100,
		ExecutedAt: day(2),
	}}
	trading := &mock.TradingClient{
		TradesResult:    trades,
		PortfolioResult: live(99000, pos("AAPL", 10)),
	}

	got := insights(t, trading, history)

	for name, state := range map[string]string{
		"risk":         got.Risk.State,
		"benchmarking": got.Benchmarking.State,
		"behavior":     got.Behavior.State,
	} {
		if state != service.StateOK {
			t.Errorf("%s state: got %q, want ok", name, state)
		}
	}
	if got.Behavior.TradeCount != 1 {
		t.Errorf("trade_count: got %d, want 1", got.Behavior.TradeCount)
	}
	// The band must be the one these exact risk figures imply.
	if want := riskProfileOf(got.Risk); got.Behavior.RiskProfile != want {
		t.Errorf("risk_profile: got %q, want %q for vol=%.2f hhi=%.2f",
			got.Behavior.RiskProfile, want,
			got.Risk.AnnualizedVolatilityPct, got.Risk.ConcentrationHHI)
	}
}

// riskProfileOf restates §2.7's bands independently, so the test asserts the
// rule rather than echoing whatever the implementation returned.
func riskProfileOf(risk service.RiskSection) string {
	switch {
	case risk.AnnualizedVolatilityPct > service.AggressiveVolatilityPct ||
		risk.ConcentrationHHI > service.AggressiveHHI:
		return service.ProfileAggressive
	case risk.AnnualizedVolatilityPct < service.ConservativeVolatilityPct &&
		risk.ConcentrationHHI < service.ConservativeHHI:
		return service.ProfileConservative
	default:
		return service.ProfileModerate
	}
}

// SPEC §2.10's mixed response, which is the whole point of per-section states:
// an account that traded but has only ONE trading day of calendar gets a
// populated behavior section beside two insufficient_data ones. That is a
// truthful description of what is known, and it is only expressible because
// the sections carry their own states rather than the report carrying one.
func TestPortfolioInsights_APopulatedBehaviorBesideTwoInsufficientSections(t *testing.T) {
	oneDay := bars(2)
	history := &mock.HistoryClient{Bars: map[string][]service.Bar{
		"AAPL": oneDay, "SPY": oneDay, "QQQ": oneDay,
	}}
	trading := &mock.TradingClient{
		TradesResult:    []service.Trade{buy("AAPL", 10, 100, 2)},
		PortfolioResult: live(99000, pos("AAPL", 10)),
	}

	got := insights(t, trading, history)

	if got.Behavior.State != service.StateOK {
		t.Errorf("behavior: got %q, want ok -- it needs one TRADE, not two trading days",
			got.Behavior.State)
	}
	if got.Behavior.TradeCount != 1 {
		t.Errorf("trade_count: got %d, want 1", got.Behavior.TradeCount)
	}
	if got.Risk.State != service.StateInsufficientData {
		t.Errorf("risk: got %q, want insufficient_data on a one-day calendar", got.Risk.State)
	}
	if got.Benchmarking.State != service.StateInsufficientData {
		t.Errorf("benchmarking: got %q, want insufficient_data", got.Benchmarking.State)
	}
	// And no band, because its inputs live in the section that could not be
	// computed.
	if got.Behavior.RiskProfile != "" {
		t.Errorf("risk_profile: got %q, want empty", got.Behavior.RiskProfile)
	}
}

// The specific failure §2.10 exists to prevent: a never-traded account must
// not receive zeros that read as measurements.
func TestPortfolioInsights_ANeverTradedAccountGetsNoZeroMeasurements(t *testing.T) {
	got := insights(t, &mock.TradingClient{TradesResult: []service.Trade{}}, &mock.HistoryClient{})

	for name, state := range map[string]string{
		"risk": got.Risk.State, "benchmarking": got.Benchmarking.State, "behavior": got.Behavior.State,
	} {
		if state != service.StateInsufficientData {
			t.Errorf("%s: got %q, want insufficient_data", name, state)
		}
	}
	// Every figure is zero -- which is exactly why the states above have to be
	// there, and why a caller must read them before any number below.
	if got.Risk.AnnualizedVolatilityPct != 0 || got.Benchmarking.PortfolioReturnPct != 0 {
		t.Error("fixture assumption broken: the zeros are the point of the state fields")
	}
}
