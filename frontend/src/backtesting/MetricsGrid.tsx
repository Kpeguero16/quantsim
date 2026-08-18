/**
 * The five metrics agents.md defines for one backtest run, reusing
 * PortfolioSummary.tsx's Stat pattern. profit_factor is rendered as "—"
 * when null -- no losing trades to divide by, not a real zero (SPEC.md
 * 2.7, the same null-over-a-plausible-number rule Step 15 already applies
 * to latest_price).
 *
 * total_return_pct and sharpe_ratio are sign-colored the same way
 * total_unrealized_pl already is. max_drawdown_pct is never up-colored --
 * the backend always returns it as a positive percentage
 * (services/backtesting/internal/service/metrics.go), so there is no
 * positive-drawdown case for "up" to mean anything for.
 */
import type { Metrics } from '../api/types'

interface StatProps {
  label: string
  value: string
  valueClassName?: string
  note?: string
}

function Stat({ label, value, valueClassName = 'text-ink', note }: StatProps) {
  return (
    <div>
      <p className="text-sm text-ink-muted">{label}</p>
      <p className={`tabular mt-1 font-mono text-2xl ${valueClassName}`}>
        {value}
      </p>
      {note && <p className="mt-1 text-xs text-ink-subtle">{note}</p>}
    </div>
  )
}

function signClassName(value: number): string {
  if (value > 0) return 'text-up'
  if (value < 0) return 'text-down'
  return 'text-ink'
}

function formatPct(value: number): string {
  return `${value.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%`
}

function formatRatio(value: number): string {
  return value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })
}

interface MetricsGridProps {
  metrics: Metrics
}

export default function MetricsGrid({ metrics }: MetricsGridProps) {
  return (
    <div className="grid grid-cols-2 gap-6 sm:grid-cols-5">
      <Stat
        label="Total Return"
        value={formatPct(metrics.total_return_pct)}
        valueClassName={signClassName(metrics.total_return_pct)}
      />
      <Stat
        label="Sharpe Ratio"
        value={formatRatio(metrics.sharpe_ratio)}
        valueClassName={signClassName(metrics.sharpe_ratio)}
      />
      {/* Neutral, not text-down: the backend always returns this as a
          non-negative percentage, so a 0% drawdown is a genuinely good
          result -- coloring it "down" the way a negative return is colored
          would misread a clean run as a bad one. */}
      <Stat label="Max Drawdown" value={formatPct(metrics.max_drawdown_pct)} />
      <Stat label="Win Rate" value={formatPct(metrics.win_rate_pct)} />
      <Stat
        label="Profit Factor"
        value={
          metrics.profit_factor !== null
            ? formatRatio(metrics.profit_factor)
            : '—'
        }
        note={metrics.profit_factor === null ? 'no losing trades' : undefined}
      />
    </div>
  )
}
