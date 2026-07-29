/**
 * Wire types for the QuantSim API.
 *
 * Field names are snake_case on purpose -- they mirror the Go json tags
 * byte for byte, so the two sides can be diffed by eye and there is no
 * remapping layer for a typo to hide in (SPEC.md 5).
 *
 * Sources of truth:
 *   services/auth/internal/service/types.go
 *   services/market-data/internal/service/types.go
 */

/** POST /auth/register */
export interface RegisterRequest {
  email: string
  username: string
  password: string
}

/** POST /auth/login */
export interface LoginRequest {
  email: string
  password: string
}

/** Returned by register, login, and refresh. expires_in is seconds (900). */
export interface TokenPair {
  access_token: string
  refresh_token: string
  expires_in: number
}

/** GET /auth/me */
export interface MeResponse {
  id: string
  email: string
  username: string
  created_at: string
  updated_at: string
}

/** GET /market-data/symbols */
export interface SymbolsResponse {
  symbols: string[]
}

/** One daily OHLC candle. timestamp is RFC3339. */
export interface Bar {
  timestamp: string
  open: number
  high: number
  low: number
  close: number
  volume: number
}

/**
 * GET /market-data/history/:symbol
 *
 * `bars` is never null: the service normalises a nil slice to an empty one
 * before marshalling (market_data.go:155), so an un-ingested symbol comes
 * back as `[]` rather than `null`.
 */
export interface HistoryResponse {
  symbol: string
  timeframe: string
  bars: Bar[]
}

/** GET /market-data/prices/:symbol */
export interface Price {
  symbol: string
  price: number
  timestamp: string
}

/** The error shape every QuantSim service returns for any 4xx/5xx. */
export interface ApiErrorBody {
  code: string
  message: string
}
