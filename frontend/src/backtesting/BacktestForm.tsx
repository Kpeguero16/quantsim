/**
 * Configures and runs one backtest. Client-side validation
 * (backtest-validation.ts) mirrors the backend's bounds and never calls
 * the API on failure -- same boundary OrderTicket.tsx already enforces for
 * orders (SPEC.md 2.5).
 *
 * On success the response body is handed to the caller directly rather
 * than re-fetched -- POST /backtests already returns the full
 * BacktestDetail, trade log included (SPEC.md 2.3) -- and the form's
 * values are left as-is rather than cleared, since tweaking one window and
 * re-running is a normal part of using this form, unlike OrderTicket where
 * a filled quantity has no reason to persist.
 */
import { useState, type FormEvent } from 'react'

import { ApiError, api } from '../api/client'
import type { BacktestDetail } from '../api/types'
import { describeBacktestError } from './backtest-errors'
import {
  validateBacktestForm,
  type BacktestFormValues,
} from './backtest-validation'

const INITIAL_VALUES: BacktestFormValues = {
  symbol: '',
  shortWindow: '5',
  longWindow: '20',
  startDate: '',
  endDate: '',
  startingCapital: '10000',
}

interface BacktestFormProps {
  onResult: (detail: BacktestDetail) => void
  onRunSaved: () => void
}

export default function BacktestForm({
  onResult,
  onRunSaved,
}: BacktestFormProps) {
  const [values, setValues] = useState<BacktestFormValues>(INITIAL_VALUES)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function setField<K extends keyof BacktestFormValues>(
    field: K,
    value: BacktestFormValues[K],
  ) {
    setValues((prev) => ({ ...prev, [field]: value }))
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault()
    // Synchronous with the event, same reasoning as OrderTicket.tsx: the
    // `disabled` prop only takes effect once React commits `submitting`,
    // which a fast double-submit can race.
    if (submitting) return

    const validation = validateBacktestForm(values)
    if (!validation.ok) {
      setError(validation.error)
      return
    }

    setError(null)
    setSubmitting(true)
    try {
      const detail = await api.runBacktest(validation.value)
      onResult(detail)
      onRunSaved()
    } catch (thrown) {
      if (thrown instanceof ApiError) {
        setError(describeBacktestError(thrown.code, thrown.message))
      } else {
        setError('Could not run the backtest.')
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => void handleSubmit(e)}
      // Same reasoning as OrderTicket.tsx: the number/date inputs' own
      // min/max/required constraints would otherwise block submission
      // silently via native validation, leaving a stale error on screen
      // instead of running handleSubmit's own checks.
      noValidate
    >
      <div>
        <label htmlFor="backtest-symbol" className="block text-sm text-ink-muted">
          Symbol
        </label>
        <input
          id="backtest-symbol"
          type="text"
          value={values.symbol}
          onChange={(e) => setField('symbol', e.target.value)}
          className="mt-1 w-full rounded-md border border-line-strong bg-canvas px-3 py-2 uppercase text-ink"
          placeholder="AAPL"
        />
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div>
          <label
            htmlFor="backtest-short-window"
            className="block text-sm text-ink-muted"
          >
            Short window
          </label>
          <input
            id="backtest-short-window"
            type="number"
            inputMode="numeric"
            min={2}
            step={1}
            value={values.shortWindow}
            onChange={(e) => setField('shortWindow', e.target.value)}
            className="tabular mt-1 w-full rounded-md border border-line-strong bg-canvas px-3 py-2 font-mono text-ink"
          />
        </div>
        <div>
          <label
            htmlFor="backtest-long-window"
            className="block text-sm text-ink-muted"
          >
            Long window
          </label>
          <input
            id="backtest-long-window"
            type="number"
            inputMode="numeric"
            min={3}
            max={500}
            step={1}
            value={values.longWindow}
            onChange={(e) => setField('longWindow', e.target.value)}
            className="tabular mt-1 w-full rounded-md border border-line-strong bg-canvas px-3 py-2 font-mono text-ink"
          />
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div>
          <label
            htmlFor="backtest-start-date"
            className="block text-sm text-ink-muted"
          >
            Start date
          </label>
          <input
            id="backtest-start-date"
            type="date"
            value={values.startDate}
            onChange={(e) => setField('startDate', e.target.value)}
            className="mt-1 w-full rounded-md border border-line-strong bg-canvas px-3 py-2 text-ink"
          />
        </div>
        <div>
          <label
            htmlFor="backtest-end-date"
            className="block text-sm text-ink-muted"
          >
            End date
          </label>
          <input
            id="backtest-end-date"
            type="date"
            value={values.endDate}
            onChange={(e) => setField('endDate', e.target.value)}
            className="mt-1 w-full rounded-md border border-line-strong bg-canvas px-3 py-2 text-ink"
          />
        </div>
      </div>

      <div>
        <label
          htmlFor="backtest-starting-capital"
          className="block text-sm text-ink-muted"
        >
          Starting capital
        </label>
        <input
          id="backtest-starting-capital"
          type="number"
          inputMode="decimal"
          min={0.01}
          step="any"
          value={values.startingCapital}
          onChange={(e) => setField('startingCapital', e.target.value)}
          className="tabular mt-1 w-full rounded-md border border-line-strong bg-canvas px-3 py-2 font-mono text-ink"
        />
      </div>

      {error && (
        <p role="alert" className="text-sm text-danger">
          {error}
        </p>
      )}

      <button
        type="submit"
        disabled={submitting}
        className="w-full rounded-md bg-accent px-3 py-2 text-sm font-medium text-canvas transition-colors hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
      >
        {submitting ? 'Running…' : 'Run Backtest'}
      </button>
    </form>
  )
}
