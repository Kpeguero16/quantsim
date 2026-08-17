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

import { ApiError, api, connectAuth } from '../api/client'
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
        // Only discard the pair when the server actually rejected it. A
        // transport failure or a 5xx says nothing about whether these
        // tokens are good, and throwing away a pair that was valid a
        // moment ago turns one flaky request into a forced second
        // sign-in. Either way the caller still sees the error and the
        // user stays on the login screen, since `user` was never set.
        if (
          error instanceof ApiError &&
          (error.status === 401 || error.status === 403)
        ) {
          clearSession()
        }
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

  // Captures the refresh token before clearSession nulls it, then clears
  // immediately so the UI reacts instantly regardless of network timing.
  // Revocation is best-effort after that: a failed or slow server call must
  // never block or visibly fail the sign-out button -- clearing local state
  // is genuinely all this can promise either way (SPEC.md Step 13, 2.3).
  const logout = useCallback(() => {
    const token = refreshToken.current
    clearSession()
    if (token) {
      void api.logout(token).catch(() => {
        // Best-effort. The local session is already cleared.
      })
    }
  }, [clearSession])

  const value = useMemo<AuthContextValue>(
    () => ({ user, login, register, logout }),
    [user, login, register, logout],
  )

  return <AuthContext value={value}>{children}</AuthContext>
}
