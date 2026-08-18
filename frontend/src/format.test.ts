import { describe, expect, it } from 'vitest'

import { formatPrice, formatQuantity } from './format'

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
