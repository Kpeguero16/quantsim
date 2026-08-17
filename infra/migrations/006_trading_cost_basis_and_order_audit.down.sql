-- Drops exactly the four columns 006 added. The tables themselves belong to
-- migration 002 and are left alone.
--
-- Reversing this loses data that cannot be reconstructed: a trade's
-- realized_pl was true only at execution time, and re-deriving it later from
-- a moved avg_cost would produce a plausible wrong number rather than an
-- obvious gap. Dropping is still the right down migration -- the point is
-- that this is not a round trip.

ALTER TABLE trades DROP COLUMN realized_pl;
ALTER TABLE orders DROP COLUMN rejection_reason;
ALTER TABLE orders DROP COLUMN filled_price;
ALTER TABLE positions DROP COLUMN avg_cost;
