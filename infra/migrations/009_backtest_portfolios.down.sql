-- Reverses 009 -- deliberately not a round trip, the same way 006's and
-- 008's down migrations are not. A 3-symbol run has no single symbol to
-- restore, and the schema this returns to cannot represent one: picking
-- symbols[1] for it would leave a plausible-looking row asserting that a
-- portfolio run was an AAPL run. Deleting those runs -- and their trade
-- logs, via backtest_trades' ON DELETE CASCADE from 007 -- is the truthful
-- reversal. Single-symbol runs, which is every run made before 009, survive
-- intact.
DELETE FROM backtests WHERE cardinality(symbols) != 1;

ALTER TABLE backtests ADD COLUMN symbol TEXT;
UPDATE backtests SET symbol = symbols[1];
ALTER TABLE backtests ALTER COLUMN symbol SET NOT NULL;

ALTER TABLE backtests DROP COLUMN symbols;
ALTER TABLE backtest_trades DROP COLUMN symbol;
ALTER TABLE backtest_trades DROP COLUMN seq;

-- Verified end to end in the same scratch database as the up migration,
-- against a deliberately MIXED row set: the migrated single-symbol AAPL run
-- with its two trades, plus a directly-inserted 3-symbol {AAPL,MSFT,NVDA} run
-- with three trades of its own. This deleted the 3-symbol run and cascaded
-- away all three of its trades (007's ON DELETE CASCADE) -- same-bar trades
-- included -- left the AAPL run's two trades untouched, and restored its
-- symbol to exactly 'AAPL'. backtest_trades came back to 007's column set,
-- with both symbol and seq gone.
--
-- One cosmetic non-reversal, noted rather than papered over: the restored
-- symbol column lands last in ordinal position instead of third. Every
-- ADD COLUMN reversal has this property, 008's included, and nothing in this
-- codebase selects by position.
