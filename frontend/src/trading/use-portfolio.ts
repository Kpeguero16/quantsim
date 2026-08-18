/**
 * Polls the whole portfolio -- balance, positions, equity, unrealized P/L --
 * in one call. No separate positions poll: PositionsTable and
 * PortfolioSummary both read off this same state (SPEC.md 2.3).
 *
 * `refetch()` lets a just-placed order update the UI immediately rather
 * than waiting for the next 15s tick (SPEC.md 2.4). Because a poll tick and
 * a refetch can now overlap, every response is tagged with a request id and
 * a stale one -- resolved after a newer request already landed -- is
 * discarded, the same intent as usePrices's `cancelled` flag but strong
 * enough to survive two in-flight requests at once instead of just one.
 */
import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, api } from '../api/client'
import type { PortfolioResponse } from '../api/types'
import { POLL_INTERVAL_MS } from '../market/use-prices'

export type PortfolioState =
  | { status: 'loading' }
  | { status: 'ok'; portfolio: PortfolioResponse }
  | { status: 'error'; message: string }

export function usePortfolio(
  intervalMs: number = POLL_INTERVAL_MS,
): PortfolioState & { refetch: () => void } {
  const [state, setState] = useState<PortfolioState>({ status: 'loading' })

  const stateRef = useRef(state)
  stateRef.current = state

  // Bumped on every request start (and on unmount) so a response can tell
  // whether it's still the most recent one in flight.
  const requestIdRef = useRef(0)

  const load = useCallback(() => {
    const requestId = ++requestIdRef.current

    // Only flash back to 'loading' when nothing good is held yet -- a
    // refetch after a known-good value should not blank the UI.
    if (stateRef.current.status !== 'ok') setState({ status: 'loading' })

    api
      .portfolio()
      .then((portfolio) => {
        if (requestIdRef.current !== requestId) return
        setState({ status: 'ok', portfolio })
      })
      .catch((error: unknown) => {
        if (requestIdRef.current !== requestId) return
        setState({
          status: 'error',
          message:
            error instanceof ApiError ? error.message : 'Could not load',
        })
      })
  }, [])

  useEffect(() => {
    load()
    const timer = setInterval(load, intervalMs)

    return () => {
      // Past any in-flight request's id, so its resolution is discarded --
      // stops it setting state after unmount or after sign-out.
      //
      // Non-blocking oxlint warning here (react-hooks/exhaustive-deps
      // flags writing `.current` in a cleanup): the rule is aimed at DOM
      // node refs, not a plain instance counter like this one. Same
      // "does not fail npm run lint" precedent as use-prices.ts.
      requestIdRef.current++
      clearInterval(timer)
    }
  }, [load, intervalMs])

  return { ...state, refetch: load }
}
