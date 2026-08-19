/**
 * One backtest's full detail: the parameters it ran with, its five
 * metrics, and its simulated trade log -- whichever BacktestDetail is
 * currently selected, either fresh off POST /backtests or reopened from
 * history (SPEC.md 2.2, 2.3).
 */
import type { BacktestDetail } from '../api/types'
import { formatDate, formatPrice } from '../format'
import MetricsGrid from './MetricsGrid'
import { describeStrategy } from './strategy-display'
import TradeLogTable from './TradeLogTable'

interface BacktestResultProps {
  backtest: BacktestDetail
}

export default function BacktestResult({ backtest }: BacktestResultProps) {
  return (
    <div className="space-y-6">
      <div>
        <h3 className="text-sm font-semibold text-ink">
          {backtest.symbol} — {describeStrategy(backtest)}
        </h3>
        <p className="mt-1 text-sm text-ink-muted">
          {formatDate(backtest.start_date)} – {formatDate(backtest.end_date)}
          {' · '}
          Starting capital ${formatPrice(backtest.starting_capital)}
          {' · '}
          Final equity ${formatPrice(backtest.final_equity)}
        </p>
      </div>

      <MetricsGrid metrics={backtest.metrics} />

      <div>
        <h4 className="mb-2 text-sm font-semibold text-ink">Trade Log</h4>
        <TradeLogTable trades={backtest.trades} />
      </div>
    </div>
  )
}
