/**
 * The 'backtest' tab's content: a form to configure and run a strategy, a
 * history list of past runs, and whichever result is currently selected --
 * either the response of the run just submitted, or an older run reopened
 * from history (SPEC.md 2.2, 2.3).
 *
 * Unlike Dashboard.tsx's other tabs, this one owns its own fetch for
 * GET /backtests/{id}: reopening an older run needs its trade log, which
 * the list endpoint (Backtest, no Trades field) never carries.
 */
import { useCallback, useState } from 'react'

import { ApiError, api } from '../api/client'
import type { Backtest, BacktestDetail } from '../api/types'
import BacktestForm from './BacktestForm'
import BacktestHistoryList from './BacktestHistoryList'
import BacktestResult from './BacktestResult'
import { useBacktests } from './use-backtests'

type ResultState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ok'; detail: BacktestDetail }
  | { status: 'error'; message: string }

export default function BacktestPanel() {
  const history = useBacktests()
  const [result, setResult] = useState<ResultState>({ status: 'idle' })

  const selectedId = result.status === 'ok' ? result.detail.id : null

  const openBacktest = useCallback(
    (backtest: Backtest) => {
      // Reopening the run already shown is a no-op, not a redundant fetch
      // (SPEC.md 2.9).
      if (backtest.id === selectedId) return

      setResult({ status: 'loading' })
      api
        .backtest(backtest.id)
        .then((detail) => setResult({ status: 'ok', detail }))
        .catch((error: unknown) => {
          setResult({
            status: 'error',
            message:
              error instanceof ApiError
                ? error.message
                : 'Could not load that backtest.',
          })
        })
    },
    [selectedId],
  )

  return (
    <div className="grid grid-cols-1 gap-4 p-4 lg:grid-cols-[1fr_280px]">
      <div className="space-y-4">
        <BacktestForm
          onResult={(detail) => setResult({ status: 'ok', detail })}
          onRunSaved={history.refetch}
        />

        {result.status === 'loading' && (
          <p className="text-sm text-ink-muted">Loading backtest…</p>
        )}
        {result.status === 'error' && (
          <p role="alert" className="text-sm text-danger">
            {result.message}
          </p>
        )}
        {result.status === 'ok' && (
          <BacktestResult backtest={result.detail} />
        )}
      </div>

      <div className="rounded-lg border border-line bg-surface">
        <h3 className="border-b border-line px-4 py-3 text-sm font-semibold text-ink">
          Run History
        </h3>
        <BacktestHistoryList
          state={history}
          selectedId={selectedId}
          onSelect={openBacktest}
        />
      </div>
    </div>
  )
}
