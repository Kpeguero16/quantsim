/**
 * Two questions about a narrative response, both of which the panel has to
 * answer before it can render a word of prose: does this narrative describe
 * the report currently on screen, and if there is no prose, what do we tell
 * the reader?
 *
 * Sources of truth:
 *   services/ai-insights/internal/handler/narrative.go (the nine reasons)
 *   SPEC.md 2.3 (hash agreement), 2.6 (the copy)
 */
import type {
  NarrativeResponse,
  PortfolioInsightsResponse,
} from '../api/types'
// Type-only, so this stays a pure module at runtime -- nothing here
// imports the client, and the import is erased in the emitted JS.
import type { NarrativeState } from './use-narrative'

/**
 * The nine reasons a narrative can be unavailable, verbatim from
 * narrative.go's const block. Spelled out rather than matched loosely,
 * because a reason this file does not recognize must fall through to safe
 * copy rather than reach a reader -- see narrativeNotice.
 */
const REASON = {
  notConfigured: 'narrative generation is not configured',
  nothing: 'no analysis is available to describe',
  overCap: 'daily generation limit reached',
} as const

/**
 * What to render in place of missing prose.
 *
 * `show: false` is not the same as an empty message and is not a
 * degenerate case: when there is nothing to describe, each section has
 * ALREADY explained its own emptiness through its `reason` (SPEC.md 2.5),
 * and a second explanation of the same emptiness underneath it is noise.
 */
export type NarrativeNotice =
  | { show: false }
  | { show: true; text: string; retry: boolean }

/**
 * The clause every message in this file ends with.
 *
 * It is doing real work rather than reassuring. This is the one place in
 * the app where a visible failure sits directly beneath correct data, and
 * the default reading of an error message on a page is that something on
 * that page is wrong. Nothing above it is.
 */
const UNAFFECTED = 'The figures above are unaffected.'

/**
 * Maps an `unavailable` narrative's reason to what the reader is told.
 *
 * Nine reasons collapse to four outcomes -- three sentences plus silence --
 * and they do not collapse to one: "there was nothing to phrase",
 * "phrasing is switched off", "you have spent today's allowance" and "it
 * broke" are four different facts about the system, and two of them are
 * actionable.
 *
 * An unrecognized reason -- including undefined -- falls through to the
 * generic sentence and NEVER renders the raw string. This is deliberately
 * the opposite of backtest-errors.ts, which passes unknown backend messages
 * through: backtesting's messages were written to be read by a user, while
 * these were written for a closed set of nine and for a log line. A tenth
 * added in some later step should degrade to safe copy rather than leak a
 * phrase nobody wrote for a reader.
 *
 * `retry` is offered only where retrying could plausibly succeed. Over the
 * daily cap it cannot, and with generation switched off it cannot; putting
 * a button there would invite a click that is guaranteed to fail, and in
 * the cap's case one that would look like it cost money.
 */
export function narrativeNotice(reason: string | undefined): NarrativeNotice {
  switch (reason) {
    case REASON.nothing:
      return { show: false }

    case REASON.notConfigured:
      return {
        show: true,
        text: 'Written summaries are turned off.',
        retry: false,
      }

    case REASON.overCap:
      return {
        show: true,
        text: `You've reached today's summary limit. ${UNAFFECTED}`,
        retry: false,
      }

    default:
      return {
        show: true,
        text: `A written summary isn't available right now. ${UNAFFECTED}`,
        retry: true,
      }
  }
}

/**
 * Does this narrative describe the report on screen?
 *
 * The two endpoints compute the report independently, so a narrative can
 * describe a report that is no longer the one being displayed -- after a
 * fill, or if the shared five-minute cache expired between the two
 * requests. The prose would then carry figures Go substituted correctly
 * from ITS report, sitting beside different figures rendered correctly from
 * OURS. Both right, no error, indistinguishable on the page. That is the
 * failure Step 21 made structurally impossible inside the service, and this
 * is what keeps it impossible once the two are composed (SPEC.md 2.3).
 *
 * FAILS CLOSED, and that is the whole point of writing it as a function
 * rather than `a === b` at the call site. `report_hash` is optional on the
 * narrative response, so an absent hash is ordinary operation rather than a
 * bug -- and `undefined === undefined` is true, which would silently turn
 * "we have no evidence these match" into "these match" in exactly the case
 * the check exists for. Absent evidence of agreement is not agreement.
 */
export function describesReport(
  report: Pick<PortfolioInsightsResponse, 'report_hash'>,
  narrative: Pick<NarrativeResponse, 'report_hash'>,
): boolean {
  if (!report.report_hash || !narrative.report_hash) return false
  return report.report_hash === narrative.report_hash
}

/**
 * What the panel should show where the prose would go.
 *
 * One function decides this, rather than the panel assembling it from four
 * conditions at the call site, because the property that matters is a
 * NEGATIVE one: stale prose must not reach the DOM at all. A condition
 * spread across JSX can be satisfied in four places and violated in a
 * fifth, and nothing would fail. Here it is one value with one producer,
 * and `kind: 'prose'` is the only way sections can be reached.
 */
export type NarrativeView =
  | { kind: 'pending' }
  | { kind: 'prose'; sections: Record<string, string> }
  | { kind: 'notice'; text: string; retry: boolean }
  | { kind: 'silent' }

/** Shown when the report has moved on since the summary was written. */
const STALE = 'These figures have changed since this summary was written.'

/**
 * Resolves a narrative request and the report on screen into one outcome.
 *
 * The ordering is the substance:
 *
 * 1. In flight is neither prose nor a failure.
 * 2. A transport failure and a `200` saying the service is unavailable are
 *    the same fact to a reader, so both are spelled by narrativeNotice.
 * 3. Hash disagreement is checked BEFORE the prose is handed over. Prose
 *    that describes a different report is not degraded prose to be shown
 *    with a caveat -- it is prose whose figures contradict the ones above
 *    it, and the reader has no way to tell which is which. It is replaced,
 *    not annotated (SPEC.md 2.3).
 * 4. `state: 'ok'` with a null map is a contract the backend does not
 *    break today. It is handled as a failure rather than as prose,
 *    because the alternative is rendering nothing where prose was
 *    promised and calling that success.
 */
export function narrativeView(
  report: Pick<PortfolioInsightsResponse, 'report_hash'>,
  state: NarrativeState,
): NarrativeView {
  if (state.status === 'loading') return { kind: 'pending' }

  if (state.status === 'error') {
    const notice = narrativeNotice(undefined)
    return notice.show
      ? { kind: 'notice', text: notice.text, retry: notice.retry }
      : { kind: 'silent' }
  }

  const { narrative } = state

  if (narrative.state !== 'ok') {
    const notice = narrativeNotice(narrative.reason)
    return notice.show
      ? { kind: 'notice', text: notice.text, retry: notice.retry }
      : { kind: 'silent' }
  }

  if (!describesReport(report, narrative)) {
    return { kind: 'notice', text: STALE, retry: true }
  }

  if (narrative.narrative === null) {
    const notice = narrativeNotice(undefined)
    return notice.show
      ? { kind: 'notice', text: notice.text, retry: notice.retry }
      : { kind: 'silent' }
  }

  return { kind: 'prose', sections: narrative.narrative }
}
