/**
 * Full order history, rejections included (SPEC.md 2.6). The backend
 * guarantees exactly one of filled_price/rejection_reason is set per
 * order, and already returns them newest-first -- this component neither
 * re-sorts nor renders both fields as if either could be present.
 */
import type { Order } from '../api/types'
import { formatPrice, formatQuantity } from '../format'
import LoadingSkeleton from './LoadingSkeleton'
import { describeRejection } from './rejection-reason'
import type { OrdersState } from './use-orders'

function formatTime(createdAt: string): string {
  return new Date(createdAt).toLocaleString()
}

function StatusBadge({ status }: { status: Order['status'] }) {
  const isFilled = status === 'filled'
  return (
    <span
      className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${
        isFilled
          ? 'bg-accent-muted text-accent-hover'
          : 'bg-danger-bg text-danger'
      }`}
    >
      {status}
    </span>
  )
}

function OrderRow({ order }: { order: Order }) {
  return (
    <tr className="border-b border-line last:border-b-0">
      <td className="px-4 py-3 text-ink-muted">{formatTime(order.created_at)}</td>
      <td className="px-4 py-3 font-medium text-ink">{order.symbol}</td>
      <td className="px-4 py-3 capitalize text-ink">{order.side}</td>
      <td className="tabular px-4 py-3 text-right font-mono text-ink">
        {formatQuantity(order.quantity)}
      </td>
      <td className="px-4 py-3">
        <StatusBadge status={order.status} />
      </td>
      <td className="tabular px-4 py-3 text-right font-mono text-ink">
        {order.filled_price !== null ? formatPrice(order.filled_price) : '—'}
      </td>
      <td className="px-4 py-3 text-sm text-ink-muted">
        {order.rejection_reason !== null
          ? describeRejection(order.rejection_reason)
          : ''}
      </td>
    </tr>
  )
}

interface OrdersTableProps {
  state: OrdersState
}

export default function OrdersTable({ state }: OrdersTableProps) {
  if (state.status === 'loading') {
    return <LoadingSkeleton count={3} label="Loading orders" />
  }

  if (state.status === 'error') {
    return (
      <p role="alert" className="p-4 text-sm text-danger">
        {state.message}
      </p>
    )
  }

  if (state.orders.length === 0) {
    return <p className="p-4 text-sm text-ink-muted">No orders yet.</p>
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-line-strong text-left text-ink-muted">
            <th className="px-4 py-2 font-medium">Time</th>
            <th className="px-4 py-2 font-medium">Symbol</th>
            <th className="px-4 py-2 font-medium">Side</th>
            <th className="px-4 py-2 text-right font-medium">Quantity</th>
            <th className="px-4 py-2 font-medium">Status</th>
            <th className="px-4 py-2 text-right font-medium">Price</th>
            <th className="px-4 py-2 font-medium">Reason</th>
          </tr>
        </thead>
        <tbody>
          {state.orders.map((order) => (
            <OrderRow key={order.id} order={order} />
          ))}
        </tbody>
      </table>
    </div>
  )
}
