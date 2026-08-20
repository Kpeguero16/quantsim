// Package mock provides in-memory doubles for the AI insights service's two
// upstream dependencies, so its suite needs neither a running trading-engine
// nor a running market-data.
//
// Both are symbol-keyed and mutex-guarded from the outset rather than grown
// into it (plan D3). That is not speculative generality: FetchHistories calls
// History from N goroutines at once, so an unguarded call log is a data race
// on the first concurrent test -- and the intersection rule cannot be tested
// at all without per-symbol answers, since every interesting case is "this
// symbol has bars, that one does not". There is no single-symbol phase here to
// grow out of.
package mock

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// Compile-time proof that these still satisfy the same interfaces the real
// clients do. Without them the doubles could drift and keep every test green
// while testing a shape production no longer has.
var (
	_ service.TradingClient = (*TradingClient)(nil)
	_ service.HistoryClient = (*HistoryClient)(nil)
)

// HistoryClient answers per symbol from a map.
//
// A symbol absent from Bars gets ErrSymbolUnavailable, which is what
// market-data's empty history means -- so the common "we have no data for
// this" case needs no setup beyond leaving it out.
type HistoryClient struct {
	Bars map[string][]service.Bar

	// Errs overrides Bars for specific symbols, so a test can say "AAPL is
	// fine, MSFT is an outage" -- distinct from "MSFT has no data".
	Errs map[string]error

	mu    sync.Mutex
	calls []string
}

func (c *HistoryClient) History(_ context.Context, symbol string) ([]service.Bar, error) {
	c.mu.Lock()
	c.calls = append(c.calls, symbol)
	c.mu.Unlock()

	if err, ok := c.Errs[symbol]; ok {
		return nil, err
	}
	bars, ok := c.Bars[symbol]
	if !ok {
		// The same TYPED error the real client returns, not a bare sentinel:
		// the handler names the symbol out of it (SPEC.md §2.10), so a mock
		// that dropped it would leave that untested against a double that
		// cannot reproduce production's shape.
		return nil, service.SymbolUnavailable(symbol)
	}
	return bars, nil
}

// Calls returns the symbols History was asked for, in call order.
//
// An accessor rather than an exported slice because an exported slice cannot
// be READ safely mid-run either, not just written -- the mistake Step 19's T5
// had to correct after the fact.
func (c *HistoryClient) Calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.calls))
	copy(out, c.calls)
	return out
}

// TradingClient answers with whatever it was configured with, and records the
// Authorization value it was handed -- which is the point of several tests:
// forwarding the caller's token is what makes the onward call work at all
// (SPEC.md §6.5), and a client that quietly dropped it would fail only against
// a real trading-engine.
type TradingClient struct {
	TradesResult    []service.Trade
	TradesErr       error
	PortfolioResult service.LivePortfolio
	PortfolioErr    error

	mu             sync.Mutex
	authorizations []string
}

func (c *TradingClient) Trades(_ context.Context, authorization string) ([]service.Trade, error) {
	c.record(authorization)
	if c.TradesErr != nil {
		return nil, c.TradesErr
	}
	return c.TradesResult, nil
}

func (c *TradingClient) Portfolio(_ context.Context, authorization string) (service.LivePortfolio, error) {
	c.record(authorization)
	if c.PortfolioErr != nil {
		return service.LivePortfolio{}, c.PortfolioErr
	}
	return c.PortfolioResult, nil
}

func (c *TradingClient) record(authorization string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authorizations = append(c.authorizations, authorization)
}

// Authorizations returns the Authorization header value passed to each call.
func (c *TradingClient) Authorizations() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.authorizations))
	copy(out, c.authorizations)
	return out
}

// InsightsCache is an in-memory stand-in for the Redis report cache, with
// injectable failures on both sides.
//
// Both directions need to be injectable separately because they fail
// differently and are handled differently: a read failure must still produce a
// report, and a write failure must still return the one already computed
// (SPEC.md §2.8). A single "broken" flag could not tell the two apart.
type InsightsCache struct {
	GetErr error
	SetErr error

	mu     sync.Mutex
	stored map[string]service.PortfolioInsights
	gets   int
	sets   int
}

var _ service.InsightsCache = (*InsightsCache)(nil)

func (c *InsightsCache) Get(_ context.Context, userID string) (service.PortfolioInsights, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets++

	if c.GetErr != nil {
		return service.PortfolioInsights{}, false, c.GetErr
	}
	insights, ok := c.stored[userID]
	return insights, ok, nil
}

func (c *InsightsCache) Set(_ context.Context, userID string, insights service.PortfolioInsights, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sets++

	if c.SetErr != nil {
		return c.SetErr
	}
	if c.stored == nil {
		c.stored = map[string]service.PortfolioInsights{}
	}
	c.stored[userID] = insights
	return nil
}

// Gets and Sets report how many times each side was called. Accessors rather
// than exported counters, for the same reason Calls is one: they are read
// while the service may still be running.
func (c *InsightsCache) Gets() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets
}

func (c *InsightsCache) Sets() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sets
}

// Keys returns what is stored, sorted -- so a test can assert the key SHAPE,
// not merely that something was written.
func (c *InsightsCache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.stored))
	for k := range c.stored {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
