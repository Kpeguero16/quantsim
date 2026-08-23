/**
 * Human-readable copy for the error codes writeServiceError
 * (services/ai-insights/internal/handler/insights.go) can return from
 * GET /insights/portfolio, plus the two transport pseudo-codes api/client.ts
 * raises when the request never reached a server (SPEC.md 2.7).
 */

const STATIC_DESCRIPTIONS: Record<string, string> = {
  upstream_unavailable:
    'Portfolio or market data is unavailable right now, so no analysis was computed.',
  internal_error: 'Something went wrong computing your analysis.',
}

/**
 * Copy for a failed report request, or null when there is nothing to say.
 *
 * `invalid_token` returns null rather than a sentence. By the time a 401
 * survives api/client.ts's refresh-and-retry, the refresh token is gone
 * too and AuthBridge.onRefreshFailed has already cleared the session -- so
 * the user is being returned to the sign-in screen, and an error about
 * portfolio analytics on the way out would explain the wrong thing.
 *
 * `symbol_unavailable` passes the backend's own message through. It names
 * the symbol and the date and was written to be read (Step 20 SPEC 2.10),
 * which is more useful than replacing it with a sentence that names
 * neither. Same judgement backtest-errors.ts made for invalid_request, and
 * for the same reason: the message quality happens to be good here.
 *
 * Anything unrecognized -- a code added in a later step, or the timeout and
 * network_error this client raises itself -- also falls back to the
 * message. Every QuantSim service returns {code, message} with a sentence
 * in it, and the transport messages are written for a reader already.
 */
export function describeInsightsError(
  code: string,
  message: string,
): string | null {
  if (code === 'invalid_token') return null
  return Object.hasOwn(STATIC_DESCRIPTIONS, code)
    ? STATIC_DESCRIPTIONS[code]
    : message
}
