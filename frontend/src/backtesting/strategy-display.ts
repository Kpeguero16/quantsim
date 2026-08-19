/**
 * One human-readable line for a backtest's strategy and parameters --
 * BacktestResult.tsx's header and BacktestHistoryList.tsx's row both used
 * to format "{short_window}/{long_window} crossover" inline; this is the
 * one place that does it now that a run can be any of three strategies
 * (Step 18 SPEC.md 2.8).
 */
import type {
  BacktestParams,
  MACDParams,
  MACrossoverParams,
  RSIParams,
  StrategyKind,
} from '../api/types'

// Deliberately not `Pick<Backtest, 'strategy' | 'params'>`: Pick applied to
// a union type does not distribute over its members, so it would collapse
// the two fields into one flat { strategy: StrategyKind; params:
// BacktestParams } shape and lose the pairing between a given strategy and
// its own params type -- exactly the correlation the switch below relies on
// to narrow `backtest.params` inside each case. Restating the union
// directly here keeps that pairing intact; any Backtest or BacktestDetail
// value is still assignable to it, since each has every field this type
// needs plus more.
export type BacktestParamsByKind =
  | { strategy: 'ma_crossover'; params: MACrossoverParams }
  | { strategy: 'rsi'; params: RSIParams }
  | { strategy: 'macd'; params: MACDParams }

/**
 * Never throws, never renders blank -- an unrecognized strategy string
 * (a run from a build newer than this one, or a future strategy this
 * frontend doesn't know about) renders its raw name rather than crashing
 * the whole dashboard the way an unconditional `.length` access on a
 * differently-shaped value already did once in this app (Step 17's
 * `trades: null` bug -- the API is the source of truth for what a stored
 * run is, and a frontend that panics on a value it doesn't recognize
 * repeats that mistake).
 */
export function describeStrategy(backtest: BacktestParamsByKind): string {
  switch (backtest.strategy) {
    case 'ma_crossover':
      return `${backtest.params.short_window}/${backtest.params.long_window} crossover`
    case 'rsi':
      return `RSI(${backtest.params.period}) ${backtest.params.oversold}/${backtest.params.overbought}`
    case 'macd':
      return `MACD(${backtest.params.fast_period}/${backtest.params.slow_period}/${backtest.params.signal_period})`
    default:
      // Runtime data is not guaranteed to match the StrategyKind union --
      // that's a compile-time promise, not a wire-format one.
      return describeUnknownStrategy(
        backtest as { strategy: string; params: unknown },
      )
  }
}

function describeUnknownStrategy(backtest: {
  strategy: string
  params: unknown
}): string {
  return backtest.strategy || 'unknown strategy'
}

// Re-exported so a consumer only needs to import from this module for both
// the union and the function that switches on it.
export type { StrategyKind, BacktestParams }
