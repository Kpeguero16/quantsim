/**
 * Human-readable copy for the error codes writeServiceError
 * (services/backtesting/internal/handler/backtest.go) can return from any
 * of the three /backtests endpoints.
 *
 * `invalid_request` is deliberately not in the static map, unlike trading's
 * identically-named code (rejection-reason.ts): backtesting's
 * validateRequest already produces a specific, user-safe message per field
 * ("long_window must be at most 500"), so it's passed through rather than
 * replaced with one generic sentence (SPEC.md 2.6).
 */
const STATIC_DESCRIPTIONS: Record<string, string> = {
  symbol_unavailable: 'No historical data is available for that symbol.',
  date_range_unavailable:
    'No historical data is available in the requested date range.',
  upstream_unavailable:
    'Market data is unavailable right now. The backtest was not run.',
  not_found: 'That backtest could not be found.',
}

/**
 * Never throws, never renders blank. `invalid_request` and any code this
 * module doesn't recognize both fall back to the backend's own message --
 * for invalid_request that's the point (see module comment); for a truly
 * unknown code it's still more informative than the bare code string.
 */
export function describeBacktestError(code: string, message: string): string {
  if (code === 'invalid_request') return message
  return Object.hasOwn(STATIC_DESCRIPTIONS, code)
    ? STATIC_DESCRIPTIONS[code]
    : message
}
