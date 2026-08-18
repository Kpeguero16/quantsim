package service

import (
	"context"

	"github.com/google/uuid"
)

// HistoryClient fetches a symbol's stored daily bars from market-data.
// Implemented by *client.MarketDataClient in production; a fake in service
// tests, an httptest.Server in integration tests.
type HistoryClient interface {
	// History returns ErrSymbolUnavailable when market-data answers with no
	// bars at all for the symbol -- market-data's own History never 404s
	// (an unfetched symbol is a valid empty result there, SPEC.md its own
	// §2.6), so this client maps "200 with an empty Bars slice" to that
	// error itself rather than relying on a status code that never comes.
	// Everything else market-data-side (unreachable, timeout, non-2xx, a
	// body that does not parse) becomes ErrUpstreamUnavailable.
	History(ctx context.Context, symbol string) ([]Bar, error)
}

// BacktestStore persists a run and its trade log, and reads them back
// scoped to the owning user. Implemented by *store.PostgresBacktestStore in
// production; mocked in tests.
type BacktestStore interface {
	// SaveBacktest persists the run and its trade log together. Unlike
	// trading-engine's ExecuteOrder, this needs no row lock -- a backtest run
	// has no concurrent writer to race against; it is written once, by the
	// request that computed it, and never mutated again.
	SaveBacktest(ctx context.Context, b Backtest, trades []TradeRecord) (Backtest, error)

	// ListBacktests returns the user's own runs, newest first.
	ListBacktests(ctx context.Context, userID uuid.UUID) ([]Backtest, error)

	// GetBacktest returns ErrNotFound both when the id does not exist and
	// when it belongs to a different user -- the store enforces ownership in
	// its WHERE clause rather than leaving the handler to compare IDs after
	// the fact, so there is exactly one code path that can get this wrong
	// instead of two.
	GetBacktest(ctx context.Context, userID, id uuid.UUID) (BacktestDetail, error)
}
