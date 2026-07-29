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
}
