/**
 * Shared number formatting for anything money- or quantity-shaped. Moved
 * out of PriceList.tsx so the trading components (SPEC.md 2.9) share the
 * same rules instead of re-deriving them.
 */

/** Two decimal places, thousands-separated. No currency symbol -- callers
 * that need "$" prepend it themselves, matching PriceList.tsx's convention. */
export function formatPrice(price: number): string {
  return price.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })
}

/**
 * Up to 4 decimal places, matching the backend's quantityScale = 1e4
 * (services/trading-engine/internal/service/trading.go), with trailing
 * zeros trimmed so a round quantity like 10 shares reads as "10", not
 * "10.0000".
 */
export function formatQuantity(quantity: number): string {
  return quantity.toLocaleString('en-US', {
    minimumFractionDigits: 0,
    maximumFractionDigits: 4,
  })
}

/**
 * Formats an RFC3339 timestamp as a calendar date, independent of the
 * viewer's local timezone. A backtest's start_date/end_date and a daily
 * bar's bar_timestamp have no meaningful time-of-day -- they're calendar
 * dates encoded as midnight in some reference offset. Converting through
 * Date's *local*-timezone rendering can shift the displayed day backward
 * for any viewer west of that offset: `2024-08-01T00:00:00Z` reads as
 * 7/31/2024 in US Eastern. Rendering with `timeZone: 'UTC'` instead reads
 * off the timestamp's own calendar fields, which is stable for every
 * value this app actually receives (UTC midnight for dates the backend
 * parsed itself, or a US-market non-UTC offset for ingested bars -- both
 * land on the same UTC calendar day, never the day before).
 */
export function formatDate(timestamp: string): string {
  return new Date(timestamp).toLocaleDateString('en-US', { timeZone: 'UTC' })
}
