package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kpeguero/quantsim/services/market-data/internal/service"
)

type PostgresHistoricalPriceStore struct {
	pool *pgxpool.Pool
}

func NewPostgresHistoricalPriceStore(pool *pgxpool.Pool) *PostgresHistoricalPriceStore {
	return &PostgresHistoricalPriceStore{pool: pool}
}

// UpsertBars inserts bars, updating OHLCV in place on conflict so re-running
// ingestion for an overlapping range never duplicates rows --
// historical_prices has a UNIQUE(symbol, timeframe, timestamp) constraint
// (infra/migrations/003_historical_prices.up.sql) that this relies on.
func (s *PostgresHistoricalPriceStore) UpsertBars(ctx context.Context, bars []service.Bar) error {
	if len(bars) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, b := range bars {
		batch.Queue(`
		INSERT INTO historical_prices (symbol, timeframe, timestamp, open, high, low, close, volume)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (symbol, timeframe, timestamp)
		DO UPDATE SET open = EXCLUDED.open, high = EXCLUDED.high, low = EXCLUDED.low,
		              close = EXCLUDED.close, volume = EXCLUDED.volume
		`, b.Symbol, b.Timeframe, b.Timestamp, b.Open, b.High, b.Low, b.Close, b.Volume)
	}

	results := s.pool.SendBatch(ctx, batch)
	defer results.Close()

	for range bars {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}
