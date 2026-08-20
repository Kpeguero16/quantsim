package service

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Bar is one OHLC daily bar, as returned by market-data's history endpoint.
// Volume is carried for parity with market-data.Bar even though no signal or
// metric in this step reads it.
type Bar struct {
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    int64
}

// Side mirrors trading-engine's Side (SPEC.md §2.6: "the same shape as
// trades"), but this package does not import trading-engine's -- these are
// simulated fills against a hypothetical balance, not real money, and the
// two must stay free to diverge without one service's schema change forcing
// the other's.
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// Signal is what GenerateSignals decides for one bar, from data available
// only through that bar's close (SPEC.md §2.4). It is not itself a fill --
// Simulate is what turns a Signal into a trade, one bar later.
type Signal int

const (
	SignalNone Signal = iota
	SignalBuy
	SignalSell
)

// StrategyParams is one backtest's configuration -- the request body of
// POST /backtests, its strategy already constructed and validated by
// NewStrategy, plus the resolved date range. Internal only, never encoded --
// validateRequest's output, not a wire type. Carrying the constructed
// Strategy itself (SPEC.md Step 18 §2.1), rather than raw parameters, is
// what lets RunBacktest call WarmupBars() and GenerateSignals() without
// caring which strategy it got.
//
// Symbols is validateRequest's normalized list -- uppercased, duplicate-free
// and sorted alphabetically (Step 19 plan D4) -- never the order a client
// sent. The same symbol set in a different order therefore produces an
// identical run, down to the stored row (SPEC.md Step 19 §2.2).
type StrategyParams struct {
	Symbols         []string
	Strategy        Strategy
	StartDate       time.Time
	EndDate         time.Time
	StartingCapital float64
}

// TradeRecord is one simulated fill, produced by SimulatePortfolio and
// returned as part of BacktestDetail. RealizedPL is set only on sells -- the
// same "at most one of these is ever meaningful, encode it as a pointer rather
// than a plausible-looking zero" rule trading-engine's Trade already follows.
//
// Symbol is populated for every trade, including a single-symbol run's (Step
// 19 SPEC.md §2.3): a single-symbol backtest is the len(symbols)==1 case of a
// portfolio run, not a separate mode, so there is no trade this engine can
// produce that legitimately lacks a symbol. One honest shape, no field that is
// meaningful only sometimes.
type TradeRecord struct {
	Symbol       string    `json:"symbol"`
	Side         Side      `json:"side"`
	BarTimestamp time.Time `json:"bar_timestamp"`
	Price        float64   `json:"price"`
	Quantity     float64   `json:"quantity"`
	RealizedPL   *float64  `json:"realized_pl"`
}

// SimulationResult is Simulate's output: the trade log and the bar-by-bar
// mark-to-market equity curve ComputeMetrics reads. FinalEquity is carried
// separately even though it always equals EquityCurve's last element, so a
// caller never has to special-case an empty curve to find it.
type SimulationResult struct {
	Trades      []TradeRecord
	EquityCurve []float64
	FinalEquity float64
}

// Metrics is the five values agents.md §3 names, computed by ComputeMetrics.
//
// ProfitFactor is a pointer: with no losing trades the ratio has no
// denominator, and returning 0 or +Inf would both read as a real number
// instead of "not computable" (SPEC.md §2.5) -- the same null-over-a-
// plausible-number rule the frontend already applies to price data.
type Metrics struct {
	TotalReturnPct float64  `json:"total_return_pct"`
	SharpeRatio    float64  `json:"sharpe_ratio"`
	MaxDrawdownPct float64  `json:"max_drawdown_pct"`
	WinRatePct     float64  `json:"win_rate_pct"`
	ProfitFactor   *float64 `json:"profit_factor"`
}

// Backtest is one persisted run: its parameters and its five metrics
// together, so a list read needs no join (SPEC.md §2.6). Strategy/Params
// replace Step 16's ShortWindow/LongWindow (Step 18 SPEC.md §2.5/§2.6) --
// Params is always the canonical re-encoding Strategy.Params() produced,
// never the raw bytes a client sent, so a stored run is always readable by
// NewStrategy.
//
// Symbols is a JSON array, never null: every write path validates a non-empty
// list, and the store's scan path materializes an empty array as []string{}
// rather than a nil slice, so a client never has to distinguish "no symbols"
// from "the field is absent".
type Backtest struct {
	ID              uuid.UUID       `json:"id"`
	UserID          uuid.UUID       `json:"-"`
	Symbols         []string        `json:"symbols"`
	Strategy        StrategyKind    `json:"strategy"`
	Params          json.RawMessage `json:"params"`
	StartDate       time.Time       `json:"start_date"`
	EndDate         time.Time       `json:"end_date"`
	StartingCapital float64         `json:"starting_capital"`
	FinalEquity     float64         `json:"final_equity"`
	Metrics         Metrics         `json:"metrics"`
	CreatedAt       time.Time       `json:"created_at"`
}

// BacktestDetail is one run plus its simulated trade log, returned by
// GET /backtests/{id}. Never listed on its own -- SPEC.md §2.6. Backtest is
// embedded so its fields marshal at the top level, with "trades" added
// alongside rather than nested under a "backtest" key.
type BacktestDetail struct {
	Backtest
	Trades []TradeRecord `json:"trades"`
}

// RunBacktestRequest is the body of POST /backtests. Strategy and Params
// form a discriminated union (SPEC.md Step 18 §2.6) -- Params' shape depends
// on Strategy, and NewStrategy is the only place that relationship is
// interpreted; this type carries Params as raw, undecoded bytes.
type RunBacktestRequest struct {
	Symbols         []string        `json:"symbols"`
	Strategy        StrategyKind    `json:"strategy"`
	Params          json.RawMessage `json:"params"`
	StartDate       string          `json:"start_date"`
	EndDate         string          `json:"end_date"`
	StartingCapital float64         `json:"starting_capital"`
}

// BacktestsResponse is GET /backtests -- wrapped rather than a bare array,
// matching every other list endpoint in this project (trading-engine's
// OrdersResponse, market-data's SymbolsResponse), so a top-level array never
// blocks adding something beside it later.
type BacktestsResponse struct {
	Backtests []Backtest `json:"backtests"`
}
