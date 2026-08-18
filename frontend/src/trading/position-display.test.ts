import { describe, expect, it } from 'vitest'

import type { Position } from '../api/types'
import { derivePositionDisplay, hasUnpricedPosition } from './position-display'

function makePosition(overrides: Partial<Position> = {}): Position {
  return {
    symbol: 'AAPL',
    quantity: 10,
    avg_cost: 100,
    latest_price: 110,
    unrealized_pl: 100,
    ...overrides,
  }
}

describe('derivePositionDisplay', () => {
  it('renders an em-dash for both price and P/L when latest_price is null, regardless of unrealized_pl', () => {
    const atZero = derivePositionDisplay(
      makePosition({ latest_price: null, unrealized_pl: 0 }),
    )
    const nonZero = derivePositionDisplay(
      makePosition({ latest_price: null, unrealized_pl: 47.5 }),
    )

    expect(atZero.priceLabel).toBe('—')
    expect(atZero.plLabel).toBe('—')
    expect(atZero.plDirection).toBe('unknown')

    expect(nonZero.priceLabel).toBe('—')
    expect(nonZero.plLabel).toBe('—')
    expect(nonZero.plDirection).toBe('unknown')
  })

  it('reports up for a positive unrealized_pl with a real price', () => {
    const display = derivePositionDisplay(
      makePosition({ latest_price: 110, unrealized_pl: 100 }),
    )
    expect(display.plDirection).toBe('up')
    expect(display.priceLabel).toBe('110.00')
  })

  it('reports down for a negative unrealized_pl with a real price', () => {
    const display = derivePositionDisplay(
      makePosition({ latest_price: 90, unrealized_pl: -100 }),
    )
    expect(display.plDirection).toBe('down')
  })

  it('reports flat for an exact zero unrealized_pl with a real price', () => {
    const display = derivePositionDisplay(
      makePosition({ latest_price: 100, unrealized_pl: 0 }),
    )
    expect(display.plDirection).toBe('flat')
  })
})

describe('hasUnpricedPosition', () => {
  it('is false for an empty list', () => {
    expect(hasUnpricedPosition([])).toBe(false)
  })

  it('is true when one of several positions is unpriced', () => {
    const positions = [
      makePosition({ symbol: 'AAPL' }),
      makePosition({ symbol: 'MSFT', latest_price: null }),
      makePosition({ symbol: 'TSLA' }),
    ]
    expect(hasUnpricedPosition(positions)).toBe(true)
  })

  it('is false when all positions are priced', () => {
    const positions = [
      makePosition({ symbol: 'AAPL' }),
      makePosition({ symbol: 'MSFT' }),
    ]
    expect(hasUnpricedPosition(positions)).toBe(false)
  })
})
