import { describe, expect, it } from 'vitest'

import { describeBacktestError } from './backtest-errors'

describe('describeBacktestError', () => {
  it('passes the backend message through verbatim for invalid_request', () => {
    expect(
      describeBacktestError(
        'invalid_request',
        'long_window must be at most 500',
      ),
    ).toBe('long_window must be at most 500')
  })

  it.each([
    'symbol_unavailable',
    'date_range_unavailable',
    'upstream_unavailable',
    'not_found',
  ])('maps %s to a distinct, non-technical description', (code) => {
    const description = describeBacktestError(code, 'raw backend message')
    expect(description).not.toBe('raw backend message')
    expect(description.length).toBeGreaterThan(0)
  })

  it('produces four distinct descriptions for the four static codes', () => {
    const descriptions = new Set(
      [
        'symbol_unavailable',
        'date_range_unavailable',
        'upstream_unavailable',
        'not_found',
      ].map((code) => describeBacktestError(code, 'raw backend message')),
    )
    expect(descriptions.size).toBe(4)
  })

  it('falls back to the backend message for an unrecognized code, never throwing', () => {
    expect(() =>
      describeBacktestError('some_new_code', 'a new backend message'),
    ).not.toThrow()
    expect(describeBacktestError('some_new_code', 'a new backend message')).toBe(
      'a new backend message',
    )
  })
})
