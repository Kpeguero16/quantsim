// Package store implements the backtesting service's persistence against
// Postgres.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kpeguero/quantsim/services/backtesting/internal/service"
)

var _ service.BacktestStore = (*PostgresBacktestStore)(nil)

// []string binds to and scans from TEXT[] through pgx v5's default codec,
// with no pgtype wrapper (Step 19 plan D5). This is the repo's first array
// column, so it has no precedent here and the round trip is asserted against
// real Postgres in the integration suite rather than trusted to compile.
type PostgresBacktestStore struct {
	pool *pgxpool.Pool
}

func NewPostgresBacktestStore(pool *pgxpool.Pool) *PostgresBacktestStore {
	return &PostgresBacktestStore{pool: pool}
}

// SaveBacktest persists the run and its trade log together in one
// transaction. Unlike trading-engine's ExecuteOrder there is no row to lock
// -- nothing else can ever write this run, since it is identified by a
// freshly generated id and never revisited by another request. The
// transaction exists only so a trade-log insert failure cannot leave a
// backtests row with no matching trades.
func (s *PostgresBacktestStore) SaveBacktest(ctx context.Context, b service.Backtest, trades []service.TradeRecord) (service.Backtest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return service.Backtest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	err = tx.QueryRow(ctx, `
	INSERT INTO backtests (
		user_id, symbols, strategy, params, start_date, end_date,
		starting_capital, final_equity, total_return_pct, sharpe_ratio,
		max_drawdown_pct, win_rate_pct, profit_factor
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	RETURNING id, created_at
	`,
		b.UserID, b.Symbols, b.Strategy, b.Params, b.StartDate, b.EndDate,
		b.StartingCapital, b.FinalEquity, b.Metrics.TotalReturnPct, b.Metrics.SharpeRatio,
		b.Metrics.MaxDrawdownPct, b.Metrics.WinRatePct, b.Metrics.ProfitFactor,
	).Scan(&b.ID, &b.CreatedAt)
	if err != nil {
		return service.Backtest{}, fmt.Errorf("inserting backtest: %w", err)
	}

	if len(trades) > 0 {
		batch := &pgx.Batch{}
		// seq is the index in the log the engine produced. Several symbols
		// fill on the same bar in a portfolio run, and bar_timestamp alone
		// cannot order those; without seq the read-back order of a same-bar
		// group is whatever their random UUIDs sort to.
		for i, t := range trades {
			batch.Queue(`
			INSERT INTO backtest_trades (backtest_id, seq, symbol, side, bar_timestamp, price, quantity, realized_pl)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			`, b.ID, i, t.Symbol, t.Side, t.BarTimestamp, t.Price, t.Quantity, t.RealizedPL)
		}
		results := tx.SendBatch(ctx, batch)
		for range trades {
			if _, err := results.Exec(); err != nil {
				_ = results.Close()
				return service.Backtest{}, fmt.Errorf("inserting backtest trade: %w", err)
			}
		}
		if err := results.Close(); err != nil {
			return service.Backtest{}, fmt.Errorf("inserting backtest trades: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return service.Backtest{}, fmt.Errorf("committing backtest: %w", err)
	}
	return b, nil
}

func (s *PostgresBacktestStore) ListBacktests(ctx context.Context, userID uuid.UUID) ([]service.Backtest, error) {
	rows, err := s.pool.Query(ctx, `
	SELECT id, user_id, symbols, strategy, params, start_date, end_date,
	       starting_capital, final_equity, total_return_pct, sharpe_ratio,
	       max_drawdown_pct, win_rate_pct, profit_factor, created_at
	FROM backtests
	WHERE user_id = $1
	ORDER BY created_at DESC, id DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil so a user with no runs encodes as [] rather than null.
	backtests := []service.Backtest{}
	for rows.Next() {
		b, err := scanBacktest(rows)
		if err != nil {
			return nil, err
		}
		backtests = append(backtests, b)
	}
	return backtests, rows.Err()
}

// GetBacktest scopes the lookup to (id, user_id) in one WHERE clause, so a
// non-owner's request and a nonexistent id produce the identical
// pgx.ErrNoRows outcome -- ErrNotFound either way, never a way to tell the
// two apart from outside (SPEC.md §2.7).
func (s *PostgresBacktestStore) GetBacktest(ctx context.Context, userID, id uuid.UUID) (service.BacktestDetail, error) {
	row := s.pool.QueryRow(ctx, `
	SELECT id, user_id, symbols, strategy, params, start_date, end_date,
	       starting_capital, final_equity, total_return_pct, sharpe_ratio,
	       max_drawdown_pct, win_rate_pct, profit_factor, created_at
	FROM backtests
	WHERE id = $1 AND user_id = $2
	`, id, userID)

	b, err := scanBacktest(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.BacktestDetail{}, service.ErrNotFound
		}
		return service.BacktestDetail{}, err
	}

	rows, err := s.pool.Query(ctx, `
	SELECT symbol, side, bar_timestamp, price, quantity, realized_pl
	FROM backtest_trades
	WHERE backtest_id = $1
	ORDER BY seq ASC
	`, id)
	if err != nil {
		return service.BacktestDetail{}, err
	}
	defer rows.Close()

	trades := []service.TradeRecord{}
	for rows.Next() {
		var t service.TradeRecord
		if err := rows.Scan(&t.Symbol, &t.Side, &t.BarTimestamp, &t.Price, &t.Quantity, &t.RealizedPL); err != nil {
			return service.BacktestDetail{}, err
		}
		trades = append(trades, t)
	}
	if err := rows.Err(); err != nil {
		return service.BacktestDetail{}, err
	}

	return service.BacktestDetail{Backtest: b, Trades: trades}, nil
}

// scanner is whatever a single backtests row can be read from -- pgx.Row
// from QueryRow, or pgx.Rows mid-iteration from Query. Sharing this lets
// ListBacktests and GetBacktest scan the identical column list once instead
// of keeping two copies in sync.
type scanner interface {
	Scan(dest ...any) error
}

func scanBacktest(row scanner) (service.Backtest, error) {
	var b service.Backtest
	err := row.Scan(
		&b.ID, &b.UserID, &b.Symbols, &b.Strategy, &b.Params, &b.StartDate, &b.EndDate,
		&b.StartingCapital, &b.FinalEquity, &b.Metrics.TotalReturnPct, &b.Metrics.SharpeRatio,
		&b.Metrics.MaxDrawdownPct, &b.Metrics.WinRatePct, &b.Metrics.ProfitFactor, &b.CreatedAt,
	)
	// symbols is NOT NULL and never written empty, so this cannot fire today.
	// It exists because the alternative if it ever did -- Symbols marshaling
	// as null on a field the API contract types as an array -- is a wire-shape
	// break discovered by a frontend, which is a worse place to find it than
	// one line of store code (Step 19 T3).
	if b.Symbols == nil {
		b.Symbols = []string{}
	}
	return b, err
}
