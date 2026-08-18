import { describe, expect, it } from 'vitest'

import { formatDate, formatPrice, formatQuantity } from './format'

describe('formatPrice', () => {
  it('renders two decimals, thousands-separated', () => {
    expect(formatPrice(1234.5)).toBe('1,234.50')
  })
})

describe('formatQuantity', () => {
  it('trims trailing zeros off a round quantity', () => {
    expect(formatQuantity(10)).toBe('10')
  })

  it('preserves the minimum tradable quantity', () => {
    expect(formatQuantity(0.0001)).toBe('0.0001')
  })

  it('trims a single trailing zero', () => {
    expect(formatQuantity(1.5)).toBe('1.5')
  })
})

describe('formatDate', () => {
  // Both assertions pin the exact UTC calendar date regardless of the
  // machine running the test: formatDate always renders via
  // {timeZone: 'UTC'}, so the result never depends on the runner's local
  // zone. Run on this dev machine (US Eastern, UTC-4), a naive
  // new Date(...).toLocaleDateString() with no timeZone override would
  // render the first case as 7/31/2024 -- the exact bug this function
  // exists to prevent.
  it('renders a UTC-midnight date string on its own calendar day', () => {
    expect(formatDate('2024-08-01T00:00:00Z')).toBe('8/1/2024')
  })

  it('renders a non-UTC-offset timestamp on its own calendar day', () => {
    expect(formatDate('2024-08-09T00:00:00-04:00')).toBe('8/9/2024')
  })
})
