import { describe, expect, it } from 'vitest'

import { describeRejection } from './rejection-reason'

describe('describeRejection', () => {
  it.each([
    'insufficient_balance',
    'insufficient_position',
    'symbol_unavailable',
    'upstream_unavailable',
  ])('maps %s to a distinct, non-technical description', (reason) => {
    const description = describeRejection(reason)
    expect(description).not.toBe(reason)
    expect(description.length).toBeGreaterThan(0)
  })

  it('produces four distinct descriptions', () => {
    const descriptions = new Set(
      [
        'insufficient_balance',
        'insufficient_position',
        'symbol_unavailable',
        'upstream_unavailable',
      ].map(describeRejection),
    )
    expect(descriptions.size).toBe(4)
  })

  it('falls back to the raw string for an unknown reason, never throwing', () => {
    expect(() => describeRejection('some_new_reason')).not.toThrow()
    expect(describeRejection('some_new_reason')).toBe('some_new_reason')
  })
})
