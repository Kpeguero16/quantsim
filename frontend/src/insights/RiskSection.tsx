/**
 * Concentration, volatility and drawdown, as of the last trading day.
 *
 * Every figure here is rendered through format.ts with the formatter that
 * matches the Kind the backend gives that same figure in
 * placeholders.go -- because each of these appears twice on one screen,
 * once as a number in this grid and once inside a sentence Go formatted,
 * and the two must agree character for character (SPEC.md 2.4).
 */
import { formatIndex, formatPercent, formatPrice, formatQuantity } from '../format'
import type { RiskSection as RiskSectionData } from '../api/types'
import NarrativeBlock from './NarrativeBlock'
import SectionNotice from './SectionNotice'
import Stat from './Stat'

interface RiskSectionProps {
  section: RiskSectionData
  /** This section's prose, when there is prose to show. Undefined covers
   * three different situations that all render the same way: no narrative
   * was generated, the narrative did not describe this section, or the
   * narrative describes a report that is no longer on screen. The panel
   * decides which -- narrativeView is the single producer -- and by the
   * time it reaches here the only question left is whether to render it. */
  narrative?: string
}

export default function RiskSection({ section, narrative }: RiskSectionProps) {
  // Before any figure is read. A degraded section still carries every one
  // of them as zero, and a zero that was never measured is not a
  // measurement (SPEC.md 2.5).
  if (section.state !== 'ok') {
    return <SectionNotice heading="Risk" reason={section.reason} />
  }

  return (
    <section className="rounded-lg border border-line bg-surface">
      <h3 className="border-b border-line px-4 py-3 text-sm font-semibold text-ink">
        Risk
      </h3>

      <div className="grid grid-cols-2 gap-4 p-4 sm:grid-cols-3 lg:grid-cols-5">
        <Stat label="Cash weight" value={formatPercent(section.cash_weight_pct)} />
        <Stat
          label="Largest position"
          value={formatPercent(section.largest_position_pct)}
        />
        {/* Three decimals, not two: KindIndex. An HHI lives in 0..1 and
            two places would collapse a real spread of concentrations onto
            the same handful of values. */}
        <Stat
          label="Concentration (HHI)"
          value={formatIndex(section.concentration_hhi)}
        />
        <Stat
          label="Volatility (annualized)"
          value={formatPercent(section.annualized_volatility_pct)}
        />
        {/* Unsigned, and not colored. The backend reports a drawdown as a
            MAGNITUDE (KindPercent, not KindSignedPercent) and the wire
            really does carry it positive -- so formatSignedPercent would
            print a "+" in front of a loss. Coloring it red regardless
            would be this layer inventing a judgement about whether a given
            drawdown is bad, which is the backend's call and not made. */}
        <Stat label="Max drawdown" value={formatPercent(section.max_drawdown_pct)} />
      </div>

      {section.positions.length > 0 && (
        <div className="overflow-x-auto border-t border-line">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-line-strong text-left text-ink-muted">
                <th className="px-4 py-2 font-medium">Symbol</th>
                <th className="px-4 py-2 text-right font-medium">Quantity</th>
                <th className="px-4 py-2 text-right font-medium">Market Value</th>
                <th className="px-4 py-2 text-right font-medium">Weight</th>
              </tr>
            </thead>
            <tbody>
              {section.positions.map((position) => (
                <tr
                  key={position.symbol}
                  className="border-b border-line last:border-b-0"
                >
                  <td className="px-4 py-3 font-medium text-ink">
                    {position.symbol}
                  </td>
                  <td className="tabular px-4 py-3 text-right font-mono text-ink">
                    {formatQuantity(position.quantity)}
                  </td>
                  <td className="tabular px-4 py-3 text-right font-mono text-ink">
                    ${formatPrice(position.market_value)}
                  </td>
                  <td className="tabular px-4 py-3 text-right font-mono text-ink">
                    {formatPercent(position.weight_pct)}
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
