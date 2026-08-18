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
