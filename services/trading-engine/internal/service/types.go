package service

import (
	"time"

	"github.com/google/uuid"
)

// Side is which way an order goes. Long-only: there is no "short" here, and
// selling more than a position holds is rejected rather than filled negative
// (SPEC.md §2.8).
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// Order statuses. 'pending' is the column default from migration 002 and is
// never observed by a caller -- this engine fills or rejects synchronously,
// so an order reaches one of these two before its transaction commits.
const (
	StatusFilled   = "filled"
	StatusRejected = "rejected"
)

// OrderTypeMarket is the only order type this step implements. Limit, stop
// and take-profit orders are explicitly out of scope (SPEC.md §1); the column
// exists from 002 and is written so a later step adding them does not have to
// backfill history that lied about what it was.
const OrderTypeMarket = "market"

// Money is float64 throughout, matching the convention already established by
// auth's StartingBalance. Postgres holds the authoritative NUMERIC(20,4);
// introducing a decimal type here would be a cross-cutting change to every
// service, which SPEC.md §6 puts out of scope.

// Order is a row of the order history, filled or rejected.
//
// FilledPrice and RejectionReason are pointers because at most one of them is
// ever set: a fill has no reason, a rejection never had a price. Encoding
// them as bare float64/string would report a rejected order as having filled
// at 0.00, which is a plausible-looking number rather than an absent one.
type Order struct {
	ID              uuid.UUID `json:"id"`
	Symbol          string    `json:"symbol"`
	Side            Side      `json:"side"`
	Quantity        float64   `json:"quantity"`
	Status          string    `json:"status"`
	OrderType       string    `json:"order_type"`
	FilledPrice     *float64  `json:"filled_price"`
	RejectionReason *string   `json:"rejection_reason"`
	CreatedAt       time.Time `json:"created_at"`
}

// Trade is one execution. Only sells carry RealizedPL, and it is captured
// here at execution time rather than recomputed later -- see migration 006's
// comment for why a recomputed value would be wrong rather than merely stale.
type Trade struct {
	ID         uuid.UUID `json:"id"`
	OrderID    uuid.UUID `json:"order_id"`
	Symbol     string    `json:"symbol"`
	Side       Side      `json:"side"`
	Quantity   float64   `json:"quantity"`
	Price      float64   `json:"price"`
	RealizedPL *float64  `json:"realized_pl"`
	ExecutedAt time.Time `json:"executed_at"`
}

// Holding is what the store knows about a position: quantity and cost basis,
// with no market price in it. Distinct from Position, which is what a caller
// sees, because the live price comes from a different system entirely and may
// not be available -- keeping them separate stops "we could not price this"
// from being indistinguishable from "the store returned zero."
type Holding struct {
	Symbol   string
	Quantity float64
	AvgCost  float64
}

// Position is a Holding priced at the current market, as returned by
// GET /trading/positions.
//
// LatestPrice is a pointer so an unpriceable position is null rather than 0.
// Zero is a plausible price and would read as "this is worthless" instead of
// "we could not reach market-data" -- the read path degrades, it does not
// lie (SPEC.md §2.9).
type Position struct {
	Symbol       string   `json:"symbol"`
	Quantity     float64  `json:"quantity"`
	AvgCost      float64  `json:"avg_cost"`
	LatestPrice  *float64 `json:"latest_price"`
	UnrealizedPL float64  `json:"unrealized_pl"`
}

// PlaceOrderRequest is the body of POST /trading/orders. There is no
// order_type field: market is the only one this step supports, and accepting
// a value the engine ignores would be worse than not accepting one.
type PlaceOrderRequest struct {
	Symbol   string  `json:"symbol"`
	Side     Side    `json:"side"`
	Quantity float64 `json:"quantity"`
}

// PlaceOrderResult is a successful fill. Balance is the post-trade balance,
// so a caller does not need a second round trip to learn what the order cost.
type PlaceOrderResult struct {
	Order   Order   `json:"order"`
	Trade   Trade   `json:"trade"`
	Balance float64 `json:"balance"`
}

// OrdersResponse is GET /trading/orders. The array is wrapped in an object
// rather than returned bare, matching market-data's SymbolsResponse: a
// top-level array leaves no room to add anything beside it later without
// breaking every client.
type OrdersResponse struct {
	Orders []Order `json:"orders"`
}

// TradesResponse is GET /trading/trades, wrapped for the same reason
// OrdersResponse is.
type TradesResponse struct {
	Trades []Trade `json:"trades"`
}

// PositionsResponse is GET /trading/positions, wrapped for the same reason
// OrdersResponse is.
type PositionsResponse struct {
	Positions []Position `json:"positions"`
}

// PortfolioResponse is GET /trading/portfolio: one call for a dashboard that
// would otherwise compose three.
type PortfolioResponse struct {
	Balance           float64    `json:"balance"`
	Positions         []Position `json:"positions"`
	TotalEquity       float64    `json:"total_equity"`
	TotalUnrealizedPL float64    `json:"total_unrealized_pl"`
}

// Account is the caller's single account. One account per user is assumed by
// both the schema and auth's CreateUserWithAccount, and is not revisited here
// (SPEC.md §1).
type Account struct {
	ID      uuid.UUID
	Balance float64
}

// ExecuteOrderParams is everything the store needs to run an order to
// completion. The price is resolved before this is built: the store never
// fetches one, because a network call has no business inside a transaction
// holding a row lock.
type ExecuteOrderParams struct {
	AccountID uuid.UUID
	Symbol    string
	Side      Side
	Quantity  float64
	Price     float64
}
