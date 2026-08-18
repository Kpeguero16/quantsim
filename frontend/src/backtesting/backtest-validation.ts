/**
 * Client-side mirror of validateRequest
 * (services/backtesting/internal/service/backtest.go) -- catches the common
 * mistake before a round trip, never replaces the backend as the authority
 * (SPEC.md 2.5). Pure function so BacktestForm.tsx and this file's test can
 * both call it without rendering anything.
 *
 * Two backend rejections have no client-side equivalent on purpose:
 * symbol_unavailable and date_range_unavailable both depend on what's
 * actually been ingested, which this module has no way to know (SPEC.md
 * 2.5) -- those surface only as a server response.
 */

// Mirrors minShortWindow/maxLongWindow in
// services/backtesting/internal/service/backtest.go. Not read from a shared
// source -- the two are independent literals, same convention
// OrderTicket.tsx already follows for its own bounds.
const MIN_SHORT_WINDOW = 2
const MAX_LONG_WINDOW = 500

/** Raw string values straight out of form inputs -- nothing parsed yet. */
export interface BacktestFormValues {
  symbol: string
  shortWindow: string
  longWindow: string
  startDate: string
  endDate: string
  startingCapital: string
}

/** The parsed, backend-ready shape once validation passes. */
export interface ValidatedBacktestForm {
  symbol: string
  short_window: number
  long_window: number
  start_date: string
  end_date: string
  starting_capital: number
}

export type BacktestFormValidation =
  | { ok: true; value: ValidatedBacktestForm }
  | { ok: false; error: string }

export function validateBacktestForm(
  values: BacktestFormValues,
): BacktestFormValidation {
  const symbol = values.symbol.trim()
  if (symbol === '') {
    return { ok: false, error: 'Symbol is required.' }
  }

  const shortWindow = Number(values.shortWindow)
  if (!Number.isInteger(shortWindow) || shortWindow < MIN_SHORT_WINDOW) {
    return {
      ok: false,
      error: `Short window must be a whole number of at least ${MIN_SHORT_WINDOW}.`,
    }
  }

  const longWindow = Number(values.longWindow)
  if (!Number.isInteger(longWindow) || longWindow <= shortWindow) {
    return {
      ok: false,
      error: 'Long window must be a whole number greater than the short window.',
    }
  }
  if (longWindow > MAX_LONG_WINDOW) {
    return {
      ok: false,
      error: `Long window must be at most ${MAX_LONG_WINDOW}.`,
    }
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

  return {
    ok: true,
    value: {
      symbol: symbol.toUpperCase(),
      short_window: shortWindow,
      long_window: longWindow,
      start_date: values.startDate,
      end_date: values.endDate,
      starting_capital: startingCapital,
    },
  }
}
