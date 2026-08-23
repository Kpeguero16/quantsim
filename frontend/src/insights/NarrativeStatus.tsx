/**
 * The one line explaining why there is no prose, and the control that buys
 * some.
 *
 * Once for the whole panel rather than once per section: a narrative
 * describes the whole report, so three copies of "a written summary isn't
 * available right now" would be one fact stated three times.
 *
 * Below the figures, not above them. The copy says the figures above are
 * unaffected, which is only true if they are actually above -- and this is
 * the one place in the app where a visible failure sits directly beneath
 * correct data, so the geometry is part of the sentence.
 *
 * The control is ABSENT rather than disabled while a generation is in
 * flight: the panel passes retry=false for a pending view. That is
 * stronger than greying it out, and the hook holds the real guard anyway
 * (use-narrative.ts), because a disabled attribute only applies on the
 * next render and this is a button that spends money.
 */
interface NarrativeStatusProps {
  text: string
  retry: boolean
  onRegenerate: () => void
}

export default function NarrativeStatus({
  text,
  retry,
  onRegenerate,
}: NarrativeStatusProps) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line bg-surface px-4 py-3">
      <p className="text-sm text-ink-muted">{text}</p>
      {retry && (
        <button
          type="button"
          onClick={onRegenerate}
          className="rounded-md border border-line-strong px-3 py-1.5 text-sm text-ink transition-colors hover:bg-elevated"
        >
          Write a new summary
        </button>
      )}
    </div>
  )
}
