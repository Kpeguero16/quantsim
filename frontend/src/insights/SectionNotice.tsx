/**
 * A section that could not be computed: its heading, and the backend's own
 * explanation of why.
 *
 * Zero figures, deliberately. A degraded section still serializes every
 * figure it would have carried, because Step 20 left them without
 * omitempty on purpose -- a drawdown measured at 0% and a drawdown never
 * measured are different facts, and an omitted key reads as "not computed"
 * for both. So `max_drawdown_pct: 0` under insufficient_data is not a
 * measurement, and rendering it here would invent one (SPEC.md 2.5).
 *
 * `reason` is backend-authored, user-safe and written as a sentence, so it
 * is shown verbatim rather than mapped -- the same judgement
 * insights-errors.ts makes for symbol_unavailable.
 */
interface SectionNoticeProps {
  heading: string
  reason?: string
}

export default function SectionNotice({ heading, reason }: SectionNoticeProps) {
  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <h3 className="text-sm font-semibold text-ink">{heading}</h3>
      <p className="mt-2 text-sm text-ink-muted">
        {/* A section is only degraded WITH a reason today -- every
            insufficient_data path in the service sets one. The fallback is
            for the day one does not, where a bare heading over blank space
            would read as a rendering bug rather than as missing data. */}
        {reason ?? 'Not enough history to compute this yet.'}
      </p>
    </section>
  )
}
