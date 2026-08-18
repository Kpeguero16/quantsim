/**
 * Human-readable copy for the four rejection reasons the trading engine
 * persists to order history (services/trading-engine/internal/service/
 * trading.go). Deliberately four, not five: `invalid_request` (a
 * malformed order that fails validation before an account is ever
 * touched) is never written to the `orders` table, so it can never reach
 * this function from order history (SPEC.md 2.6). OrderTicket.tsx handles
 * `invalid_request` inline, at the point the API call itself fails.
 */
export type RejectionReason =
  | 'insufficient_balance'
  | 'insufficient_position'
  | 'symbol_unavailable'
  | 'upstream_unavailable'

const DESCRIPTIONS: Record<RejectionReason, string> = {
  insufficient_balance: 'Not enough cash balance to place this order.',
  insufficient_position: 'Not enough shares held to sell this quantity.',
  symbol_unavailable: 'No market data is available for this symbol.',
  upstream_unavailable: 'The market data service is temporarily unavailable.',
}

// Exported so OrderTicket.tsx's live-submit error path can check the same
// four codes this module already knows about, rather than re-listing them
// in a second if/else the two files could drift out of sync on.
export function isRejectionReason(reason: string): reason is RejectionReason {
  return Object.hasOwn(DESCRIPTIONS, reason)
}

/**
 * Never throws, never renders blank -- an unrecognized reason falls back
 * to the raw string rather than hiding it.
 */
export function describeRejection(reason: string): string {
  return isRejectionReason(reason) ? DESCRIPTIONS[reason] : reason
}
