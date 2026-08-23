/**
 * The 'insights' tab's content: the Step 20 report, section by section.
 *
 * The report renders as soon as it lands and is never blocked on the
 * narrative, which is a separate request with a separate failure mode
 * (SPEC.md 2.2). Prose arrives underneath these figures in T8.
 *
 * Each section owns its own state branch rather than being handed a
 * pre-checked figure set: the "branch before reading" rule is only worth
 * anything if it sits in the same file as the reading.
 *
 * Unlike the four trading tabs this one is not scoped to the selected
 * symbol -- the report is about the account, not about whatever is charted
 * on the left.
 */
import { formatDate, formatQuantity } from '../format'
import LoadingSkeleton from '../trading/LoadingSkeleton'
import BehaviorSection from './BehaviorSection'
import BenchmarkingSection from './BenchmarkingSection'
import NarrativeStatus from './NarrativeStatus'
import RiskSection from './RiskSection'
import { narrativeView } from './narrative-state'
import { useInsights } from './use-insights'
import { useNarrative } from './use-narrative'

interface InsightsPanelProps {
  /** Bumped by Dashboard on every fill. The report is recomputed from the
   * account's trade history, so a fill is the one thing that invalidates
   * it -- and the only thing, which is why nothing else bumps this. */
  refreshKey: number
}

export default function InsightsPanel({ refreshKey }: InsightsPanelProps) {
  const state = useInsights(refreshKey)
  // Fired on mount alongside the report and never again on its own. The
  // two are independent: the figures below do not wait for this, and this
  // failing changes nothing about them (SPEC.md 2.2).
  const narrative = useNarrative()

  if (state.status === 'loading') {
    return (
      <LoadingSkeleton
        count={3}
        label="Loading portfolio analysis"
        barClassName="h-24"
        containerClassName="space-y-4 p-4"
      />
    )
  }

  if (state.status === 'error') {
    // A null message is the decision not to show one, not a missing
    // string: on invalid_token the session is already being cleared and an
    // error about portfolio analytics would explain the wrong thing
    // (SPEC.md 2.7).
    if (state.message === null) return null
    return (
      <p role="alert" className="p-4 text-sm text-danger">
        {state.message}
      </p>
    )
  }

  const { insights } = state
  const { risk, benchmarking, behavior } = insights

  // One value, one producer. Prose is reachable only through kind ===
  // 'prose', which is what makes "stale prose never reaches the DOM" a
  // property of this file rather than a rule someone has to remember at
  // four call sites (SPEC.md 2.3).
  const view = narrativeView(insights, narrative)
  const prose = view.kind === 'prose' ? view.sections : undefined

  return (
    <div className="space-y-4 p-4">
      <header className="flex items-baseline justify-between gap-4">
        <h2 className="text-sm font-semibold text-ink">Portfolio Analysis</h2>
        {/* The window, once, here -- not per section and not from the
            narrative's copy of it (SPEC.md 9.4). It is what makes every
            figure below interpretable: a 1.2% volatility measured over
            three days and the same figure measured over three years are
            not the same claim. Repeating it three times would invite three
            different answers the day the two requests disagree.

            computed_at is deliberately absent. It is cache age rather than
            data age -- a report can be freshly computed from bars a day old
            -- and showing it beside these figures would date them wrongly.

            trading_days counts dates in the reconstruction calendar rather
            than weekdays elapsed, which is why it is stated instead of
            being left for the reader to infer from the two dates. */}
        {insights.as_of_date && (
          <p className="text-sm text-ink-muted">
            {insights.window.start_date &&
              `${formatDate(insights.window.start_date)} – `}
            {formatDate(insights.as_of_date)}
            <span className="text-ink-subtle">
              {' · '}
              {formatQuantity(insights.window.trading_days)} trading day
              {insights.window.trading_days === 1 ? '' : 's'}
            </span>
          </p>
        )}
      </header>

      {/* Prose is handed to the section, which renders it inside its own
          card beneath its own figures -- never above them and never
          instead of them. Passing it in rather than rendering it here is
          also what makes "a degraded section shows no prose" structural:
          a degraded section returns its notice before it ever reaches the
          line that would render this. */}
      <RiskSection section={risk} narrative={prose?.risk} />
      <BenchmarkingSection
        section={benchmarking}
        narrative={prose?.benchmarking}
      />
      <BehaviorSection section={behavior} narrative={prose?.behavior} />

      {view.kind === 'notice' && (
        <NarrativeStatus
          text={view.text}
          retry={view.retry}
          onRegenerate={narrative.regenerate}
        />
      )}
      {view.kind === 'pending' && (
        <NarrativeStatus
          text="Preparing a written summary…"
          retry={false}
          onRegenerate={narrative.regenerate}
        />
      )}
    </div>
  )
}
