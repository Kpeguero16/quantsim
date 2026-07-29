/**
 * Session state, held entirely in memory.
 *
 * Neither token is written to localStorage, sessionStorage, or a cookie
 * (SPEC.md 2.5). An injected script therefore has to win a race against a
 * live page rather than reading a credential out of storage at leisure. The
 * accepted cost is that a page refresh logs you out; the production fix is
 * an HttpOnly refresh cookie, which is a backend change (the gateway sends
 * no CORS credentials today, so cookies are not even possible yet).
 *
 * Tokens live in refs rather than state: they are read by the API client's
 * bridge, not rendered, so storing them in state would only cause needless
 * re-renders of the whole tree on every refresh.
 */
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'

import { api, connectAuth } from '../api/client'
import type { LoginRequest, MeResponse, RegisterRequest, TokenPair } from '../api/types'
import { AuthContext, type AuthContextValue } from './context'

export function AuthProvider({ children }: { children: ReactNode }) {
  const accessToken = useRef<string | null>(null)
  const refreshToken = useRef<string | null>(null)
  const [user, setUser] = useState<MeResponse | null>(null)

  const clearSession = useCallback(() => {
    accessToken.current = null
    refreshToken.current = null
    setUser(null)
  }, [])

  // Wire the API client to this session. Done in an effect so it is set
  // before any interaction can trigger a request.
  useEffect(() => {
    connectAuth({
      getAccessToken: () => accessToken.current,
      getRefreshToken: () => refreshToken.current,
      onRefreshed: (pair: TokenPair) => {
        accessToken.current = pair.access_token
        refreshToken.current = pair.refresh_token
      },
      // The refresh token is spent or invalid -- there is no way back to an
      // authenticated state, so drop to the login screen rather than
      // leaving a signed-in shell that 401s on every action.
      onRefreshFailed: clearSession,
    })
    return () => connectAuth(null)
  }, [clearSession])

  const establish = useCallback(
    async (pair: TokenPair) => {
      accessToken.current = pair.access_token
      refreshToken.current = pair.refresh_token
      try {
        setUser(await api.me())
      } catch (error) {
        // Tokens that cannot fetch a profile are not a usable session.
        clearSession()
        throw error
      }
    },
    [clearSession],
  )

  const login = useCallback(
    async (request: LoginRequest) => establish(await api.login(request)),
    [establish],
  )

  const register = useCallback(
    async (request: RegisterRequest) => establish(await api.register(request)),
    [establish],
  )

  const value = useMemo<AuthContextValue>(
    () => ({ user, login, register, logout: clearSession }),
    [user, login, register, clearSession],
  )

  return <AuthContext value={value}>{children}</AuthContext>
}
