/**
 * The parity read of SPEC.md 2.4, run against real values instead of by eye.
 *
 * The fixtures are a genuine report and the genuine narrative describing it,
 * captured from the running stack during T11 (account seeded, then deleted).
 * Every figure format.ts renders for the card must appear CHARACTER FOR
 * CHARACTER in the sentence Go rendered beside it.
 *
 * WHY this exists next to format.test.ts's parity table rather than inside
 * it. The two catch different faults and neither subsumes the other:
 *
 *   - The table catches ROUNDING divergence. Its inputs are adversarial
 *     halfway cases (-99.85, -9.995) chosen because toFixed and the backend
 *     disagree there; mutating fixed() to toFixed turns 14 of them red.
 *   - This file catches FORMATTER SELECTION -- rendering a signed percent
 *     unsigned, an HHI at two places, a Sharpe at one. The table cannot see
 *     those, because it tests each formatter against its own expectations
 *     rather than against the Kind the backend gave that same field.
 *
 * Measured, not assumed: mutating fixed() to toFixed leaves every assertion
 * here GREEN, because no figure a real portfolio produces lands on a halfway
 * case (SPEC.md 2.4 measured 0 mismatches in 20,000 random doubles). That is
 * the table's job, not this one's. Conversely, dropping the sign, or moving
 * the HHI to 2 or 4 places, or the Sharpe to 1, each turns this file red and
 * leaves the table green.
 */
import { describe, expect, it } from 'vitest'
import report from './__fixtures__/live-report.json'
import narrative from './__fixtures__/live-narrative.json'
import {
  formatIndex,
  formatPercent,
  formatRatio,
  formatSignedPercent,
} from '../format'

const r = report as any
const prose = (narrative as any).narrative as Record<string, string>

const cases: [string, string, string][] = [
  ['risk', 'cash weight', formatPercent(r.risk.cash_weight_pct)],
  ['risk', 'largest position', formatPercent(r.risk.largest_position_pct)],
  ['risk', 'concentration HHI', formatIndex(r.risk.concentration_hhi)],
  ['risk', 'annualized volatility', formatPercent(r.risk.annualized_volatility_pct)],
  ['risk', 'max drawdown', formatPercent(r.risk.max_drawdown_pct)],
  ['benchmarking', 'portfolio return', formatSignedPercent(r.benchmarking.portfolio_return_pct)],
  ['benchmarking', 'portfolio sharpe', formatRatio(r.benchmarking.portfolio_sharpe)],
  ...r.benchmarking.benchmarks.flatMap((b: any) => [
    ['benchmarking', `${b.symbol} return`, formatSignedPercent(b.return_pct)],
    ['benchmarking', `${b.symbol} excess`, formatSignedPercent(b.excess_return_pct)],
    ['benchmarking', `${b.symbol} sharpe`, formatRatio(b.sharpe)],
  ] as [string, string, string][]),
]

describe('live parity: card figure vs the sentence beside it', () => {
  it('the report and the narrative describe the same report', () => {
    expect((narrative as any).report_hash).toBe(r.report_hash)
  })

  // Boundary-aware: a bare toContain would accept "5.9%" inside "+5.9%",
  // which is exactly the formatter-selection error this check exists to
  // catch (KindPercent used where Go used KindSignedPercent).
  it.each(cases)('%s / %s renders as %s in the prose', (section, _label, rendered) => {
    const escaped = rendered.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    // Guarded on BOTH sides: without the right-hand guard "0.50" matches
    // inside "0.504", which let a 3dp->2dp HHI mutation through.
    const bounded = new RegExp(`(^|[^0-9.+-])${escaped}(?![0-9])`)
    expect(prose[section]).toMatch(bounded)
  })

  it('every rendered figure is accounted for, none silently skipped', () => {
    expect(cases).toHaveLength(13)
  })
})
