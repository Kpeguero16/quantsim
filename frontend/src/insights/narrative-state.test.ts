import { describe, expect, it } from 'vitest'

import type { NarrativeResponse } from '../api/types'
import type { NarrativeState } from './use-narrative'
import {
  describesReport,
  narrativeNotice,
  narrativeView,
} from './narrative-state'

/**
 * All nine reasons narrative.go can return, verbatim from its const block.
 *
 * Kept as a list rather than folded into the assertions so that the count
 * itself is asserted: SPEC.md 2.6 collapses NINE reasons to three
 * outcomes, and if a tenth is added upstream the number here is what
 * should be re-derived rather than the mapping quietly absorbing it.
 */
const ALL_REASONS = [
  'narrative generation is not configured',
  'no analysis is available to describe',
  'no usable narrative could be generated',
  'the narrative service is unavailable',
  'daily generation limit reached',
  'narrative generation is unavailable without a generation counter',
  'the narrative service is rate limited',
  'narrative generation timed out',
  'the model declined to describe this portfolio',
]

/** The notice's text, failing loudly if it renders nothing -- so a test
 * that meant to compare two sentences cannot quietly compare two blanks. */
function textOf(reason: string | undefined): string {
  const notice = narrativeNotice(reason)
  if (!notice.show) throw new Error(`expected copy for ${String(reason)}`)
  return notice.text
}

describe('narrativeNotice', () => {
  it('covers the nine reasons the backend can send', () => {
    expect(ALL_REASONS).toHaveLength(9)
    expect(new Set(ALL_REASONS).size).toBe(9)
  })

  it('says nothing when there was nothing to describe', () => {
    // The sections have already explained their own emptiness. A second
    // explanation of the same emptiness underneath them is noise.
    expect(narrativeNotice('no analysis is available to describe')).toEqual({
      show: false,
    })
  })

  it('distinguishes "switched off" from "it broke"', () => {
    expect(textOf('narrative generation is not configured')).not.toBe(
      textOf('the narrative service is unavailable'),
    )
  })

  it('distinguishes "you spent today\'s allowance" from "it broke"', () => {
    expect(textOf('daily generation limit reached')).not.toBe(
      textOf('the narrative service is unavailable'),
    )
  })

  // Four, not three: silence is an outcome, and it is the one that is
  // easiest to lose by folding it into the generic branch.
  it('produces exactly four distinct outcomes across all nine reasons', () => {
    const outcomes = new Set(
      ALL_REASONS.map((r) => JSON.stringify(narrativeNotice(r))),
    )
    expect(outcomes.size).toBe(4)
  })

  // The clause is load-bearing, not reassurance: this is the one place a
  // visible failure sits directly beneath correct data, and the default
  // reading of an error message on a page is that the page is wrong.
  it.each(
    ALL_REASONS.filter((r) => r !== 'no analysis is available to describe'),
  )('tells the reader the figures are unaffected: %s', (reason) => {
    const notice = narrativeNotice(reason)
    expect(notice.show).toBe(true)
    if (!notice.show) return
    if (notice.text === 'Written summaries are turned off.') return
    expect(notice.text).toContain('figures above are unaffected')
  })

  it('offers a retry only where retrying could succeed', () => {
    // Over the cap and switched off, a retry is guaranteed to fail -- and
    // in the cap's case it would look like it cost money.
    expect(narrativeNotice('daily generation limit reached')).toMatchObject({
      retry: false,
    })
    expect(
      narrativeNotice('narrative generation is not configured'),
    ).toMatchObject({ retry: false })
    expect(narrativeNotice('narrative generation timed out')).toMatchObject({
      retry: true,
    })
  })

  it.each([
    'a reason added in some later step',
    '',
    'daily generation limit reached ',
  ])('never renders an unrecognized reason verbatim: %s', (reason) => {
    const notice = narrativeNotice(reason)
    expect(notice.show).toBe(true)
    if (!notice.show) return
    expect(notice.text).not.toContain(reason.trim() || 'IMPOSSIBLE')
    expect(notice.text).toContain('figures above are unaffected')
  })

  it('handles an absent reason without throwing', () => {
    const notice = narrativeNotice(undefined)
    expect(notice.show).toBe(true)
    if (!notice.show) return
    expect(notice.text.length).toBeGreaterThan(0)
  })
})

