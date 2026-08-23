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

/**
 * One decimal place, no thousands separator. For percentages whose sign
 * carries no meaning -- a position weight, an annualized volatility.
 *
 * This mirrors KindPercent in the backend's renderer
 * (services/ai-insights/internal/narrative/render.go). That file is the
 * convention rather than this one, because format.ts had no percent
 * formatter at all when Step 21 was written -- and the two must agree
 * character for character, since the same figure appears both on a card
 * this module formats and inside a sentence Go formatted (SPEC.md 2.4).
 *
 * toLocaleString, NOT toFixed. Step 21's own note says any browser
 * one-liner matches the backend's away-from-zero rounding; that is true of
 * the rule and false of toFixed, which rounds the exact binary value.
 * -99.85 is really -99.8499999999999943, so toFixed(1) prints "-99.8"
 * where the backend's scale-then-round prints "-99.9". Measured against
 * the Go implementation over 60,002 constructed decimals in +/-100:
 * toLocaleString disagreed on 0, toFixed on 960.
 *
 * useGrouping: false is load-bearing too -- the backend groups money but
 * not percentages, so a 1234.5% return must not render as "1,234.5%".
 * Both rules live in fixed() below, with the rounding.
 */
export function formatPercent(pct: number): string {
  return `${fixed(pct, 1)}%`
}

/**
 * Same, with an explicit "+" on positives. For percentages whose sign is
 * the point -- a return, an excess over a benchmark, a drawdown. Mirrors
 * KindSignedPercent.
 *
 * The sign is decided on the raw value, not the rounded one, which is what
 * the backend does: a return of +0.04999% renders "+0.0%" on both sides.
 * Deciding it after rounding would drop the "+" there and disagree.
 */
export function formatSignedPercent(pct: number): string {
  return `${pct > 0 ? '+' : ''}${fixed(pct, 1)}%`
}

/**
 * Sharpe ratios and the like: two decimals, dimensionless, no separator.
 * Mirrors KindRatio.
 */
export function formatRatio(value: number): string {
  return fixed(value, 2)
}

/**
 * The concentration index (HHI): three decimals, dimensionless and small.
 * Mirrors KindIndex.
 */
export function formatIndex(value: number): string {
  return fixed(value, 3)
}

/**
 * The one place a figure becomes digits, so no two formatters above can
 * spell the same number differently -- and a port of the backend's
 * roundHalfAway (render.go), so none of them can disagree with the sentence
 * beside it either.
 *
 * WHY a port rather than the one-line toLocaleString this file used first.
 * Plain toLocaleString rounds the SHORTEST DECIMAL FORM, while Go's
 * scale-then-round rounds the exact binary value once the multiply has
 * nudged it onto a boundary. At one decimal place the two never disagreed
 * -- 0 mismatches over 60,002 constructed decimals -- which is why the
 * percent formatters originally shipped without this. At two and three
 * places they do: 272 and 184 mismatches over 270,002 values, all of them
 * decimal literals like -9.995 whose double is a hair BELOW the halfway
 * point. Sharpe and HHI live at those precisions.
 *
 * Rounding the magnitude and reapplying the sign is the whole trick:
 * Math.round sends a negative halfway case toward +Infinity (-2.5 -> -2)
 * where Go's math.Round sends it away from zero (-3). Math.sign preserves
 * negative zero, so a figure too small to show but negative still renders
 * "-0.0" -- which the backend also prints, and matching it matters more
 * than prettifying one side.
 *
 * Measured at 0 mismatches over 330,004 values across all three
 * precisions, covering every constructed 3- and 4-decimal literal in range
 * plus 50,000 random doubles. That is evidence, not a proof: the two
 * languages still hand an already-rounded double to two different decimal
 * renderers at the last step. The parity tables in format.test.ts and
 * render_test.go are what keep it honest.
 */
function fixed(value: number, places: number): string {
  const scale = 10 ** places
  const rounded = (Math.sign(value) * Math.round(Math.abs(value) * scale)) / scale
  return rounded.toLocaleString('en-US', {
    minimumFractionDigits: places,
    maximumFractionDigits: places,
    useGrouping: false,
  })
}
