-- Makes one backtest a PORTFOLIO of symbols (Step 19 SPEC.md §2.5). A run
-- draws every symbol from one shared pool of capital and produces one set of
-- metrics over the combined equity curve, so "which symbol was this run for"
-- stops being a single answer.
--
-- backtest_trades gains its own symbol rather than inheriting the parent's:
-- a portfolio run's trade log interleaves fills across symbols, and a trade
-- that cannot say which symbol it was is unreadable. Every existing trade
-- gets its parent's symbol, which is exactly right -- those runs had one.
--
-- It also gains seq, because a trade log is a SEQUENCE and this schema had no
-- way to say so. Reads ordered by (bar_timestamp, id) and relied on ties being
-- impossible -- true while one run meant one symbol and so at most one fill
-- per bar. A portfolio run fills several symbols on the same bar routinely,
-- and id is a random UUID, so the engine's within-bar order (sells first, then
-- buys alphabetically) came back shuffled differently on every write. Storing
-- the order is the fix; re-deriving it in an ORDER BY would be a second copy
-- of the engine's rules, free to drift from them.
--
-- No CHECK on cardinality: the 1..10 bound is a request-validation rule
-- (§2.4) that this project enforces in validateRequest, alongside every
-- other bound on a request. A CHECK here would be a second, silently
-- divergent copy of it.

ALTER TABLE backtests ADD COLUMN symbols TEXT[];
UPDATE backtests SET symbols = ARRAY[symbol];
ALTER TABLE backtests ALTER COLUMN symbols SET NOT NULL;

-- Statement order matters: this backfill JOINS against backtests.symbol, so
-- it has to run before that column is dropped below.
ALTER TABLE backtest_trades ADD COLUMN symbol TEXT;
UPDATE backtest_trades t SET symbol = b.symbol FROM backtests b WHERE t.backtest_id = b.id;
ALTER TABLE backtest_trades ALTER COLUMN symbol SET NOT NULL;

-- 0-based, matching the Go slice index the trade log is written from.
ALTER TABLE backtest_trades ADD COLUMN seq INTEGER;
UPDATE backtest_trades t SET seq = ordered.rn
FROM (
    SELECT id, row_number() OVER (PARTITION BY backtest_id ORDER BY bar_timestamp, id) - 1 AS rn
    FROM backtest_trades
) ordered
WHERE ordered.id = t.id;
ALTER TABLE backtest_trades ALTER COLUMN seq SET NOT NULL;

-- Without this an ordering column can quietly take duplicates, which puts the
-- arbitrary order right back.
ALTER TABLE backtest_trades ADD CONSTRAINT backtest_trades_backtest_id_seq_key UNIQUE (backtest_id, seq);

ALTER TABLE backtests DROP COLUMN symbol;

-- Verified end to end in a throwaway database (step19_migration_scratch,
-- created and dropped; the real dev database was untouched throughout, still
-- users=20/backtests=0 afterward). Applied at 008 against one pre-existing
-- single-symbol AAPL run carrying two trades: symbols became {AAPL} and BOTH
-- trades were backfilled to AAPL, with seq 0/1 in bar_timestamp order.
--
-- The seq backfill was checked against trades INSERTed in reverse
-- chronological order: they came out 0 = the earlier buy, 1 = the later sell,
-- so row_number()'s ORDER BY is doing the work and not insertion order. A
-- second row at an existing (backtest_id, seq) is refused by the constraint.
--
-- The statement order above is load-bearing, and that was tested rather than
-- assumed: with DROP COLUMN symbol moved ahead of the trade backfill, the
-- same sequence fails with `ERROR: column b.symbol does not exist`.
--
-- This backfill cannot be covered by the integration harness, which always
-- migrates a freshly created, EMPTY database to head -- the UPDATE there
-- always runs against zero rows (Step 19 plan D2, the same wall 008 hit).
-- Do not add a test that appears to cover it.
