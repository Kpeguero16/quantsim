-- Makes backtests multi-strategy (Step 18 SPEC.md §2.5). short_window and
-- long_window described only moving-average crossover; strategy/params
-- describe any of the three named strategies uniformly, since they're only
-- ever read back whole to re-render one run, never filtered or aggregated
-- on -- exactly the condition under which JSONB costs nothing.
--
-- No CHECK (strategy IN (...)): that would mean a migration per future
-- strategy, the exact cost this design exists to avoid.

ALTER TABLE backtests ADD COLUMN strategy TEXT NOT NULL DEFAULT 'ma_crossover';
ALTER TABLE backtests ADD COLUMN params JSONB;

-- Backfills every existing row (all moving-average crossover, the only
-- strategy that existed before this migration) into the new params shape.
UPDATE backtests SET params = jsonb_build_object(
    'short_window', short_window, 'long_window', long_window);

-- The DEFAULT above exists only to make the new NOT NULL column legal
-- against the rows just backfilled; dropped immediately after so no future
-- insert can quietly omit the strategy.
ALTER TABLE backtests ALTER COLUMN params SET NOT NULL;
ALTER TABLE backtests ALTER COLUMN strategy DROP DEFAULT;

ALTER TABLE backtests DROP COLUMN short_window, DROP COLUMN long_window;

-- Verified end to end in a throwaway database (step18_migration_scratch,
-- created and dropped; the real dev database was untouched throughout,
-- still users=20/backtests=0 afterward): applied against a pre-existing
-- ma_crossover row, producing exactly
-- {"short_window": 5, "long_window": 20}.
