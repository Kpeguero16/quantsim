-- Cost basis and the order audit trail: the two gaps in migration 002's
-- schema that block P/L and a truthful order history. Every table touched
-- here has existed and been empty since 002, so there is nothing to backfill.

-- Weighted-average cost basis, updated on every buy fill:
--   new_avg = (old_avg*old_qty + fill_price*fill_qty) / (old_qty + fill_qty)
-- and READ, never recomputed, on every sell.
--
-- NOT NULL DEFAULT 0 because a position row is created by its first buy,
-- which sets this in the same statement -- there is no window in which a
-- position legitimately has no cost basis. Zero is also exactly what the
-- formula wants for an empty position: a position sold down to quantity 0
-- and then bought again gets the new buy's price with no special case.
ALTER TABLE positions ADD COLUMN avg_cost NUMERIC(20, 4) NOT NULL DEFAULT 0;

-- The order's own record of what happened to it. Both nullable: at most one
-- of them is set on any completed order, and neither is set while it is
-- 'pending' -- a filled order has no rejection reason, a rejected one never
-- had a price.
--
-- filled_price is not redundant with its trade's price. Reading it back
-- through the trade works today and stops working the moment one order fills
-- in more than one trade. rejection_reason has nowhere else to live at all,
-- since a rejected order has no trade -- and rejected orders are persisted
-- deliberately (SPEC.md 2.5): an order history showing only the successes is
-- not an audit trail.
ALTER TABLE orders ADD COLUMN filled_price NUMERIC(20, 4);
ALTER TABLE orders ADD COLUMN rejection_reason TEXT;

-- Realized P/L, captured at execution time, on sell trades only.
--
-- This column exists because the obvious alternative is wrong rather than
-- merely slower. Recomputing a historical sell's P/L as
-- (price - avg_cost) * quantity from the position's CURRENT avg_cost gives a
-- different answer after any later buy moves that average. The number is only
-- true at the instant of the sell, so it is stored at the instant of the sell.
ALTER TABLE trades ADD COLUMN realized_pl NUMERIC(20, 4);
