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
 *   services/ai-insights/internal/service/{insights,risk,benchmark,behavior}.go
 *   services/ai-insights/internal/handler/{insights,narrative}.go
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

/** One holding's share of the portfolio. */
export interface PositionWeight {
  symbol: string
  quantity: number
  market_value: number
  weight_pct: number
}

/**
 * A section that could not be computed: its reason, and nothing measured.
 *
 * WHY this arm omits the figures even though the wire still sends them.
 * Step 20 deliberately left them without omitempty, so a degraded section
 * really does serialize `max_drawdown_pct: 0` (Step 20 SPEC.md 2.5) -- and
 * that was the right call there: a portfolio whose max drawdown was
 * measured at 0% and one whose drawdown was never measured are different
 * facts, and an omitted key reads as "not computed" for both. It does mean
 * every such zero is a number nothing measured. These types therefore model
 * what may be READ, not what arrives on the wire, which is the one place in
 * this file where the two diverge on purpose.
 *
 * Modelling it the other way -- one flat interface mirroring the wire --
 * is what the frontend shipped first, and an adversarial pass (T10) showed
 * why it does not hold: deleting a section's `state` guard together with
 * its now-unused SectionNotice import, which is precisely what an IDE's
 * remove-unused-imports fix does unprompted, left `npm test`, `npm run
 * build` and `npm run lint` all green while the page rendered 0.0% for
 * five figures. The union makes that a compile error that names each
 * invented figure, which is the same trade SPEC.md 2.3 made for the
 * composition race: pay a few lines to make a silent wrong number
 * impossible rather than merely unlikely.
 */
export interface DegradedSection {
  state: 'insufficient_data'
  /** Backend-authored and written to be read by a user, so it is displayed
   * verbatim. */
  reason?: string
}

/** Concentration, volatility and drawdown, as of the last trading day. */
export interface RiskFigures {
  state: 'ok'
  /** Never set on this arm. Present so that `section.reason` still
   * narrows cleanly at a call site that reads it before branching. */
  reason?: undefined
  /** Always an array, never null -- the service initializes it on every
   * path including the degraded ones, and a handler test pins it. */
  positions: PositionWeight[]
  cash_weight_pct: number
  concentration_hhi: number
  largest_position_pct: number
  annualized_volatility_pct: number
  max_drawdown_pct: number
}

export type RiskSection = RiskFigures | DegradedSection

/**
 * One buy-and-hold comparison. excess_return_pct is a simple difference
 * against the portfolio, not a regression alpha.
 */
export interface Benchmark {
  symbol: string
  label: string
  return_pct: number
  excess_return_pct: number
  sharpe: number
}

export interface BenchmarkingFigures {
  state: 'ok'
  reason?: undefined
  portfolio_return_pct: number
  portfolio_sharpe: number
  /** Always an array, never null -- initialized on every path. */
  benchmarks: Benchmark[]
}

export type BenchmarkingSection = BenchmarkingFigures | DegradedSection

/**
 * One behavioural rule that fired.
 *
 * turnover_ratio and occurrences are optional because each belongs to one
 * kind of finding and is genuinely absent from the other -- Go's omitempty
 * is correct here rather than a hazard, because neither zero is reachable
 * in a finding that exists (overtrading fires above a ratio threshold,
 * panic selling at one occurrence or more).
 *
 * `detail` is prose for a human and must never be parsed -- the backend
 * says so at the struct, and this is the layer that would be tempted to.
 */
export interface Finding {
  code: 'overtrading' | 'panic_selling'
  severity: 'info' | 'warn'
  detail: string
  turnover_ratio?: number
  occurrences?: number
  /** Carried so the narrative can cite specifics without inventing them.
   * Not rendered here, not linked, and not counted (SPEC.md 2.8). */
  evidence_trade_ids: string[]
}

export interface BehaviorFigures {
  state: 'ok'
  reason?: undefined
  trade_count: number
  /** Always an array, never null -- initialized on every path. */
  findings: Finding[]
  /** The one legitimately-absent field in this response: it is derived from
   * the other two sections, so when they cannot be computed it was never
   * measured. Absent means absent -- not "unknown", not a default band. */
  risk_profile?: 'aggressive' | 'moderate' | 'conservative'
}

export type BehaviorSection = BehaviorFigures | DegradedSection

/** The measured window. trading_days counts dates in the reconstruction
 * calendar, not weekdays elapsed. */
export interface Window {
  start_date?: string
  trading_days: number
}

/**
 * GET /insights/portfolio.
 *
 * computed_at is cache age; as_of_date is data age. A response can be
 * freshly computed from bars that are a day old, and one field cannot say
 * both -- so both are here and they are not interchangeable.
 *
 * report_hash identifies the report by its content. It is what lets a
 * separately-fetched narrative be checked against the figures actually on
 * screen (SPEC.md 2.3); without it the two could disagree with nothing to
 * detect it. Not optional on this endpoint -- unlike the narrative's copy
 * of it, this one is always sent.
 */
export interface PortfolioInsightsResponse {
  computed_at: string
  as_of_date?: string
  window: Window
  risk: RiskSection
  benchmarking: BenchmarkingSection
  behavior: BehaviorSection
  /** Last, because the Go handler embeds the report and appends this -- so
   * the wire order is the report's fields then this one, and keeping the
   * same order here is what lets the two files be diffed by eye. */
  report_hash: string
}

/**
 * GET /insights/portfolio/narrative.
 *
 * `narrative` is null rather than {} when there is no prose, so a caller
 * can tell "there is no narrative" from "there is a narrative describing
 * nothing". Keys are section names -- risk, benchmarking, behavior -- and a
 * section the model could not usefully describe is an absent key rather
 * than an empty string.
 *
 * report_hash is optional HERE and required on the report: the degraded
 * response carries one, but nothing in the type system forces it to, and
 * treating an absent hash as agreement would silently defeat the check it
 * exists for. See describesReport in insights/narrative-state.ts.
 */
export interface NarrativeResponse {
  as_of_date?: string
  report_hash?: string
  state: 'ok' | 'unavailable'
  narrative: Record<string, string> | null
  /** Present only when state is unavailable. One of a closed set of nine,
   * mapped to user copy rather than shown raw (SPEC.md 2.6). */
  reason?: string
  model?: string
  generated_at?: string
}
