/**
 * Two states, one switch. There is no router: with exactly two views,
 * react-router-dom would be a dependency, a provider, and route config for
 * what a conditional does in one line (SPEC.md 2.9). The trade is no deep
 * links and no back button between views, neither of which Phase 1 needs.
 */
import LoginPage from './auth/LoginPage'
import { useAuth } from './auth/context'
import Dashboard from './market/Dashboard'

export default function App() {
  const { user } = useAuth()

  return user ? <Dashboard /> : <LoginPage />
}
