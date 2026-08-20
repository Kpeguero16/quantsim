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
 *   services/backtesting/internal/service/types.go
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

/**
 * Backtesting types, from services/backtesting/internal/service/types.go.
 *
 * `profit_factor` is nullable for the same reason `filled_price` etc. are
 * above: it's a pointer on the Go side, null (not 0 or Infinity) when there
 * are no losing trades to divide by.
 */

/** One simulated fill, part of a BacktestDetail's trade log. `realized_pl`
 * is set only on sells. */
export interface TradeRecord {
  /** Which symbol this fill belongs to. A portfolio run interleaves fills
   * across symbols in one log, and it is populated at every run size --
   * including 1 -- so it never has to be rendered conditionally. */
  symbol: string
  side: Side
  bar_timestamp: string
  price: number
  quantity: number
  realized_pl: number | null
}

/** The five metrics agents.md defines, computed for one backtest run. */
export interface Metrics {
  total_return_pct: number
  sharpe_ratio: number
  max_drawdown_pct: number
  win_rate_pct: number
  profit_factor: number | null
}

/** Which named strategy a backtest ran, from
 * services/backtesting/internal/service/strategy.go's StrategyKind. */
export type StrategyKind = 'ma_crossover' | 'rsi' | 'macd'

export interface MACrossoverParams {
  short_window: number
  long_window: number
}

export interface RSIParams {
  period: number
  oversold: number
  overbought: number
}

export interface MACDParams {
  fast_period: number
  slow_period: number
  signal_period: number
}

export type BacktestParams = MACrossoverParams | RSIParams | MACDParams

/** strategy discriminates params' shape -- narrowing on `backtest.strategy`
 * (e.g. `=== 'rsi'`) narrows `backtest.params` to the matching interface,
 * the same way the Go side's json.RawMessage is only ever interpreted once
 * NewStrategy has already looked at the kind (Step 18 SPEC.md 2.6). */
interface BacktestBase {
  id: string
  /** Always at least one, uppercased and sorted alphabetically by the server
   * (Step 19 SPEC.md 2.4). A single-symbol run is the length-1 case of this,
   * not a separate shape, so nothing here is optional or nullable. */
  symbols: string[]
  start_date: string
  end_date: string
  starting_capital: number
  final_equity: number
  metrics: Metrics
  created_at: string
}

/** One persisted backtest run's parameters and metrics -- no trade log
 * (that's BacktestDetail below). Returned by GET /backtests. */
export type Backtest =
  | (BacktestBase & { strategy: 'ma_crossover'; params: MACrossoverParams })
  | (BacktestBase & { strategy: 'rsi'; params: RSIParams })
  | (BacktestBase & { strategy: 'macd'; params: MACDParams })

/** GET /backtests/{id} and the response of POST /backtests. Backtest's
 * fields plus the simulated trade log. */
export type BacktestDetail = Backtest & { trades: TradeRecord[] }

interface RunBacktestRequestBase {
  /** 1..10 symbols. The server uppercases, sorts and rejects duplicates
   * case-insensitively, so what comes back may not be the order sent. */
  symbols: string[]
  /** YYYY-MM-DD calendar dates. */
  start_date: string
  end_date: string
  starting_capital: number
}

/** POST /backtests. Mirrors Backtest's discriminated shape -- the request
 * that produced a run and the run itself agree on what params means for a
 * given strategy. */
export type RunBacktestRequest =
  | (RunBacktestRequestBase & { strategy: 'ma_crossover'; params: MACrossoverParams })
  | (RunBacktestRequestBase & { strategy: 'rsi'; params: RSIParams })
  | (RunBacktestRequestBase & { strategy: 'macd'; params: MACDParams })

/** GET /backtests */
export interface BacktestsResponse {
  backtests: Backtest[]
}
