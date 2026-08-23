import { describe, expect, it } from 'vitest'

import { describeInsightsError } from './insights-errors'

describe('describeInsightsError', () => {
  it('says nothing for invalid_token', () => {
    // The client's refresh-and-retry has already run and failed, so the
    // session is being cleared. An error about portfolio analytics on the
    // way to the sign-in screen would explain the wrong thing.
    expect(
      describeInsightsError('invalid_token', 'invalid or expired token'),
    ).toBeNull()
  })

  it('passes the symbol_unavailable message through verbatim', () => {
    // It names the symbol and the date; a replacement sentence names
    // neither.
    const message = 'no historical data is available for AAPL on 2026-03-04'
    expect(describeInsightsError('symbol_unavailable', message)).toBe(message)
  })

  it.each(['upstream_unavailable', 'internal_error'])(
    'replaces %s with copy that is not the backend string',
    (code) => {
      const description = describeInsightsError(code, 'raw backend message')
      expect(description).not.toBe('raw backend message')
      expect(description?.length).toBeGreaterThan(0)
    },
  )

  it('gives the two replaced codes distinct copy', () => {
    expect(describeInsightsError('upstream_unavailable', 'x')).not.toBe(
      describeInsightsError('internal_error', 'x'),
    )
  })

  it.each([
    ['timeout', 'The server took too long to respond. Please try again.'],
    [
      'network_error',
      'Could not reach the server. Check that the gateway is running.',
    ],
  ])('passes the %s transport message through', (code, message) => {
    // api/client.ts writes these for a reader already.
    expect(describeInsightsError(code, message)).toBe(message)
  })

  it('falls back to the backend message for an unrecognized code', () => {
    expect(describeInsightsError('some_new_code', 'a new message')).toBe(
      'a new message',
    )
  })

  it('never throws on an empty code or message', () => {
    expect(() => describeInsightsError('', '')).not.toThrow()
  })
})
