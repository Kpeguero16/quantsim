/**
 * The pulsing-bar placeholder PositionsTable, OrdersTable, and
 * PortfolioSummary all show while their shared `usePortfolio`/`useOrders`
 * state is `loading` -- pulled out once three components needed the same
 * bars-in-a-container shape so a future change to it (animation, a11y
 * attributes) has one place to land instead of three.
 */
interface LoadingSkeletonProps {
  count: number
  label: string
  barClassName?: string
  containerClassName?: string
}

export default function LoadingSkeleton({
  count,
  label,
  barClassName = 'h-9',
  containerClassName = 'space-y-3 p-4',
}: LoadingSkeletonProps) {
  return (
    <div className={containerClassName} aria-busy="true" aria-label={label}>
      {Array.from({ length: count }).map((_, i) => (
        <div
          // eslint-disable-next-line react/no-array-index-key -- a
          // fixed-count skeleton list has no stable identity to key by
          key={i}
          className={`animate-pulse rounded bg-elevated ${barClassName}`}
        />
      ))}
    </div>
  )
}