describe('describesReport', () => {
  it('agrees only when both hashes are present and equal', () => {
    expect(
      describesReport({ report_hash: 'abc123' }, { report_hash: 'abc123' }),
    ).toBe(true)
  })

  it('disagrees when the hashes differ', () => {
    expect(
      describesReport({ report_hash: 'abc123' }, { report_hash: 'def456' }),
    ).toBe(false)
  })

  /**
   * The four absence combinations, and the reason this function exists at
   * all rather than `a === b` at the call site.
   *
   * report_hash is optional on the narrative response, so undefined is
   * ordinary operation. `undefined === undefined` is true, which would
   * turn "no evidence these match" into "these match" in exactly the case
   * the check was written for -- and the prose would then be shown beside
   * figures it may not describe, silently, which is the whole failure
   * SPEC.md 2.3 exists to prevent.
   */
  it.each([
    ['both absent', undefined, undefined],
    ['narrative absent', 'abc123', undefined],
    ['report absent', undefined, 'abc123'],
    ['both empty strings', '', ''],
    ['narrative empty', 'abc123', ''],
    ['report empty', '', 'abc123'],
  ])('fails closed: %s', (_name, reportHash, narrativeHash) => {
    // The cast is the point, not a workaround. types.ts declares
    // report_hash as required on the report, because the handler always
    // sends it -- but a declared type is a claim about the wire, not a
    // guarantee about it (api/client.ts makes the same argument where it
    // validates the error body field by field). This asserts the function
    // still fails closed when the claim turns out to be false.
    expect(
      describesReport(
        { report_hash: reportHash as string },
        { report_hash: narrativeHash },
      ),
    ).toBe(false)
  })
})

/**
 * narrativeView is where "stale prose never reaches the DOM" stops being a
 * rule someone has to remember and becomes a property: `kind: 'prose'` is
 * the only route to a sections map, and every test below that expects
 * something other than prose is asserting that route is closed.
 */
const REPORT = { report_hash: 'abc123' }

function ok(narrative: Partial<NarrativeResponse>): NarrativeState {
  return {
    status: 'ok',
    narrative: {
      state: 'ok',
      narrative: { risk: 'Risk prose.' },
      report_hash: 'abc123',
      ...narrative,
    } as NarrativeResponse,
  }
}

describe('narrativeView', () => {
  it('hands over prose only when the narrative describes this report', () => {
    expect(narrativeView(REPORT, ok({}))).toEqual({
      kind: 'prose',
      sections: { risk: 'Risk prose.' },
    })
  })

  it('is pending while the request is in flight', () => {
    expect(narrativeView(REPORT, { status: 'loading' })).toEqual({
      kind: 'pending',
    })
  })

  /**
   * The load-bearing case. The prose here is present, well-formed, and
   * describes a DIFFERENT report -- every figure in it was substituted
   * correctly by Go from the report it was written for. Showing it beside
   * these figures would put two correct-but-different numbers on one
   * screen with nothing marking which is which, which is the whole failure
   * SPEC.md 2.3 exists to prevent.
   */
  it('replaces prose that describes a different report', () => {
    const view = narrativeView(REPORT, ok({ report_hash: 'def456' }))
    expect(view.kind).toBe('notice')
    expect(view).not.toHaveProperty('sections')
  })

  it.each([
    ['the narrative carries no hash', undefined],
    ['the narrative carries an empty hash', ''],
  ])('replaces prose when %s', (_name, hash) => {
    const view = narrativeView(REPORT, ok({ report_hash: hash }))
    expect(view.kind).toBe('notice')
    expect(view).not.toHaveProperty('sections')
  })

  it('replaces prose when the report itself carries no hash', () => {
    const view = narrativeView({ report_hash: '' }, ok({}))
    expect(view.kind).toBe('notice')
    expect(view).not.toHaveProperty('sections')
  })

  it('offers a retry on a stale narrative, and says the figures changed', () => {
    const view = narrativeView(REPORT, ok({ report_hash: 'def456' }))
    expect(view).toMatchObject({ retry: true })
    if (view.kind !== 'notice') return
    expect(view.text).toContain('changed')
  })

  it('maps an unavailable narrative through the reason copy', () => {
    const view = narrativeView(REPORT, {
      status: 'ok',
      narrative: {
        state: 'unavailable',
        narrative: null,
        reason: 'daily generation limit reached',
      } as NarrativeResponse,
    })
    expect(view).toMatchObject({ kind: 'notice', retry: false })
    if (view.kind !== 'notice') return
    expect(view.text).toContain("today's summary limit")
  })

  it('says nothing when there was nothing to describe', () => {
    const view = narrativeView(REPORT, {
      status: 'ok',
      narrative: {
        state: 'unavailable',
        narrative: null,
        reason: 'no analysis is available to describe',
      } as NarrativeResponse,
    })
    // The sections have already explained their own emptiness. Note this
    // is reached WITHOUT a hash on the response, which is correct: there
    // is no prose to check against the report in the first place.
    expect(view).toEqual({ kind: 'silent' })
  })

  it('treats a transport failure exactly like a backend outage', () => {
    const failed = narrativeView(REPORT, { status: 'error' })
    const outage = narrativeView(REPORT, {
      status: 'ok',
      narrative: {
        state: 'unavailable',
        narrative: null,
        reason: 'the narrative service is unavailable',
      } as NarrativeResponse,
    })
    // Same fact to a reader: it broke, the figures are fine, try again.
    expect(failed).toEqual(outage)
  })

  /**
   * A contract the backend does not break today. It is handled as a
   * failure rather than as prose, because the alternative is rendering
   * nothing where prose was promised and calling that success.
   */
  it('does not treat an ok state with a null map as prose', () => {
    const view = narrativeView(REPORT, ok({ narrative: null }))
    expect(view.kind).toBe('notice')
    expect(view).not.toHaveProperty('sections')
  })
})
