package service

import (
	"context"
	"log"
	"sort"
	"time"
)

// dateFormat is the wire format for the two date-only fields in the response.
// Dates, not timestamps: a calendar date is what they mean, and rendering
// them as RFC3339 instants would invite a caller to read a timezone into a
// value that has none (SPEC.md §2.4).
const dateFormat = "2006-01-02"

// BenchmarkSymbols are the two buy-and-hold comparisons (SPEC.md §2.6), and
// are also part of the reconstruction calendar (§2.1).
//
// §2.1 names only SPY as a calendar member, but §2.6 requires every benchmark
// figure to be measured over "the same trading days as the portfolio curve,
// which §2.1's calendar guarantees by construction". Those two statements are
// only both true if BOTH benchmarks are in the intersection -- otherwise a QQQ
// gap would leave its return measured over a window the portfolio's was not,
// and the guarantee would have to be re-established by a separate check that
// §2.6 says is unnecessary. Including both is the reading that makes the
// construction argument hold.
//
// The cost is real and worth naming: a gap in a benchmark's bars shortens the
// portfolio's own measured window. That is already true of SPY under either
// reading, so this adds a second instance of an accepted trade-off rather than
// a new class of one -- and §2.12's guard turns any resulting divergence into
// a refusal instead of a wrong number.
var BenchmarkSymbols = []string{"SPY", "QQQ"}

// Window is the span the report covers (SPEC.md §2.4).
//
// TradingDays counts dates in the reconstruction calendar, not weekdays
// elapsed: they differ across holidays, and the first is the number every
// other figure in the response was actually computed from.
type Window struct {
	StartDate   string `json:"start_date,omitempty"`
	TradingDays int    `json:"trading_days"`
}

// PortfolioInsights is the whole response object (SPEC.md §2.4).
//
// ComputedAt and AsOfDate are distinct on purpose and both load-bearing:
// ComputedAt is cache age (§2.8), AsOfDate is data age. A response can be
// freshly computed from bars that are a day old, and one field cannot say
// both.
//
// All three sections of §2.4, in the order that section lists them.
type PortfolioInsights struct {
	ComputedAt   time.Time           `json:"computed_at"`
	AsOfDate     string              `json:"as_of_date,omitempty"`
	Window       Window              `json:"window"`
	Risk         RiskSection         `json:"risk"`
	Benchmarking BenchmarkingSection `json:"benchmarking"`
	Behavior     BehaviorSection     `json:"behavior"`
}

// CacheTTL is how long a rendered report stays reusable (SPEC.md §2.8).
//
// Deliberately NOT invalidated when a trade is placed. Invalidation would
// require trading-engine to know this service exists and to call it on every
// fill -- a dependency pointing from the core trading path INTO an optional
// analytics service, which puts an analytics outage on the critical path of
// placing an order. The cost is bounded and disclosed instead: a trade may not
// be reflected for up to five minutes, and computed_at says exactly how stale
// the object is.
const CacheTTL = 5 * time.Minute

// Service assembles one portfolio report from its two upstreams. It holds no
// database handle, because this service owns no tables (SPEC.md §2.9).
type Service struct {
	trading TradingClient
	history HistoryClient
	cache   InsightsCache
}

// NewService wires the service. A nil cache means no caching -- every request
// computes -- which is a supported configuration rather than a broken one:
// this service's only stateful dependency is one it can run without (§2.9).
func NewService(trading TradingClient, history HistoryClient, cache InsightsCache) *Service {
	if cache == nil {
		cache = noopCache{}
	}
	return &Service{trading: trading, history: history, cache: cache}
}

// noopCache always misses and always accepts, so the request path has no
// branch for "there is no cache" -- the fail-open handling it already needs
// covers that case for free.
type noopCache struct{}

func (noopCache) Get(context.Context, string) (PortfolioInsights, bool, error) {
	return PortfolioInsights{}, false, nil
}

func (noopCache) Set(context.Context, string, PortfolioInsights, time.Duration) error {
	return nil
}

// PortfolioInsights builds the caller's report.
//
// authorization is the caller's own Authorization header, forwarded to
// trading-engine because /trading/* authenticates at trading-engine rather
// than trusting the gateway (SPEC.md §6.5). It is not passed to market-data,
// which has no auth on it, and must not be: sending a bearer token to a
// service that never asked for one widens where that token has been for no
// benefit.
func (s *Service) PortfolioInsights(ctx context.Context, userID, authorization string) (PortfolioInsights, error) {
	// Fail-open on read: a cache error means compute, not fail. Logged rather
	// than swallowed, because a cache that is erroring on every request is
	// invisible from the outside -- every response still succeeds, just
	// slowly, and the only symptom is upstream load nobody ordered.
	cached, found, err := s.cache.Get(ctx, userID)
	if err != nil {
		log.Printf("ai-insights: cache read failed, computing: %v", err)
	} else if found {
		return cached, nil
	}

	out, err := s.buildInsights(ctx, authorization)
	if err != nil {
		// Errors are never cached. A 502 from a momentary upstream blip would
		// otherwise be served for five minutes after the upstream recovered.
		return PortfolioInsights{}, err
	}

	// Fail-open on write, for the same reason and with the same logging.
	if err := s.cache.Set(ctx, userID, out, CacheTTL); err != nil {
		log.Printf("ai-insights: cache write failed, ignoring: %v", err)
	}
	return out, nil
}

