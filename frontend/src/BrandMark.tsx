/**
 * The QuantSim logo mark: a rising line with a terminal dot, in the same
 * green as a rising candle (--color-up), so the brand and the charts share
 * one palette.
 *
 * Decorative by default. Every current call site renders it immediately
 * beside the "QuantSim" wordmark, so labelling the SVG too would make a
 * screen reader announce the name twice. If it is ever used *without*
 * adjacent text, give that instance an accessible name at the call site.
 */
interface BrandMarkProps {
  /** Tailwind sizing classes. Callers set this; there is no default size. */
  className: string
}

export default function BrandMark({ className }: BrandMarkProps) {
  return (
    <svg
      viewBox="0 0 32 32"
      className={className}
      aria-hidden="true"
      focusable="false"
    >
      <rect width="32" height="32" rx="6" className="fill-elevated" />
      <path
        d="M5 22.5 L12 15 L17 19 L27 8"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.75"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="text-up"
      />
      <circle cx="27" cy="8" r="2.75" className="fill-up" />
    </svg>
  )
}
