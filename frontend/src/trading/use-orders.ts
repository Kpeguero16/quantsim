/**
 * Full order history. Fetched once on mount, not polled -- nothing outside
 * this session mutates these orders, so there's no clock-driven reason to
 * poll (SPEC.md 2.3). `refetch()` is called explicitly right after a
 * successful order (SPEC.md 2.4).
 *
 * Same request-id race guard as use-portfolio.ts: a refetch can overlap
 * with... nothing else here, since there's no interval, but the guard is
 * kept so a slow initial fetch racing a fast manual refetch (e.g. double
 * order placement) can't have its late response overwrite the newer one.
 */
import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, api } from '../api/client'
import type { Order } from '../api/types'

export type OrdersState =
  | { status: 'loading' }
  | { status: 'ok'; orders: Order[] }
  | { status: 'error'; message: string }

export function useOrders(): OrdersState & { refetch: () => void } {
  const [state, setState] = useState<OrdersState>({ status: 'loading' })

  const stateRef = useRef(state)
  stateRef.current = state

  const requestIdRef = useRef(0)

  const load = useCallback(() => {
    const requestId = ++requestIdRef.current

    if (stateRef.current.status !== 'ok') setState({ status: 'loading' })

    api
      .orders()
      .then((response) => {
        if (requestIdRef.current !== requestId) return
        setState({ status: 'ok', orders: response.orders })
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
    // Non-blocking oxlint warning here, same as use-portfolio.ts: the
    // exhaustive-deps rule's `.current`-in-cleanup check is aimed at DOM
    // node refs, not a plain instance counter like this one.
    return () => {
      requestIdRef.current++
    }
  }, [load])

  return { ...state, refetch: load }
}
