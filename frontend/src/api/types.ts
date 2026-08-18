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
 *   services/trading-engine/internal/service/types.go
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

/**
 * Trading types, from services/trading-engine/internal/service/types.go.
 *
 * `filled_price`, `rejection_reason`, `realized_pl`, and `latest_price` --
 * and only those four -- are nullable here because they're pointers on the
 * Go side: at most one of a fill/rejection pair is ever set, and a market
 * price the backend could not reach is null rather than a misleading 0.
 */
export type Side = 'buy' | 'sell'

/** One row of order history, filled or rejected. */
export interface Order {
  id: string
  symbol: string
  side: Side
  quantity: number
  status: string
  order_type: string
  filled_price: number | null
  rejection_reason: string | null
  created_at: string
}

/** One execution. Only sells carry realized_pl. */
export interface Trade {
  id: string
  order_id: string
  symbol: string
  side: Side
  quantity: number
  price: number
  realized_pl: number | null
  executed_at: string
}

/** A holding priced at the current market, as returned by GET /trading/positions. */
export interface Position {
  symbol: string
  quantity: number
  avg_cost: number
  latest_price: number | null
  unrealized_pl: number
}

/** POST /trading/orders */
export interface PlaceOrderRequest {
  symbol: string
  side: Side
  quantity: number
}

/** Response of POST /trading/orders. balance is the post-trade balance. */
export interface PlaceOrderResult {
  order: Order
  trade: Trade
  balance: number
}

/** GET /trading/orders */
export interface OrdersResponse {
  orders: Order[]
}

/** GET /trading/positions */
export interface PositionsResponse {
  positions: Position[]
}

/** GET /trading/portfolio */
export interface PortfolioResponse {
  balance: number
  positions: Position[]
  total_equity: number
  total_unrealized_pl: number
}
