package service

import (
	"context"
	"time"
)

// TradingClient reads the caller's own trading data from trading-engine over
// HTTP. Implemented by *client.TradingClient in production; mocked in tests.
//
// Every method takes the caller's Authorization header value and forwards it.
// /trading/* is authenticated at trading-engine itself rather than trusted
// from the gateway, so an unauthenticated internal call is a 401 no matter
// which process makes it (SPEC.md §6.5). Forwarding the caller's own token
// also means account scoping stays enforced by trading-engine's AccountForUser
// -- this service never learns an account id, and so cannot express a request
// for someone else's history even by mistake.
type TradingClient interface {
	// Trades returns the caller's executions oldest first.
	Trades(ctx context.Context, authorization string) ([]Trade, error)

	// Portfolio returns the caller's live balance and open positions, for the
	// reconciliation guard (SPEC.md §2.12).
	Portfolio(ctx context.Context, authorization string) (LivePortfolio, error)
}

// HistoryClient reads a symbol's stored daily bars from market-data over HTTP.
//
// No credential: /market-data/* has no RequireAuth on it, unlike /trading/*.
// That asymmetry is market-data's to change, not this service's to work
// around.
type HistoryClient interface {
	// History returns ErrSymbolUnavailable when market-data has no bars for
	// the symbol, and ErrUpstreamUnavailable for everything else.
	History(ctx context.Context, symbol string) ([]Bar, error)
}

// InsightsCache stores rendered reports so a dashboard poll does not redo the
// whole reconstruction (SPEC.md §2.8). Implemented by *cache.RedisInsightsCache.
//
// Every method may fail, and no failure is a request failure. The cache is an
// optimization, and an unreachable optimization must never turn a working
// endpoint into a 5xx -- so the service logs and carries on in both
// directions. That is the contract, not an implementation detail: an
// implementation that returned an error expecting the caller to abort would be
// wrong about what this interface means.
type InsightsCache interface {
	// Get reports whether an entry was found. A miss is (zero, false, nil);
	// an error is (zero, false, err). Both mean "compute" to the caller, but
	// only the second is worth logging.
	Get(ctx context.Context, userID string) (PortfolioInsights, bool, error)

	Set(ctx context.Context, userID string, insights PortfolioInsights, ttl time.Duration) error
}
