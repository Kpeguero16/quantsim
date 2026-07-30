/**
 * Polls the latest cached price for a set of symbols.
 *
 * 15 seconds, matched to the market-data service's own poller (SPEC.md
 * 2.2). Polling faster only burns requests against a Redis key that has not
 * changed.
 *
 * A 404 price_not_cached is modelled as its own state rather than an error.
 * It is routine -- markets closed, the poller not yet run, or a symbol
 * missing from the last Alpaca snapshot -- and treating it as a failure
 * would make the dashboard look broken every evening and all weekend
 * (SPEC.md 2.7).
 */
import { useEffect, useState } from 'react'

import { ApiError, api, isPriceNotCached } from '../api/client'

export type PriceState =
  | { status: 'loading' }
  | { status: 'ok'; price: number; previous?: number; timestamp: string }
  | { status: 'not-cached' }
  | { status: 'error'; message: string }

export const POLL_INTERVAL_MS = 15_000

export function usePrices(
  symbols: string[],
  intervalMs: number = POLL_INTERVAL_MS,
): Record<string, PriceState> {
  const [prices, setPrices] = useState<Record<string, PriceState>>({})

  // Symbols arrive once from /market-data/symbols and never change, but
  // depending on the array identity would restart the interval on every
  // render if that ever stopped being true.
  const key = symbols.join(',')

  useEffect(() => {
    if (symbols.length === 0) return

    let cancelled = false

    const readOne = async (symbol: string): Promise<[string, PriceState]> => {
      try {
        const price = await api.price(symbol)
        return [
          symbol,
          {
            status: 'ok',
            price: price.price,
            timestamp: price.timestamp,
          },
        ]
      } catch (error) {
        if (isPriceNotCached(error)) return [symbol, { status: 'not-cached' }]
        return [
          symbol,
          {
            status: 'error',
            message:
              error instanceof ApiError ? error.message : 'Could not load',
          },
        ]
      }
    }

    const tick = async () => {
      const results = await Promise.all(symbols.map(readOne))
      if (cancelled) return

      setPrices((current) => {
        const next: Record<string, PriceState> = { ...current }
        for (const [symbol, state] of results) {
          const before = current[symbol]
          next[symbol] =
            state.status === 'ok' && before?.status === 'ok'
              ? // Carry the last known price forward so the row can show
                // which way it just moved.
                { ...state, previous: before.price }
              : state
        }
        return next
      })
    }

    void tick()
    const timer = setInterval(() => void tick(), intervalMs)

    // Without this the interval keeps firing after sign-out, sending
    // authenticated requests with a cleared token.
    return () => {
      cancelled = true
      clearInterval(timer)
    }
    // Non-blocking oxlint warning here (exhaustive-deps wants `symbols`
    // itself): `key` is the intentional, stable identity of `symbols` --
    // depending on the array would restart the poll on every render. A
    // disable comment did not suppress it under oxlint regardless of which
    // line it was attached to; not chasing further since it does not fail
    // `npm run lint` (exit 0).
  }, [key, intervalMs])

  return prices
}
