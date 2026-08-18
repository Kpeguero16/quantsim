-- Backtest runs and their simulated trade logs (SPEC.md Step 16 §2.6).
-- Split the same way orders/trades already are: one summary row per run,
-- one row per simulated fill.

-- One row per POST /backtests. Parameters and the five agents.md §3 metrics
-- live together so GET /backtests is a cheap list with no joins.
--
-- profit_factor is nullable: a run with no losing trades has an undefined
-- ratio (SPEC.md §2.5), and 0 or Infinity would both read as a real number
-- instead of "not computable".
CREATE TABLE backtests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    symbol TEXT NOT NULL,
    short_window INT NOT NULL,
    long_window INT NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    starting_capital NUMERIC(20, 4) NOT NULL,
    final_equity NUMERIC(20, 4) NOT NULL,
    total_return_pct NUMERIC(20, 4) NOT NULL,
    sharpe_ratio NUMERIC(20, 4) NOT NULL,
    max_drawdown_pct NUMERIC(20, 4) NOT NULL,
    win_rate_pct NUMERIC(20, 4) NOT NULL,
    profit_factor NUMERIC(20, 4),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_backtests_user_id ON backtests(user_id, created_at DESC);

-- The simulated trade log for one run. Mirrors `trades`' own shape from
-- migration 006 -- side, quantity, price, realized_pl on sells only -- but
-- keyed to a backtest instead of an account, since these fills never touched
-- real money or a real position.
--
-- ON DELETE CASCADE: a backtest's trade log has no meaning once its parent
-- run is gone, unlike orders/trades which are a standalone audit trail.
CREATE TABLE backtest_trades (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    backtest_id UUID NOT NULL REFERENCES backtests(id) ON DELETE CASCADE,
    side TEXT NOT NULL,
    bar_timestamp TIMESTAMPTZ NOT NULL,
    price NUMERIC(20, 4) NOT NULL,
    quantity NUMERIC(20, 4) NOT NULL,
    realized_pl NUMERIC(20, 4)
);

CREATE INDEX idx_backtest_trades_backtest_id ON backtest_trades(backtest_id);
