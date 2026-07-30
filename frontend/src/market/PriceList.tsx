/**
 * The watchlist, one row per symbol. Selecting a row is how Task 5's chart
 * knows what to render.
 *
 * `404 price_not_cached` renders as an em-dash rather than an error (SPEC.md
 * 2.7) -- a closed market or a fresh poller are routine, not failures, and
 * treating them as errors would make the dashboard look broken every
 * evening and all weekend.
 */
import type { PriceState } from './use-prices'

function formatPrice(price: number): string {
  return price.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })
}

interface RowValueProps {
  state: PriceState
}

function RowValue({ state }: RowValueProps) {
  if (state.status === 'loading') {
    return (
      <span
        className="inline-block h-4 w-16 animate-pulse rounded bg-elevated"
        aria-hidden="true"
      />
    )
  }

  if (state.status === 'not-cached') {
    return (
      <span className="tabular font-mono text-ink-subtle">
        &mdash;
        <span className="sr-only-text"> no price cached yet</span>
      </span>
    )
  }

  if (state.status === 'error') {
    return (
      <span className="text-sm text-danger" title={state.message}>
        error
      </span>
    )
  }

  const direction =
    state.previous === undefined
      ? 'flat'
      : state.price > state.previous
        ? 'up'
        : state.price < state.previous
          ? 'down'
          : 'flat'

  const directionColor =
    direction === 'up'
      ? 'text-up'
      : direction === 'down'
        ? 'text-down'
        : 'text-ink'

  const arrow = direction === 'up' ? '▲' : direction === 'down' ? '▼' : ''

  return (
    <span className={`tabular font-mono ${directionColor}`}>
      {arrow && (
        <span aria-hidden="true" className="mr-1">
          {arrow}
        </span>
      )}
      ${formatPrice(state.price)}
      {/* An icon and a number are not enough alone for a screen reader
          user to know a price just moved -- spell out the direction too. */}
      <span className="sr-only-text">
        {direction === 'up' && ', up'}
        {direction === 'down' && ', down'}
      </span>
    </span>
  )
}

interface PriceListProps {
  symbols: string[]
  prices: Record<string, PriceState>
  selected: string | null
  onSelect: (symbol: string) => void
}

export default function PriceList({
  symbols,
  prices,
  selected,
  onSelect,
}: PriceListProps) {
  return (
    <ul className="divide-y divide-line">
      {symbols.map((symbol) => {
        const isSelected = symbol === selected
        return (
          <li key={symbol}>
            <button
              type="button"
              onClick={() => onSelect(symbol)}
              aria-current={isSelected ? 'true' : undefined}
              className={`flex w-full items-center justify-between gap-4 px-4 py-3 text-left transition-colors hover:bg-elevated ${
                isSelected ? 'bg-elevated' : ''
              }`}
            >
              <span className="font-medium text-ink">{symbol}</span>
              <RowValue state={prices[symbol] ?? { status: 'loading' }} />
            </button>
          </li>
        )
      })}
    </ul>
  )
}
