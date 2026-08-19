import { describe, expect, it } from 'vitest'

import { describeStrategy } from './strategy-display'

describe('describeStrategy', () => {
  it('describes a moving-average crossover', () => {
    expect(
      describeStrategy({
        strategy: 'ma_crossover',
        params: { short_window: 5, long_window: 20 },
      }),
    ).toBe('5/20 crossover')
  })

  it('describes an RSI strategy', () => {
    expect(
      describeStrategy({
        strategy: 'rsi',
        params: { period: 14, oversold: 30, overbought: 70 },
      }),
    ).toBe('RSI(14) 30/70')
  })

  it('describes a MACD strategy', () => {
    expect(
      describeStrategy({
        strategy: 'macd',
        params: { fast_period: 12, slow_period: 26, signal_period: 9 },
      }),
    ).toBe('MACD(12/26/9)')
  })

  // The API is the source of truth for what a stored run is -- a run from a
  // build newer than this one (or a future strategy this frontend has never
  // heard of) must render its raw name, not crash the dashboard the way an
  // unconditional field access already did once (Step 17's `trades: null`
  // bug). Cast through `unknown` since this is deliberately outside
  // StrategyKind's compile-time union -- the point of the test.
  it('falls back to the raw strategy name for an unrecognized strategy, without throwing', () => {
    const unknown = {
      strategy: 'bollinger_bands',
      params: { anything: 'goes' },
    } as unknown as Parameters<typeof describeStrategy>[0]

    expect(() => describeStrategy(unknown)).not.toThrow()
    expect(describeStrategy(unknown)).toBe('bollinger_bands')
  })

  it('falls back to a generic label for an empty strategy string', () => {
    const empty = {
      strategy: '',
      params: {},
    } as unknown as Parameters<typeof describeStrategy>[0]

    expect(describeStrategy(empty)).toBe('unknown strategy')
  })
})
