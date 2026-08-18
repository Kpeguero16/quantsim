/**
 * A backtest's simulated trade log (BacktestDetail.Trades). Structurally
 * identical to trading/OrdersTable.tsx's fill rows -- same formatPrice/
 * formatQuantity, same up/down/em-dash convention for a nullable P/L --
 * because TradeRecord is shaped the same way Order/Trade already is
 * (SPEC.md 2.8). No new formatting logic here, just a reuse.
 *
 * One deliberate difference from OrdersTable: bar_timestamp is a daily
 * bar's date, not an order's created_at instant, so this renders a
 * calendar date via format.ts's formatDate rather than OrdersTable's
 * full date+time -- there's no time-of-day here that would mean anything.
 */
import type { TradeRecord } from '../api/types'
import { formatDate, formatPrice, formatQuantity } from '../format'

function plClassName(realizedPl: number | null): string {
  if (realizedPl === null) return 'text-ink-subtle'
  if (realizedPl > 0) return 'text-up'
  if (realizedPl < 0) return 'text-down'
  return 'text-ink'
}

function TradeRow({ trade }: { trade: TradeRecord }) {
  return (
    <tr className="border-b border-line last:border-b-0">
      <td className="px-4 py-3 text-ink-muted">
        {formatDate(trade.bar_timestamp)}
      </td>
      <td className="px-4 py-3 capitalize text-ink">{trade.side}</td>
      <td className="tabular px-4 py-3 text-right font-mono text-ink">
        {formatQuantity(trade.quantity)}
      </td>
      <td className="tabular px-4 py-3 text-right font-mono text-ink">
        {formatPrice(trade.price)}
      </td>
      <td
        className={`tabular px-4 py-3 text-right font-mono ${plClassName(trade.realized_pl)}`}
      >
        {trade.realized_pl !== null ? formatPrice(trade.realized_pl) : '—'}
      </td>
    </tr>
  )
}

interface TradeLogTableProps {
  trades: TradeRecord[]
}

export default function TradeLogTable({ trades }: TradeLogTableProps) {
  if (trades.length === 0) {
    return (
      <p className="p-4 text-sm text-ink-muted">
        No trades were simulated for this run.
      </p>
    )
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-line-strong text-left text-ink-muted">
            <th className="px-4 py-2 font-medium">Date</th>
            <th className="px-4 py-2 font-medium">Side</th>
            <th className="px-4 py-2 text-right font-medium">Quantity</th>
            <th className="px-4 py-2 text-right font-medium">Price</th>
            <th className="px-4 py-2 text-right font-medium">Realized P/L</th>
          </tr>
        </thead>
        <tbody>
          {trades.map((trade, index) => (
            // eslint-disable-next-line react/no-array-index-key -- trades
            // have no id; bar_timestamp+side+price+quantity could repeat.
            <TradeRow key={index} trade={trade} />
          ))}
        </tbody>
      </table>
    </div>
  )
}
