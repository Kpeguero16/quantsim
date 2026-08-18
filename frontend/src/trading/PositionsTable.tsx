/**
 * Open positions, read off the shared portfolio poll -- no independent
 * fetch here, `PortfolioResponse.positions` is the only source (SPEC.md
 * 2.3). Loading/error/ok states pass straight through from Dashboard.tsx.
 */
import type { Position } from '../api/types'
import { formatPrice, formatQuantity } from '../format'
import LoadingSkeleton from './LoadingSkeleton'
import { derivePositionDisplay } from './position-display'
import type { PortfolioState } from './use-portfolio'

const DIRECTION_CLASS: Record<
  ReturnType<typeof derivePositionDisplay>['plDirection'],
  string
> = {
  up: 'text-up',
  down: 'text-down',
  flat: 'text-ink',
  unknown: 'text-ink-subtle',
}

function PositionRow({ position }: { position: Position }) {
  const display = derivePositionDisplay(position)

  return (
    <tr className="border-b border-line last:border-b-0">
      <td className="px-4 py-3 font-medium text-ink">{position.symbol}</td>
      <td className="tabular px-4 py-3 text-right font-mono text-ink">
        {formatQuantity(position.quantity)}
      </td>
      <td className="tabular px-4 py-3 text-right font-mono text-ink">
        {formatPrice(position.avg_cost)}
      </td>
      <td className="tabular px-4 py-3 text-right font-mono text-ink">
        {display.priceLabel}
      </td>
      <td
        className={`tabular px-4 py-3 text-right font-mono ${DIRECTION_CLASS[display.plDirection]}`}
      >
        {display.plLabel}
      </td>
    </tr>
  )
}

interface PositionsTableProps {
  state: PortfolioState
}

export default function PositionsTable({ state }: PositionsTableProps) {
  if (state.status === 'loading') {
    return <LoadingSkeleton count={3} label="Loading positions" />
  }

  if (state.status === 'error') {
    return (
      <p role="alert" className="p-4 text-sm text-danger">
        {state.message}
      </p>
    )
  }

  const { positions } = state.portfolio

  if (positions.length === 0) {
    return <p className="p-4 text-sm text-ink-muted">No open positions.</p>
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-line-strong text-left text-ink-muted">
            <th className="px-4 py-2 font-medium">Symbol</th>
            <th className="px-4 py-2 text-right font-medium">Quantity</th>
            <th className="px-4 py-2 text-right font-medium">Avg Cost</th>
            <th className="px-4 py-2 text-right font-medium">Latest Price</th>
            <th className="px-4 py-2 text-right font-medium">Unrealized P/L</th>
          </tr>
        </thead>
        <tbody>
          {positions.map((position) => (
            <PositionRow key={position.symbol} position={position} />
          ))}
        </tbody>
      </table>
    </div>
  )
}
