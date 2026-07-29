/**
 * Task 1 placeholder. Exists to prove the Tailwind v4 pipeline and the design
 * tokens in index.css actually render; Task 3 replaces this with the real
 * logged-out / logged-in switch.
 */
export default function App() {
  return (
    <main className="min-h-dvh grid place-items-center p-6">
      <div className="w-full max-w-md rounded-lg border border-line bg-surface p-6">
        <h1 className="text-xl font-semibold text-ink">QuantSim</h1>
        <p className="mt-1 text-sm text-ink-muted">
          Frontend scaffold is up. Design tokens below should render as
          described.
        </p>

        <dl className="mt-5 space-y-2 text-sm">
          <div className="flex items-center justify-between gap-4">
            <dt className="text-ink-muted">Rising</dt>
            <dd className="tabular font-mono text-up">&uarr; 182.41</dd>
          </div>
          <div className="flex items-center justify-between gap-4">
            <dt className="text-ink-muted">Falling</dt>
            <dd className="tabular font-mono text-down">&darr; 411.09</dd>
          </div>
          <div className="flex items-center justify-between gap-4">
            <dt className="text-ink-muted">Not cached</dt>
            <dd className="tabular font-mono text-ink-subtle">&mdash;</dd>
          </div>
        </dl>

        <button
          type="button"
          className="mt-6 w-full rounded-md bg-accent px-4 py-2 text-sm font-medium text-canvas transition-colors hover:bg-accent-hover"
        >
          Accent button
        </button>
      </div>
    </main>
  )
}
