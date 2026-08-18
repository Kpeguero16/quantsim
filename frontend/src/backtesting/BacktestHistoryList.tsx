/**
 * The caller's past runs, newest-first (already ordered that way by
 * GET /backtests -- SPEC.md 2.9, no client-side re-sort or pagination).
 * Each row is a button that loads that run into the result view; a click
 * on the already-selected row is a no-op rather than a redundant refetch.
 */
import type { Backtest } from '../api/types'
import { formatDate } from '../format'
import type { BacktestsState } from './use-backtests'

function signClassName(value: number): string {
  if (value > 0) return 'text-up'
  if (value < 0) return 'text-down'
  return 'text-ink'
}

function formatPct(value: number): string {
  return `${value.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%`
}

interface BacktestHistoryListProps {
  state: BacktestsState
  selectedId: string | null
  onSelect: (backtest: Backtest) => void
}

export default function BacktestHistoryList({
  state,
  selectedId,
  onSelect,
}: BacktestHistoryListProps) {
  if (state.status === 'loading') {
    return (
      <div className="space-y-2 p-4" aria-busy="true" aria-label="Loading run history">
        {Array.from({ length: 3 }).map((_, i) => (
          // eslint-disable-next-line react/no-array-index-key -- a
          // fixed-count skeleton list has no stable identity to key by
          <div key={i} className="h-12 animate-pulse rounded bg-elevated" />
        ))}
      </div>
    )
  }

  if (state.status === 'error') {
    return (
      <p role="alert" className="p-4 text-sm text-danger">
        {state.message}
      </p>
    )
  }

  if (state.backtests.length === 0) {
    return <p className="p-4 text-sm text-ink-muted">No backtests run yet.</p>
  }

  return (
    <ul className="divide-y divide-line">
      {state.backtests.map((backtest) => (
        <li key={backtest.id}>
          <button
            type="button"
            onClick={() => onSelect(backtest)}
            aria-current={backtest.id === selectedId}
            className={`w-full px-4 py-3 text-left transition-colors hover:bg-elevated ${
              backtest.id === selectedId ? 'bg-elevated' : ''
            }`}
          >
            <div className="flex items-center justify-between">
              <span className="font-medium text-ink">{backtest.symbol}</span>
              <span
                className={`tabular font-mono text-sm ${signClassName(backtest.metrics.total_return_pct)}`}
              >
                {formatPct(backtest.metrics.total_return_pct)}
              </span>
            </div>
            <p className="mt-1 text-xs text-ink-muted">
              {backtest.short_window}/{backtest.long_window} ·{' '}
              {formatDate(backtest.start_date)} – {formatDate(backtest.end_date)}
            </p>
          </button>
        </li>
      ))}
    </ul>
  )
}
