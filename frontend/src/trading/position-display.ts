/**
 * The null-price/null-P&L rule from SPEC.md 2.5, as one pure function
 * instead of two components (PositionsTable, PortfolioSummary)
 * independently re-deriving it -- the same reasoning Step 14's plan used
 * to put weighted-avg-cost math in one place (money.go) rather than
 * duplicating it.
 *
 * `latest_price: null` means market-data could not price the position.
 * The backend still returns a real `unrealized_pl` in that case (valued
 * at cost, so it comes back as 0.0) -- but that 0.0 is not evidence of
 * anything, so this derivation keys off `latest_price` alone and never
 * inspects `unrealized_pl` when the price is null.
 */
import type { Position } from '../api/types'
import { formatPrice } from '../format'

const EM_DASH = '—'

export interface PositionDisplay {
  priceLabel: string
  plLabel: string
  plDirection: 'up' | 'down' | 'flat' | 'unknown'
}

export function derivePositionDisplay(position: Position): PositionDisplay {
  if (position.latest_price === null) {
    return { priceLabel: EM_DASH, plLabel: EM_DASH, plDirection: 'unknown' }
  }

  const plDirection =
    position.unrealized_pl > 0
      ? 'up'
      : position.unrealized_pl < 0
        ? 'down'
        : 'flat'

  return {
    priceLabel: formatPrice(position.latest_price),
    plLabel: formatPrice(position.unrealized_pl),
    plDirection,
  }
}

export function hasUnpricedPosition(positions: Position[]): boolean {
  return positions.some((position) => position.latest_price === null)
}
