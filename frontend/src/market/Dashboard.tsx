/**
 * The logged-in screen: watchlist with live prices, one symbol selected at
 * a time. Task 5 fills the empty detail panel with the candlestick chart
 * for whichever symbol is selected here.
 */
import { useEffect, useState } from 'react'

import { useAuth } from '../auth/context'
import PriceList from './PriceList'
import { usePrices } from './use-prices'
import { useSymbols } from './use-symbols'

// A stable empty-array reference. `symbolsState.symbols` is itself stable
// once loaded (it's the same array object held in state), but a fresh `[]`
// literal here would be a *new* reference on every render while loading --
// which would retrigger usePrices's effect and the selection effect below
// on every unrelated re-render (e.g. each 15s price tick).
const NO_SYMBOLS: string[] = []

export default function Dashboard() {
  const { user, logout } = useAuth()
  const symbolsState = useSymbols()
  const symbols =
    symbolsState.status === 'ok' ? symbolsState.symbols : NO_SYMBOLS
  const prices = usePrices(symbols)

  const [selected, setSelected] = useState<string | null>(null)

  // Default to the first symbol once the watchlist arrives, without
  // overriding a selection the user already made.
  useEffect(() => {
    if (selected === null && symbols.length > 0) setSelected(symbols[0])
  }, [selected, symbols])

  return (
    <div className="min-h-dvh">
      <header className="flex items-center justify-between border-b border-line px-4 py-3 sm:px-6">
        <div className="flex items-center gap-2.5">
          <BrandMark />
          <span className="text-base font-semibold tracking-tight text-ink">
            QuantSim
          </span>
        </div>
        <div className="flex items-center gap-3">
          <span className="hidden text-sm text-ink-muted sm:inline">
            {user?.username}
          </span>
          <button
            type="button"
            onClick={logout}
            className="rounded-md border border-line-strong px-3 py-1.5 text-sm text-ink transition-colors hover:bg-elevated"
          >
            Sign out
          </button>
        </div>
      </header>

      <main className="grid grid-cols-1 gap-4 p-4 sm:p-6 lg:grid-cols-[320px_1fr]">
        <section
          aria-label="Watchlist"
          className="rounded-lg border border-line bg-surface"
        >
          {symbolsState.status === 'loading' && (
            <div
              className="space-y-3 p-4"
              aria-busy="true"
              aria-label="Loading watchlist"
            >
              {Array.from({ length: 5 }).map((_, i) => (
                <div
                  // eslint-disable-next-line react/no-array-index-key -- a
                  // fixed-count skeleton list has no stable identity to key by
                  key={i}
                  className="h-9 animate-pulse rounded bg-elevated"
                />
              ))}
            </div>
          )}

          {symbolsState.status === 'error' && (
            <p role="alert" className="p-4 text-sm text-danger">
              {symbolsState.message}
            </p>
          )}

          {symbolsState.status === 'ok' && (
            <PriceList
              symbols={symbols}
              prices={prices}
              selected={selected}
              onSelect={setSelected}
            />
          )}
        </section>

        <section
          aria-label={selected ? `${selected} chart` : 'Chart'}
          className="rounded-lg border border-line bg-surface p-4"
        >
          {selected ? (
            <p className="text-sm text-ink-muted">
              Chart for <span className="font-medium text-ink">{selected}</span> —
              coming in Task 5.
            </p>
          ) : (
            <p className="text-sm text-ink-muted">
              Select a symbol to see its chart.
            </p>
          )}
        </section>
      </main>
    </div>
  )
}

function BrandMark() {
  return (
    <svg
      viewBox="0 0 32 32"
      className="h-6 w-6"
      role="img"
      aria-label="QuantSim"
    >
      <rect width="32" height="32" rx="6" className="fill-elevated" />
      <path
        d="M5 22.5 L12 15 L17 19 L27 8"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.75"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="text-up"
      />
      <circle cx="27" cy="8" r="2.75" className="fill-up" />
    </svg>
  )
}
