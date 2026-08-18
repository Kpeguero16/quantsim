/**
 * Full backtest run history. Fetched once on mount, not polled -- nothing
 * outside this session creates a backtest for this account, so there's no
 * clock-driven reason to poll (SPEC.md 2.3, mirroring use-orders.ts's
 * identical reasoning for order history). `refetch()` is called explicitly
 * right after a successful run (SPEC.md 2.4).
 *
 * Same request-id race guard as use-orders.ts/use-portfolio.ts.
 */
import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, api } from '../api/client'
import type { Backtest } from '../api/types'

export type BacktestsState =
  | { status: 'loading' }
  | { status: 'ok'; backtests: Backtest[] }
  | { status: 'error'; message: string }

export function useBacktests(): BacktestsState & { refetch: () => void } {
  const [state, setState] = useState<BacktestsState>({ status: 'loading' })

  const stateRef = useRef(state)
  stateRef.current = state

  const requestIdRef = useRef(0)

  const load = useCallback(() => {
    const requestId = ++requestIdRef.current

    if (stateRef.current.status !== 'ok') setState({ status: 'loading' })

    api
      .backtests()
      .then((response) => {
        if (requestIdRef.current !== requestId) return
        setState({ status: 'ok', backtests: response.backtests })
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
    return () => {
      requestIdRef.current++
    }
  }, [load])

  return { ...state, refetch: load }
}
