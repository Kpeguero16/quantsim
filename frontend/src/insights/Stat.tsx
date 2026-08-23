/**
 * One labelled figure.
 *
 * A local copy of the shape PortfolioSummary.tsx uses rather than an
 * extraction of it: that one is a 2xl hero number for three headline
 * values, these are a denser grid of five or six. Pulling one component out
 * to serve both would mean parameterising the size, the grid and the
 * emphasis for two callers -- more configuration than the six lines it
 * would save.
 */
interface StatProps {
  label: string
  value: string
  valueClassName?: string
}

export default function Stat({
  label,
  value,
  valueClassName = 'text-ink',
}: StatProps) {
  return (
    <div>
      <p className="text-sm text-ink-muted">{label}</p>
      <p className={`tabular mt-1 font-mono text-lg ${valueClassName}`}>
        {value}
      </p>
    </div>
  )
}
