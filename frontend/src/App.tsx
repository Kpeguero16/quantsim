/**
 * Two states, one switch. There is no router: with exactly two views,
 * react-router-dom would be a dependency, a provider, and route config for
 * what a conditional does in one line (SPEC.md 2.9). The trade is no deep
 * links and no back button between views, neither of which Phase 1 needs.
 */
import LoginPage from './auth/LoginPage'
import { useAuth } from './auth/context'

export default function App() {
  const { user, logout } = useAuth()

  if (!user) return <LoginPage />

  // Task 4 replaces this panel with the dashboard.
  return (
    <main className="min-h-dvh px-4 py-10">
      <div className="mx-auto max-w-sm rounded-lg border border-line bg-surface p-6">
        <h1 className="text-lg font-semibold text-ink">
          Signed in as {user.username}
        </h1>
        <p className="mt-1 text-sm text-ink-muted">{user.email}</p>
        <button
          type="button"
          onClick={logout}
          className="mt-5 rounded-md border border-line-strong px-3 py-1.5 text-sm text-ink transition-colors hover:bg-elevated"
        >
          Sign out
        </button>
      </div>
    </main>
  )
}
