package service

import (
	"context"

	"github.com/google/uuid"
)

// AccountStore resolves the account behind an authenticated user.
// Implemented by *store.PostgresTradingStore in production; mocked in tests.
type AccountStore interface {
	// AccountForUser returns ErrAccountNotFound when the user has no account
	// row. The balance it reports is a snapshot with no lock behind it -- fine
	// for a portfolio read, never sufficient to validate an order against.
	AccountForUser(ctx context.Context, userID uuid.UUID) (Account, error)
}

// TradingStore owns everything that touches orders, trades and positions.
// Implemented by *store.PostgresTradingStore in production; mocked in tests.
type TradingStore interface {
	// ExecuteOrder runs one order to completion inside a single transaction
	// that locks the account row for its whole duration.
	//
	// The balance and holding checks live here rather than in the service for
	// one reason: they have to read the values the lock protects. A service
	// that validated first and called a store second would be reading a
	// balance that another order can change before the write lands -- which is
	// exactly the double-spend SPEC.md §2.6 exists to prevent, and it reads as
	// correct code.
	//
	// A business rejection (ErrInsufficientBalance, ErrInsufficientPosition)
	// still COMMITS: the rejected order row is durable, and only the balance,
	// position and trade are left untouched. A rollback would erase the audit
	// trail that persisting rejected orders exists to keep. Rollback is
	// reserved for infrastructure failure, where there is no working
	// connection to persist anything with.
	ExecuteOrder(ctx context.Context, p ExecuteOrderParams) (PlaceOrderResult, error)

	// RecordRejectedOrder persists an order rejected before any transaction
	// opened -- the price fetch failed, so there was nothing to execute.
	// Separate from ExecuteOrder because it must not take the account lock:
	// nothing is being mutated, and blocking a live order behind a failed
	// price lookup would turn a market-data outage into a queue.
	RecordRejectedOrder(ctx context.Context, accountID uuid.UUID, symbol string, side Side, quantity float64, reason string) (Order, error)

	// ListOrders returns the account's order history, newest first, rejected
	// orders included.
	ListOrders(ctx context.Context, accountID uuid.UUID) ([]Order, error)

	// ListHoldings returns open positions only (quantity > 0), unpriced.
	ListHoldings(ctx context.Context, accountID uuid.UUID) ([]Holding, error)
}

// PriceClient fetches the latest cached price for a symbol from market-data
// over HTTP. Implemented by *client.MarketDataClient in production; a fake in
// service and handler tests, an httptest.Server in integration tests, so
// nothing in the suite needs the real service running (SPEC.md §5).
type PriceClient interface {
	// LatestPrice returns ErrSymbolUnavailable when market-data has no price
	// for the symbol, and ErrUpstreamUnavailable for everything else --
	// unreachable, timed out, non-2xx, or a body that does not parse. The
	// distinction is the difference between a 404 and a 502 to the caller, and
	// between "you asked for something that does not exist" and "we cannot
	// tell you right now."
	LatestPrice(ctx context.Context, symbol string) (float64, error)
}
