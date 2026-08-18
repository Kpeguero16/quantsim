/**
 * Cash, total equity, total unrealized P/L -- plus a muted note when one or
 * more positions are priced at cost because market-data couldn't be
 * reached (SPEC.md 2.5). The backend already values degraded positions at
 * cost; this component only surfaces that it happened, it never
 * recomputes the numbers itself.
 */
import { formatPrice } from '../format'
import LoadingSkeleton from './LoadingSkeleton'
import { hasUnpricedPosition } from './position-display'
import type { PortfolioState } from './use-portfolio'

interface StatProps {
  label: string
  value: string
  valueClassName?: string
}

function Stat({ label, value, valueClassName = 'text-ink' }: StatProps) {
  return (
    <div>
      <p className="text-sm text-ink-muted">{label}</p>
      <p className={`tabular mt-1 font-mono text-2xl ${valueClassName}`}>
        {value}
      </p>
    </div>
  )
}

interface PortfolioSummaryProps {
  state: PortfolioState
}

export default function PortfolioSummary({ state }: PortfolioSummaryProps) {
  if (state.status === 'loading') {
    return (
      <LoadingSkeleton
        count={3}
        label="Loading portfolio summary"
        barClassName="h-12"
        containerClassName="grid grid-cols-1 gap-6 p-4 sm:grid-cols-3"
      />
    )
  }

  if (state.status === 'error') {
    return (
      <p role="alert" className="p-4 text-sm text-danger">
        {state.message}
      </p>
    )
  }

  const { portfolio } = state
  const unpricedCount = portfolio.positions.filter(
    (position) => position.latest_price === null,
  ).length

  const plClassName =
    portfolio.total_unrealized_pl > 0
      ? 'text-up'
      : portfolio.total_unrealized_pl < 0
        ? 'text-down'
        : 'text-ink'

  return (
    <div className="p-4">
      <div className="grid grid-cols-1 gap-6 sm:grid-cols-3">
        <Stat label="Cash" value={formatPrice(portfolio.balance)} />
        <Stat
          label="Total Equity"
          value={formatPrice(portfolio.total_equity)}
        />
        <Stat
          label="Total Unrealized P/L"
          value={formatPrice(portfolio.total_unrealized_pl)}
          valueClassName={plClassName}
        />
      </div>

      {hasUnpricedPosition(portfolio.positions) && (
        <p className="mt-4 text-sm text-ink-subtle">
          {unpricedCount} position{unpricedCount === 1 ? '' : 's'} valued at
          cost — live price unavailable
        </p>
      )}
    </div>
  )
}
