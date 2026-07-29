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

// GetHistory returns the most recent `limit` bars for symbol/timeframe in
// ascending timestamp order. Queried DESC+LIMIT (so "most recent N" is
// correct regardless of how much history exists) then reversed in place,
// since the API always returns bars oldest-to-newest (SPEC.md §5).
func (s *PostgresHistoricalPriceStore) GetHistory(ctx context.Context, symbol, timeframe string, limit int) ([]service.Bar, error) {
	rows, err := s.pool.Query(ctx, `
	SELECT timestamp, open, high, low, close, volume
	FROM historical_prices
	WHERE symbol = $1 AND timeframe = $2
	ORDER BY timestamp DESC
	LIMIT $3
	`, symbol, timeframe, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bars []service.Bar
	for rows.Next() {
		b := service.Bar{Symbol: symbol, Timeframe: timeframe}
		if err := rows.Scan(&b.Timestamp, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			return nil, err
		}
		bars = append(bars, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i, j := 0, len(bars)-1; i < j; i, j = i+1, j-1 {
		bars[i], bars[j] = bars[j], bars[i]
	}

	return bars, nil
}
