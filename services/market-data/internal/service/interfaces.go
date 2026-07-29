package service

import (
	"context"
	"time"

	"github.com/kpeguero/quantsim/services/market-data/internal/alpaca"
)

// AlpacaClient fetches historical bars for a single symbol. Implemented by
// *alpaca.Client in production; mocked in tests.
type AlpacaClient interface {
	GetBars(ctx context.Context, symbol, timeframe string, start, end time.Time) ([]alpaca.Bar, error)
}

// HistoricalPriceStore persists/queries bars. Implemented by
// *store.PostgresHistoricalPriceStore in production; mocked in tests.
type HistoricalPriceStore interface {
	UpsertBars(ctx context.Context, bars []Bar) error
	// GetHistory returns the most recent `limit` bars for symbol/timeframe,
	// in ascending timestamp order.
	GetHistory(ctx context.Context, symbol, timeframe string, limit int) ([]Bar, error)
}

// PriceCache reads/writes the latest-price cache and its pub/sub fan-out.
// Implemented by *cache.RedisPriceCache in production; mocked in tests.
type PriceCache interface {
	SetPrice(ctx context.Context, symbol string, price Price, ttl time.Duration) error
	// GetPrice returns cache.ErrNotFound when the key is missing or expired.
	GetPrice(ctx context.Context, symbol string) (Price, error)
	PublishPrice(ctx context.Context, symbol string, price Price) error
}

// SnapshotClient fetches latest-trade snapshots for a batch of symbols in
// one request. Implemented by *alpaca.Client (same type as AlpacaClient) in
// production; mocked in tests.
type SnapshotClient interface {
	GetSnapshots(ctx context.Context, symbols []string) (map[string]alpaca.Snapshot, error)
}
