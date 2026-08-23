/**
 * The Step 20 report. Fetched once on mount, not polled.
 *
 * No interval, and for a stronger reason than use-backtests.ts's: every
 * figure in this report is computed as of the last COMPLETED trading day
 * from stored daily closes, so an intraday price tick cannot move a single
 * one of them (SPEC.md 2.2). The only thing that changes this report is a
 * fill, which is what `refreshKey` is for.
 *
 * `refreshKey` is a counter Dashboard bumps on every order it places. It
 * joins the mount effect's dependencies rather than adding a second one,
 * so mount fetches once and each fill fetches once -- there is no path
 * that fetches twice.
 *
 * useNarrative deliberately has NO equivalent, and the asymmetry is the
 * whole of SPEC.md 2.9: this request is free and recomputing it after a
 * fill is simply correct, while that one is billed per call against a
 * cache keyed on the report's content hash, so the same wiring there would
 * make every fill buy prose nobody asked to re-read.
 *
 * Same request-id race guard as use-orders.ts/use-portfolio.ts.
 */
import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, api } from '../api/client'
import type { PortfolioInsightsResponse } from '../api/types'
import { describeInsightsError } from './insights-errors'

export type InsightsState =
  | { status: 'loading' }
  | { status: 'ok'; insights: PortfolioInsightsResponse }
  /**
   * `message` is null when there is nothing to say -- invalid_token, where
   * the session is already being cleared and an error about portfolio
   * analytics would explain the wrong thing (SPEC.md 2.7). Null is not the
   * absence of a message here, it is the decision not to show one.
   */
  | { status: 'error'; message: string | null }

export function useInsights(refreshKey = 0): InsightsState {
  const [state, setState] = useState<InsightsState>({ status: 'loading' })

  const stateRef = useRef(state)
  stateRef.current = state

  const requestIdRef = useRef(0)

  const load = useCallback(() => {
    const requestId = ++requestIdRef.current

    if (stateRef.current.status !== 'ok') setState({ status: 'loading' })

    api
      .insights()
      .then((insights) => {
        if (requestIdRef.current !== requestId) return
        setState({ status: 'ok', insights })
      })
      .catch((error: unknown) => {
        if (requestIdRef.current !== requestId) return
        setState({
          status: 'error',
          message:
            error instanceof ApiError
              ? describeInsightsError(error.code, error.message)
              : 'Could not load your analysis.',
        })
      })
  }, [])

  useEffect(() => {
    load()
    return () => {
      // Past any in-flight request's id, so its resolution is discarded.
      // Non-blocking oxlint warning here (react-hooks/exhaustive-deps flags
      // writing `.current` in a cleanup): the rule is aimed at DOM node
      // refs, not a plain instance counter. Same precedent as
      // use-portfolio.ts, use-orders.ts and use-backtests.ts.
      requestIdRef.current++
    }
  }, [load, refreshKey])

  return state
}
