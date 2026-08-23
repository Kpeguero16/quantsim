/**
 * The Step 21 narrative. Fetched once on mount, and after that only when
 * the reader asks for a new one.
 *
 * Three things about this hook are deliberate and would each be a bug if
 * written the obvious way instead:
 *
 * 1. It takes NO arguments and depends on nothing. In particular it does
 *    not take the report, and no effect here re-fires when the report
 *    changes. Generation is billed per call and the cache is keyed on the
 *    report's content hash, so a report that just changed is guaranteed to
 *    miss and guaranteed to cost. An effect keyed on the report would turn
 *    every fill into a paid API call for prose nobody asked to re-read
 *    (SPEC.md 2.9). The panel handles a changed report by hiding the stale
 *    prose, not by buying new prose.
 *
 * 2. A 200 carrying `state: 'unavailable'` is a SUCCESS, not an error. It
 *    is the ordinary response when generation is switched off or capped,
 *    and it is the response whose `reason` the panel maps to copy. That
 *    falls out of the client throwing only on a non-2xx rather than out of
 *    a branch here, which is why there is no branch here.
 *
 * 3. The error state carries NO message. A request that never produced a
 *    body and a 200 that says "the narrative service is unavailable" are
 *    the same fact to a reader -- it broke, the figures are fine, try
 *    again -- so both are spelled by narrativeNotice and neither is spelled
 *    here. Two copies of one sentence in two files is how they drift.
 *
 * Same request-id race guard as the other hooks.
 */
import { useCallback, useEffect, useRef, useState } from 'react'

import { api } from '../api/client'
import type { NarrativeResponse } from '../api/types'

export type NarrativeState =
  | { status: 'loading' }
  | { status: 'ok'; narrative: NarrativeResponse }
  | { status: 'error' }

export function useNarrative(): NarrativeState & { regenerate: () => void } {
  const [state, setState] = useState<NarrativeState>({ status: 'loading' })

  const requestIdRef = useRef(0)

  /**
   * True from the moment a request starts until it settles.
   *
   * The regenerate control is already absent while a generation is in
   * flight, and `disabled` would be a third layer -- but both of those act
   * on the NEXT render, and two clicks can land inside one frame. This
   * guard is checked synchronously in the click handler itself, which is
   * the only place that can actually stop the second call before it is
   * made. It is here rather than in the component because the thing being
   * protected is a billed API call, not a button (SPEC.md 2.9).
   */
  const inFlightRef = useRef(false)

  /**
   * The id of the request inFlightRef is tracking, so a re-run of the mount
   * effect can re-adopt it. See the re-adopt branch in `load` -- without
   * this the two guards below deadlock and the hook never leaves 'loading'.
   */
  const inFlightIdRef = useRef(0)

  /**
   * Unlike the other hooks, this ALWAYS goes back to 'loading' rather than
   * holding a previous good value. It has exactly two callers: mount, where
   * there is nothing held; and the regenerate control, which is only
   * offered when what is held is stale or failed. Keeping stale prose on
   * screen while its replacement is in flight would show figures that no
   * longer describe the report -- the one thing SPEC.md 2.3 forbids -- and
   * it is what makes `status === 'loading'` sufficient to disable the
   * control, so a double-click cannot buy two generations (SPEC.md 2.9).
   */
  const load = useCallback(() => {
    if (inFlightRef.current) {
      /**
       * Re-adopt the in-flight request rather than returning empty-handed.
       *
       * WHY this is not just `return`. The mount effect's cleanup bumps
       * requestIdRef to disown any in-flight response, which is right when
       * the component is going away for good. But React re-runs that effect
       * on the SAME instance -- StrictMode does it on every mount in
       * development -- and the refs survive, so the sequence is: request
       * starts (id 1), cleanup bumps to 2, effect re-runs, and this guard
       * blocks the replacement. The response then arrives to find 2 != 1
       * and is discarded, leaving the hook on 'loading' with nothing left
       * to re-trigger it. The panel sits on "Preparing a written summary..."
       * forever, which is exactly what the browser showed.
       *
       * Restoring the id accepts that response instead. It cannot buy a
       * second generation -- no request is issued on this path -- so the
       * double-spend guard this branch belongs to is untouched.
       */
      requestIdRef.current = inFlightIdRef.current
      return
    }
    inFlightRef.current = true

    const requestId = ++requestIdRef.current
    inFlightIdRef.current = requestId
    setState({ status: 'loading' })

    api
      .narrative()
      .then((narrative) => {
        if (requestIdRef.current !== requestId) return
        setState({ status: 'ok', narrative })
      })
      .catch(() => {
        if (requestIdRef.current !== requestId) return
        setState({ status: 'error' })
      })
      .finally(() => {
        inFlightRef.current = false
      })
  }, [])

  useEffect(() => {
    load()
    return () => {
      // Past any in-flight request's id, so its resolution is disowned --
      // unless the effect re-runs on this same instance, in which case
      // `load` restores it rather than letting the response go to waste.
      //
      // Unlike its three sibling hooks this line raises no oxlint
      // exhaustive-deps warning, because `requestIdRef.current` is now
      // written in `load` as well and the rule stops treating it as a
      // cleanup-only ref. The rule was always aimed at DOM node refs rather
      // than a plain instance counter; use-portfolio.ts, use-orders.ts and
      // use-backtests.ts still carry the warning and the same reasoning.
      requestIdRef.current++
    }
  }, [load])

  return { ...state, regenerate: load }
}
