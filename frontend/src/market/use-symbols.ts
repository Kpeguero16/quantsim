/**
 * The watchlist symbol list. Fetched once on mount -- it's a fixed,
 * curated list on the backend (SPEC.md references the 7-symbol watchlist),
 * not something that changes during a session.
 */
import { useEffect, useState } from 'react'

import { ApiError, api } from '../api/client'

export type SymbolsState =
  | { status: 'loading' }
  | { status: 'ok'; symbols: string[] }
  | { status: 'error'; message: string }

export function useSymbols(): SymbolsState {
  const [state, setState] = useState<SymbolsState>({ status: 'loading' })

  useEffect(() => {
    let cancelled = false

    api
      .symbols()
      .then((response) => {
        if (!cancelled) setState({ status: 'ok', symbols: response.symbols })
      })
      .catch((error: unknown) => {
        if (cancelled) return
        setState({
          status: 'error',
          message:
            error instanceof ApiError ? error.message : 'Could not load',
        })
      })

    return () => {
      cancelled = true
    }
  }, [])

  return state
}
