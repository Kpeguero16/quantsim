-- Reverses 008 -- but not a round trip (matching migration 006's own down
-- migration in stating that plainly, rather than pretending otherwise). An
-- RSI or MACD run has no window values to restore, and the schema this
-- migration returns to has no way to represent one: fabricating 0/0 to
-- satisfy the old NOT NULL short_window/long_window would leave a
-- plausible-looking wrong row rather than an honest gap. Deleting those
-- runs -- and their trade logs, via backtest_trades' own ON DELETE CASCADE
-- from migration 007 -- is the truthful reversal.
DELETE FROM backtests WHERE strategy != 'ma_crossover';

ALTER TABLE backtests ADD COLUMN short_window INT;
ALTER TABLE backtests ADD COLUMN long_window INT;

UPDATE backtests SET
    short_window = (params->>'short_window')::INT,
    long_window = (params->>'long_window')::INT;

ALTER TABLE backtests ALTER COLUMN short_window SET NOT NULL;
ALTER TABLE backtests ALTER COLUMN long_window SET NOT NULL;

ALTER TABLE backtests DROP COLUMN strategy;
ALTER TABLE backtests DROP COLUMN params;

-- Verified end to end in the same scratch database as the up migration:
-- against one ma_crossover row and one directly-inserted rsi row (with a
-- backtest_trades row attached), this deleted the rsi row and its trade
-- (cascaded via the existing FK), and restored the ma_crossover row's
-- short_window/long_window to exactly 5/20 -- matching what the up
-- migration had encoded into params for that same row.
