/**
 * One section's prose, underneath that section's figures.
 *
 * Renders nothing at all when there is no prose for this section -- the
 * narrative is a map, and a section the model could not usefully describe
 * is an absent key rather than an empty string. Nothing here decides
 * WHETHER prose may be shown; narrativeView already did, and this
 * component is only reachable with sections it approved.
 */
interface NarrativeBlockProps {
  text: string | undefined
}

export default function NarrativeBlock({ text }: NarrativeBlockProps) {
  if (!text) return null

  return (
    <p className="border-t border-line px-4 py-3 text-sm leading-relaxed text-ink-muted">
      {text}
    </p>
  )
}
