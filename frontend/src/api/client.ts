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
  ApiErrorBody,
  HistoryResponse,
  LoginRequest,
  MeResponse,
  OrdersResponse,
  PlaceOrderRequest,
  PlaceOrderResult,
  PortfolioResponse,
  Price,
  PositionsResponse,
  RegisterRequest,
  SymbolsResponse,
  TokenPair,
} from './types'

const BASE_URL = (
  import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080'
).replace(/\/+$/, '')

/**
 * Upper bound on any single request. Without this a hung gateway leaves a
 * promise pending forever: the dashboard's per-symbol requests would pile
 * up tick after tick with nothing ever settling, and the UI would sit on a
 * loading state with no way out.
 *
 * Kept below the 15s poll interval so a stalled request is abandoned before
 * the next tick fires rather than overlapping with it.
 */
const REQUEST_TIMEOUT_MS = 10_000

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

/**
 * Turns a thrown fetch rejection into an ApiError. Status 0 marks "never
 * reached the server", so callers can tell a transport failure apart from
 * anything the backend actually said.
 */
function toTransportError(error: unknown): ApiError {
  if (error instanceof DOMException && error.name === 'TimeoutError') {
    return new ApiError(
      0,
      'timeout',
      'The server took too long to respond. Please try again.',
    )
  }
  return new ApiError(
    0,
    'network_error',
    'Could not reach the server. Check that the gateway is running.',
  )
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
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    })
  } catch {
    // Unreachable or timed out. Transient, and not evidence the token is
    // bad, so the session is left intact for the next attempt.
    return null
  }

  if (!response.ok) {
    bridge?.onRefreshFailed()
    return null
  }

  let pair: TokenPair
  try {
    // The timeout covers body streaming too, so this read can still abort
    // even though the response headers arrived.
    pair = (await response.json()) as TokenPair
  } catch {
    // Same reasoning as the fetch failure above: transient, and no
    // evidence the refresh token itself is bad.
    return null
  }

  bridge?.onRefreshed(pair)
  return pair.access_token
}

async function toApiError(response: Response): Promise<ApiError> {
  let code = 'unknown_error'
  let message = `Request failed with status ${response.status}`

  try {
    const body: unknown = await response.json()
    if (body && typeof body === 'object') {
      // Validated field by field rather than cast outright: this is data
      // off the wire, so the declared shape is a claim to check, not a
      // guarantee to trust.
      const parsed = body as Partial<Record<keyof ApiErrorBody, unknown>>
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
        // A fresh signal per attempt: the retry below must not inherit an
        // already-fired timeout from the first attempt.
        signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
      })
    } catch (error) {
      throw toTransportError(error)
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

  try {
    return (await response.json()) as T
  } catch (error) {
    // The timeout covers body streaming, so a slow response can abort here
    // rather than at the fetch above. Anything else at this point is a
    // malformed body, which is a different failure and says so.
    if (error instanceof DOMException) throw toTransportError(error)
    throw new ApiError(
      0,
      'invalid_response',
      'The server returned a malformed response.',
    )
  }
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

  // Unauthenticated for the same reason refresh is: by the time someone
  // signs out, the access token may already be expired. The refresh token
  // in the body is what's actually being revoked, and what authorizes the
  // call (SPEC.md Step 13, 2.5).
  logout: (refreshToken: string) =>
    request<Record<string, never>>('/auth/logout', {
      method: 'POST',
      body: { refresh_token: refreshToken },
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

  placeOrder: (body: PlaceOrderRequest) =>
    request<PlaceOrderResult>('/trading/orders', { method: 'POST', body }),

  orders: () => request<OrdersResponse>('/trading/orders'),

  positions: () => request<PositionsResponse>('/trading/positions'),

  portfolio: () => request<PortfolioResponse>('/trading/portfolio'),
}
