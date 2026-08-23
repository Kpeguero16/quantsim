/**
 * The portfolio against its buy-and-hold benchmarks.
 *
 * excess_return_pct is a simple difference, not a regression alpha -- the
 * backend says so at the struct, and nothing here dresses it up as more
 * than that.
 */
import { formatRatio, formatSignedPercent } from '../format'
import type { BenchmarkingSection as BenchmarkingSectionData } from '../api/types'
import NarrativeBlock from './NarrativeBlock'
import SectionNotice from './SectionNotice'
import Stat from './Stat'

/**
 * Red, green or neutral by the sign of the figure itself.
 *
 * On the raw value rather than the formatted string, for the same reason
 * the backend decides its "+" that way: a return of +0.04% renders as
 * "+0.0%" and is still a gain. Reading the sign off the rendered text
 * would call it flat.
 */
function signClass(value: number): string {
  if (value > 0) return 'text-up'
  if (value < 0) return 'text-down'
  return 'text-ink'
}

interface BenchmarkingSectionProps {
  section: BenchmarkingSectionData
  /** This section's prose, when there is prose to show. Undefined covers
   * three different situations that all render the same way: no narrative
   * was generated, the narrative did not describe this section, or the
   * narrative describes a report that is no longer on screen. The panel
   * decides which -- narrativeView is the single producer -- and by the
   * time it reaches here the only question left is whether to render it. */
  narrative?: string
}

export default function BenchmarkingSection({ section, narrative }: BenchmarkingSectionProps) {
  if (section.state !== 'ok') {
    return <SectionNotice heading="Benchmarking" reason={section.reason} />
  }

  return (
    <section className="rounded-lg border border-line bg-surface">
      <h3 className="border-b border-line px-4 py-3 text-sm font-semibold text-ink">
        Benchmarking
      </h3>

      <div className="grid grid-cols-2 gap-4 p-4">
        <Stat
          label="Portfolio return"
          value={formatSignedPercent(section.portfolio_return_pct)}
          valueClassName={signClass(section.portfolio_return_pct)}
        />
        {/* Two decimals: KindRatio. Not colored -- a Sharpe ratio's sign
            is not the same kind of good-or-bad news a return's is, and
            painting it green would assert a judgement nothing computed. */}
        <Stat label="Sharpe" value={formatRatio(section.portfolio_sharpe)} />
      </div>

      {section.benchmarks.length > 0 && (
        <div className="overflow-x-auto border-t border-line">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-line-strong text-left text-ink-muted">
                <th className="px-4 py-2 font-medium">Benchmark</th>
                <th className="px-4 py-2 text-right font-medium">Return</th>
                <th className="px-4 py-2 text-right font-medium">Excess</th>
                <th className="px-4 py-2 text-right font-medium">Sharpe</th>
              </tr>
            </thead>
            <tbody>
              {section.benchmarks.map((benchmark) => (
                <tr
                  key={benchmark.symbol}
                  className="border-b border-line last:border-b-0"
                >
                  <td className="px-4 py-3 text-ink">
                    <span className="font-medium">{benchmark.symbol}</span>{' '}
                    <span className="text-ink-muted">{benchmark.label}</span>
                  </td>
                  <td
                    className={`tabular px-4 py-3 text-right font-mono ${signClass(benchmark.return_pct)}`}
                  >
                    {formatSignedPercent(benchmark.return_pct)}
                  </td>
                  {/* The portfolio's excess OVER this benchmark, so its
                      sign is the portfolio's news and not the
                      benchmark's. */}
                  <td
                    className={`tabular px-4 py-3 text-right font-mono ${signClass(benchmark.excess_return_pct)}`}
                  >
                    {formatSignedPercent(benchmark.excess_return_pct)}
                  </td>
                  <td className="tabular px-4 py-3 text-right font-mono text-ink">
                    {formatRatio(benchmark.sharpe)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <NarrativeBlock text={narrative} />
    </section>
  )
}