// buildInsights is the uncached path: everything PortfolioInsights does when
// the cache does not answer.
//
// Split out so there is exactly ONE place that writes to the cache, and so
// every successful return -- including the insufficient_data ones -- goes
// through it. Caching those matters as much as caching a full report: a
// brand-new user polling a dashboard is precisely the case where the answer is
// cheap to reuse and never changes between polls.
func (s *Service) buildInsights(ctx context.Context, authorization string) (PortfolioInsights, error) {
	trades, err := s.trading.Trades(ctx, authorization)
	if err != nil {
		return PortfolioInsights{}, err
	}

	// A never-traded account returns before any bar is fetched. Not just an
	// optimization: with no trades there is no window to measure and no
	// holdings to weigh, so the benchmark fetch could only fail the request --
	// a new user would get a 404 naming SPY, for a portfolio they do not have.
	if len(trades) == 0 {
		return insufficientData("no trades yet"), nil
	}

	live, err := s.trading.Portfolio(ctx, authorization)
	if err != nil {
		return PortfolioInsights{}, err
	}

	barsBySymbol, err := FetchHistories(ctx, s.history, reconstructionSymbols(trades))
	if err != nil {
		return PortfolioInsights{}, err
	}

	reconstruction, err := Reconstruct(trades, barsBySymbol, Calendar(barsBySymbol))
	if err != nil {
		return PortfolioInsights{}, err
	}

	// SPEC.md §2.12: checked before any section is computed, and a failure
	// degrades every section rather than failing the request. The caller's
	// account is fine; it is this service's derivation of it that cannot be
	// trusted, and that is a 200 saying so, not a 500.
	//
	// Logged because nothing else will surface it: the response is a
	// successful one, so a divergence would otherwise be invisible to
	// everyone except the user reading a section that has gone quiet.
	if err := Reconcile(reconstruction, trades, live); err != nil {
		log.Printf("ai-insights: %v", err)
		return insufficientData("portfolio history could not be verified against the account"), nil
	}

	risk, err := ComputeRisk(reconstruction, barsBySymbol)
	if err != nil {
		return PortfolioInsights{}, err
	}

	benchmarking, err := ComputeBenchmarking(reconstruction, barsBySymbol)
	if err != nil {
		return PortfolioInsights{}, err
	}

	// Last: it reads the risk section's own figures for the profile bands.
	behavior, err := ComputeBehavior(trades, reconstruction, barsBySymbol, risk)
	if err != nil {
		return PortfolioInsights{}, err
	}

	out := PortfolioInsights{
		ComputedAt:   time.Now().UTC(),
		Window:       Window{TradingDays: len(reconstruction.Dates)},
		Risk:         risk,
		Benchmarking: benchmarking,
		Behavior:     behavior,
	}
	if len(reconstruction.Dates) > 0 {
		out.Window.StartDate = reconstruction.Dates[0].Format(dateFormat)
		out.AsOfDate = reconstruction.Dates[len(reconstruction.Dates)-1].Format(dateFormat)
	}
	return out, nil
}

// insufficientData is the whole-report degradation: every section says why it
// is empty, and the window is zero rather than invented.
//
// AsOfDate is deliberately absent. There is no last calendar date, and
// emitting today's would date the report to data it was not computed from.
//
// Every section gets the SAME reason, because at this point the cause is the
// report's, not the section's: there is no history for any of them to measure.
// A section-specific reason belongs where a section alone cannot be computed.
func insufficientData(reason string) PortfolioInsights {
	return PortfolioInsights{
		ComputedAt: time.Now().UTC(),
		Risk: RiskSection{
			State:     StateInsufficientData,
			Reason:    reason,
			Positions: []PositionWeight{},
		},
		Benchmarking: BenchmarkingSection{
			State:      StateInsufficientData,
			Reason:     reason,
			Benchmarks: []Benchmark{},
		},
		Behavior: BehaviorSection{
			State:    StateInsufficientData,
			Reason:   reason,
			Findings: []Finding{},
		},
	}
}

// reconstructionSymbols is every symbol the calendar must cover: each one the
// account has EVER traded, plus both benchmarks.
//
// Ever-traded rather than currently-held, because a symbol bought and fully
// sold in March is part of March's equity and the curve spans March
// (SPEC.md §2.1). Sold-out symbols are dropped from Holdings by the
// reconstruction itself, so including them here costs one bar fetch and
// prevents a hole in the middle of the curve.
func reconstructionSymbols(trades []Trade) []string {
	unique := make(map[string]struct{}, len(trades)+len(BenchmarkSymbols))
	for _, t := range trades {
		unique[t.Symbol] = struct{}{}
	}
	for _, symbol := range BenchmarkSymbols {
		unique[symbol] = struct{}{}
	}

	symbols := make([]string, 0, len(unique))
	for symbol := range unique {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	return symbols
}
