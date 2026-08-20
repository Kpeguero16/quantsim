package service

import "time"

// Bar is one OHLC daily bar, as returned by market-data's history endpoint.
// The same shape backtesting's service.Bar has, and deliberately not an
// import of it: two services sharing a struct through one another's internal
// packages would couple their release cycles for the sake of six fields.
//
// Only Close is read by anything in this step -- a portfolio is marked to
// market at the close of each day (SPEC.md §2.1) -- but the rest are carried
// for parity with what market-data actually sends.
type Bar struct {
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    int64
}

// Side mirrors trading-engine's, and deliberately does not import it -- see
// Bar. The wire values are trading-engine's, because these decode from its
// GET /trading/trades response.
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// Trade is one execution, as returned by trading-engine's GET /trading/trades.
//
// RealizedPL is a pointer because only sells carry one. A buy's absent P/L and
// a break-even round trip are different facts, and a bare float64 would render
// both as 0.00 -- the same reason trading-engine's own Trade uses a pointer.
type Trade struct {
	ID         string    `json:"id"`
	Symbol     string    `json:"symbol"`
	Side       Side      `json:"side"`
	Quantity   float64   `json:"quantity"`
	Price      float64   `json:"price"`
	RealizedPL *float64  `json:"realized_pl"`
	ExecutedAt time.Time `json:"executed_at"`
}

// StartingBalance is the simulated USD balance every account opens with,
// mirroring auth's own constant (services/auth/internal/service/auth.go).
//
// Duplicated rather than imported, like Bar and Side. The reconstruction in
// this package depends on it being the value accounts were actually opened
// with -- so if auth ever changes it, historical accounts keep the old one and
// this becomes a per-account lookup rather than a constant. It is a constant
// today because there is exactly one value in the table.
const StartingBalance = 100000.00

// Reconstruction is a portfolio's history, derived rather than stored.
//
// Dates, Equity and Cash are parallel and always the same length. Holdings is
// the position map as of the LAST date, which is what the risk section weighs;
// intermediate holdings are not retained because nothing reads them and the
// map-per-day would be the largest allocation in the request.
type Reconstruction struct {
	Dates    []time.Time
	Equity   []float64
	Cash     []float64
	Holdings map[string]float64
}

// LivePosition is one open holding as trading-engine reports it right now.
// Only the quantity is read here -- this exists for the reconciliation guard
// (SPEC.md §2.12), which compares what the reconstruction concluded against
// what the account actually holds.
type LivePosition struct {
	Symbol   string  `json:"symbol"`
	Quantity float64 `json:"quantity"`
}

// LivePortfolio is trading-engine's GET /trading/portfolio, narrowed to the
// two facts the reconciliation guard needs.
//
// It carries no equity total on purpose. trading-engine's own total is priced
// from live market-data quotes and can be null per position when a price is
// unavailable, whereas this service marks to the stored daily close. Comparing
// those two numbers would compare two different definitions of equity and fail
// for reasons that have nothing to do with the guard.
type LivePortfolio struct {
	Balance   float64        `json:"balance"`
	Positions []LivePosition `json:"positions"`
}
