/**
 * The single place this app talks to the network.
 *
 * Everything goes through the gateway on one origin -- the frontend never
 * knows that auth lives on :8081 and market-data on :8082 (SPEC.md 2.3).
 * Nothing above this module calls fetch directly.
 *
 * The interesting part is 401 handling (SPEC.md 2.6). Access tokens last 15
 * minutes and neither token is persisted, so a dashboard left open will
 * start 401ing while the refresh token is still good in memory. This module
 * recovers silently, with three constraints that are easy to get wrong:
 *
 *   1. The refresh call itself bypasses this interceptor. If it did not, an
 *      expired refresh token would 401 and trigger another refresh, forever.
 *   2. A request is retried exactly once. A second 401 after a successful
 *      refresh means something other than expiry is wrong.
 *   3. Concurrent 401s share one refresh. The dashboard fires seven price
 *      requests per tick, so an expiry mid-tick 401s all seven at once;
 *      without a shared in-flight promise that is seven parallel refreshes.
 *
 * On (3): the backend's refresh is stateless with no rotation, so duplicate
 * refreshes are wasteful rather than incorrect *today*. If Phase 2 adds
 * refresh-token rotation this becomes a correctness requirement -- do not
 * remove it as an unnecessary optimisation.
 */
import type {
  HistoryResponse,
  LoginRequest,
  MeResponse,
  Price,
  RegisterRequest,
  SymbolsResponse,
  TokenPair,
} from './types'

const BASE_URL = (
  import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080'
).replace(/\/+$/, '')

/** A failed request, carrying the backend's {code, message} body. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

/**
 * True when the price cache simply has no entry for a symbol yet -- markets
 * closed, or the poller has not run. Routine, not a failure: the dashboard
 * renders a dash and the row stays healthy (SPEC.md 2.7).
 */
export function isPriceNotCached(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status === 404 &&
    error.code === 'price_not_cached'
  )
}

/**
 * How this module reaches the auth layer. Injected rather than imported:
 * AuthContext calls the client, so importing it back here would be a cycle.
 */
export interface AuthBridge {
  getAccessToken: () => string | null
  getRefreshToken: () => string | null
  /** A refresh succeeded -- store the new pair. */
  onRefreshed: (pair: TokenPair) => void
  /** The refresh token is no longer good -- clear the session. */
  onRefreshFailed: () => void
}

let bridge: AuthBridge | null = null

export function connectAuth(next: AuthBridge | null): void {
  bridge = next
}

let inFlightRefresh: Promise<string | null> | null = null

/** Shared-promise wrapper: concurrent callers await one refresh. */
function refreshAccessToken(): Promise<string | null> {
  if (inFlightRefresh) return inFlightRefresh

  const pending = performRefresh().finally(() => {
    // Cleared once settled so a later expiry can refresh again.
    if (inFlightRefresh === pending) inFlightRefresh = null
  })
  inFlightRefresh = pending
  return pending
}

async function performRefresh(): Promise<string | null> {
  const refreshToken = bridge?.getRefreshToken() ?? null
  if (!refreshToken) return null

  let response: Response
  try {
    // Raw fetch on purpose -- see constraint (1) in the module comment.
    response = await fetch(`${BASE_URL}/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
  } catch {
    // The gateway is unreachable. Transient, and not evidence the token is
    // bad, so the session is left intact for the next attempt.
    return null
  }

  if (!response.ok) {
    bridge?.onRefreshFailed()
    return null
  }

  const pair = (await response.json()) as TokenPair
  bridge?.onRefreshed(pair)
  return pair.access_token
}

async function toApiError(response: Response): Promise<ApiError> {
  let code = 'unknown_error'
  let message = `Request failed with status ${response.status}`

  try {
    const body: unknown = await response.json()
    if (body && typeof body === 'object') {
      const parsed = body as Partial<{ code: unknown; message: unknown }>
      if (typeof parsed.code === 'string') code = parsed.code
      if (typeof parsed.message === 'string') message = parsed.message
    }
  } catch {
    // Not JSON. Every QuantSim service returns {code, message} for every
    // 4xx/5xx, so this means something outside the stack answered.
  }

  return new ApiError(response.status, code, message)
}

interface RequestOptions {
  method?: string
  body?: unknown
  /** Send the bearer token and participate in refresh-retry. Default true. */
  authenticated?: boolean
}

async function request<T>(
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const { method = 'GET', body, authenticated = true } = options

  const send = async (token: string | null): Promise<Response> => {
    const headers: Record<string, string> = {}
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    if (token) headers.Authorization = `Bearer ${token}`

    try {
      return await fetch(`${BASE_URL}${path}`, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
      })
    } catch {
      throw new ApiError(
        0,
        'network_error',
        'Could not reach the server. Check that the gateway is running.',
      )
    }
  }

  let response = await send(
    authenticated ? (bridge?.getAccessToken() ?? null) : null,
  )

  if (response.status === 401 && authenticated) {
    const token = await refreshAccessToken()
    // Exactly one retry -- constraint (2) in the module comment.
    if (token) response = await send(token)
  }

  if (!response.ok) throw await toApiError(response)

  return (await response.json()) as T
}

export const api = {
  // Public: you cannot present a token before you have one.
  register: (body: RegisterRequest) =>
    request<TokenPair>('/auth/register', {
      method: 'POST',
      body,
      authenticated: false,
    }),

  login: (body: LoginRequest) =>
    request<TokenPair>('/auth/login', {
      method: 'POST',
      body,
      authenticated: false,
    }),

  // Proxied publicly by the gateway, but the auth service enforces its own
  // middleware on this route -- so it needs the bearer token.
  me: () => request<MeResponse>('/auth/me'),

  symbols: () => request<SymbolsResponse>('/market-data/symbols'),

  price: (symbol: string) =>
    request<Price>(`/market-data/prices/${encodeURIComponent(symbol)}`),

  history: (symbol: string) =>
    request<HistoryResponse>(
      `/market-data/history/${encodeURIComponent(symbol)}`,
    ),
}
