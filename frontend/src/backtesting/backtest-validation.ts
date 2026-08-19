/**
 * Client-side mirror of validateRequest and NewStrategy
 * (services/backtesting/internal/service/{backtest,strategy}.go) -- catches
 * the common mistake before a round trip, never replaces the backend as the
 * authority (SPEC.md 2.5, Step 18 SPEC.md 2.8). Pure function so
 * BacktestForm.tsx and this file's test can both call it without rendering
 * anything.
 *
 * Two backend rejections have no client-side equivalent on purpose:
 * symbol_unavailable and date_range_unavailable both depend on what's
 * actually been ingested, which this module has no way to know (SPEC.md
 * 2.5) -- those surface only as a server response.
 */
import type {
  MACDParams,
  MACrossoverParams,
  RSIParams,
  RunBacktestRequest,
  StrategyKind,
} from '../api/types'

// Mirrors maxWarmupBars in services/backtesting/internal/service/strategy.go
// -- the one bound shared by all three strategies (Step 18 SPEC.md 2.4). Not
// read from a shared source -- independent literals, same convention
// OrderTicket.tsx already follows for its own bounds.
const MAX_WARMUP_BARS = 500

const MA_MIN_SHORT_WINDOW = 2
const RSI_MIN_PERIOD = 2
const MACD_MIN_FAST_PERIOD = 2
const MACD_MIN_SIGNAL_PERIOD = 2

/** Raw string values straight out of form inputs -- nothing parsed yet.
 * Holds every strategy's fields at once, not just the selected one's, so
 * switching strategies never loses what was typed into a now-hidden field
 * (BacktestForm.tsx). */
export interface BacktestFormValues {
  symbol: string
  strategy: StrategyKind
  shortWindow: string
  longWindow: string
  rsiPeriod: string
  oversold: string
  overbought: string
  fastPeriod: string
  slowPeriod: string
  signalPeriod: string
  startDate: string
  endDate: string
  startingCapital: string
}

export type BacktestFormValidation =
  | { ok: true; value: RunBacktestRequest }
  | { ok: false; error: string }

type FieldValidation<T> =
  | { ok: true; value: T }
  | { ok: false; error: string }

export function validateBacktestForm(
  values: BacktestFormValues,
): BacktestFormValidation {
  const symbol = values.symbol.trim()
  if (symbol === '') {
    return { ok: false, error: 'Symbol is required.' }
  }

  if (values.startDate.trim() === '' || values.endDate.trim() === '') {
    return { ok: false, error: 'Start date and end date are required.' }
  }
  // YYYY-MM-DD strings (what a <input type="date"> produces) sort
  // lexicographically the same as chronologically, so a plain string
  // comparison matches the backend's time.Time.Before check exactly,
  // including its "equal is not before" rejection.
  if (!(values.startDate < values.endDate)) {
    return { ok: false, error: 'Start date must be before end date.' }
  }

  const startingCapital = Number(values.startingCapital)
  if (!Number.isFinite(startingCapital) || startingCapital <= 0) {
    return { ok: false, error: 'Starting capital must be greater than 0.' }
  }

  const base = {
    symbol: symbol.toUpperCase(),
    start_date: values.startDate,
    end_date: values.endDate,
    starting_capital: startingCapital,
  }

  switch (values.strategy) {
    case 'ma_crossover': {
      const params = validateMACrossoverParams(values)
      if (!params.ok) return params
      return {
        ok: true,
        value: { ...base, strategy: 'ma_crossover', params: params.value },
      }
    }
    case 'rsi': {
      const params = validateRSIParams(values)
      if (!params.ok) return params
      return { ok: true, value: { ...base, strategy: 'rsi', params: params.value } }
    }
    case 'macd': {
      const params = validateMACDParams(values)
      if (!params.ok) return params
      return { ok: true, value: { ...base, strategy: 'macd', params: params.value } }
    }
  }
}

function validateMACrossoverParams(
  values: BacktestFormValues,
): FieldValidation<MACrossoverParams> {
  const shortWindow = Number(values.shortWindow)
  if (!Number.isInteger(shortWindow) || shortWindow < MA_MIN_SHORT_WINDOW) {
    return {
      ok: false,
      error: `Short window must be a whole number of at least ${MA_MIN_SHORT_WINDOW}.`,
    }
  }

  const longWindow = Number(values.longWindow)
  if (!Number.isInteger(longWindow) || longWindow <= shortWindow) {
    return {
      ok: false,
      error: 'Long window must be a whole number greater than the short window.',
    }
  }
  if (longWindow > MAX_WARMUP_BARS) {
    return { ok: false, error: `Long window must be at most ${MAX_WARMUP_BARS}.` }
  }

  return { ok: true, value: { short_window: shortWindow, long_window: longWindow } }
}

function validateRSIParams(values: BacktestFormValues): FieldValidation<RSIParams> {
  const period = Number(values.rsiPeriod)
  if (!Number.isInteger(period) || period < RSI_MIN_PERIOD) {
    return {
      ok: false,
      error: `RSI period must be a whole number of at least ${RSI_MIN_PERIOD}.`,
    }
  }
  // WarmupBars = period + 1 (SPEC.md Step 18 2.4), so this is the same bound
  // as MAX_WARMUP_BARS, one bar removed.
  if (period > MAX_WARMUP_BARS - 1) {
    return { ok: false, error: `RSI period must be at most ${MAX_WARMUP_BARS - 1}.` }
  }

  const oversold = Number(values.oversold)
  const overbought = Number(values.overbought)
  if (
    !Number.isFinite(oversold) ||
    !Number.isFinite(overbought) ||
    !(oversold > 0 && oversold < overbought && overbought < 100)
  ) {
    return {
      ok: false,
      error: 'Oversold and overbought must satisfy 0 < oversold < overbought < 100.',
    }
  }

  return { ok: true, value: { period, oversold, overbought } }
}

function validateMACDParams(values: BacktestFormValues): FieldValidation<MACDParams> {
  const fastPeriod = Number(values.fastPeriod)
  if (!Number.isInteger(fastPeriod) || fastPeriod < MACD_MIN_FAST_PERIOD) {
    return {
      ok: false,
      error: `Fast period must be a whole number of at least ${MACD_MIN_FAST_PERIOD}.`,
    }
  }

  const slowPeriod = Number(values.slowPeriod)
  if (!Number.isInteger(slowPeriod) || slowPeriod <= fastPeriod) {
    return {
      ok: false,
      error: 'Slow period must be a whole number greater than the fast period.',
    }
  }

  const signalPeriod = Number(values.signalPeriod)
  if (!Number.isInteger(signalPeriod) || signalPeriod < MACD_MIN_SIGNAL_PERIOD) {
    return {
      ok: false,
      error: `Signal period must be a whole number of at least ${MACD_MIN_SIGNAL_PERIOD}.`,
    }
  }

  // WarmupBars = slow_period + signal_period - 1 (SPEC.md Step 18 2.4) --
  // the case a bound checked only on slow_period would miss.
  if (slowPeriod + signalPeriod - 1 > MAX_WARMUP_BARS) {
    return {
      ok: false,
      error: `Slow period plus signal period must be at most ${MAX_WARMUP_BARS + 1}.`,
    }
  }

  return {
    ok: true,
    value: { fast_period: fastPeriod, slow_period: slowPeriod, signal_period: signalPeriod },
  }
}
