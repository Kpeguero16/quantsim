// Package mock provides in-memory doubles for the trading engine's three
// dependencies, so the service and handler suites need neither Postgres nor a
// running market-data.
//
// These deliberately do not simulate a database. The one thing that matters
// most about the real store -- that a row lock serializes concurrent orders --
// cannot be modelled here at all, which is why SPEC.md §5 makes the store's
// integration tests required rather than optional. What these doubles are for
// is the business rules around that transaction: which errors propagate, what
// gets persisted when a price lookup fails, and in what order the service
// talks to its dependencies.
package mock

import (
	"context"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
)

// Compile-time proof that these still satisfy the same interfaces the real
// implementations do. The service suite runs entirely against these types, so
// without the assertions they could drift away from the store and keep every
// test green while testing a shape production no longer has -- the failure
// Step 12 added the same assertions to catch.
var (
	_ service.AccountStore = (*AccountStore)(nil)
	_ service.TradingStore = (*TradingStore)(nil)
	_ service.PriceClient  = (*PriceClient)(nil)
)

type AccountStore struct {
	Account service.Account
	Err     error

	Calls []uuid.UUID
}

func (s *AccountStore) AccountForUser(_ context.Context, userID uuid.UUID) (service.Account, error) {
	s.Calls = append(s.Calls, userID)
	if s.Err != nil {
		return service.Account{}, s.Err
	}
	return s.Account, nil
}

// RejectedOrderCall records one RecordRejectedOrder invocation, so a test can
// assert not just that a rejection was persisted but that it was persisted
// with the right reason.
type RejectedOrderCall struct {
	AccountID uuid.UUID
	Symbol    string
	Side      service.Side
	Quantity  float64
	Reason    string
}

type TradingStore struct {
	// ExecuteResult and ExecuteErr are ExecuteOrder's outcome. Setting Err to
	// service.ErrInsufficientBalance is how a service test exercises a
	// rejection that, in production, is only reachable from inside the lock.
	ExecuteResult service.PlaceOrderResult
	ExecuteErr    error

	Orders      []service.Order
	OrdersErr   error
	Holdings    []service.Holding
	HoldingsErr error
	Trades      []service.Trade
	TradesErr   error

	// RejectErr makes persisting a rejection fail. Worth being able to
	// simulate: the caller is already handling one failure when it happens.
	RejectErr error

	ExecuteCalls  []service.ExecuteOrderParams
	RejectedCalls []RejectedOrderCall

	// TradeLimits records the limit each ListTrades call was given, so a test
	// can assert the service normalized it rather than passing a caller's 0 or
	// 99999 straight through to a LIMIT clause.
	TradeLimits []int
}

func (s *TradingStore) ExecuteOrder(_ context.Context, p service.ExecuteOrderParams) (service.PlaceOrderResult, error) {
	s.ExecuteCalls = append(s.ExecuteCalls, p)
	if s.ExecuteErr != nil {
		return service.PlaceOrderResult{}, s.ExecuteErr
	}
	return s.ExecuteResult, nil
}

func (s *TradingStore) RecordRejectedOrder(_ context.Context, accountID uuid.UUID, symbol string, side service.Side, quantity float64, reason string) (service.Order, error) {
	s.RejectedCalls = append(s.RejectedCalls, RejectedOrderCall{
		AccountID: accountID,
		Symbol:    symbol,
		Side:      side,
		Quantity:  quantity,
		Reason:    reason,
	})
	if s.RejectErr != nil {
		return service.Order{}, s.RejectErr
	}
	return service.Order{
		ID:              uuid.New(),
		Symbol:          symbol,
		Side:            side,
		Quantity:        quantity,
		Status:          service.StatusRejected,
		OrderType:       service.OrderTypeMarket,
		RejectionReason: &reason,
	}, nil
}

func (s *TradingStore) ListOrders(_ context.Context, _ uuid.UUID) ([]service.Order, error) {
	if s.OrdersErr != nil {
		return nil, s.OrdersErr
	}
	return s.Orders, nil
}

func (s *TradingStore) ListHoldings(_ context.Context, _ uuid.UUID) ([]service.Holding, error) {
	if s.HoldingsErr != nil {
		return nil, s.HoldingsErr
	}
	return s.Holdings, nil
}

// ListTrades returns Trades verbatim and does NOT apply limit -- the mock
// records it instead. Truncating here would hide the thing worth asserting:
// that the service normalized the limit before the store ever saw it.
func (s *TradingStore) ListTrades(_ context.Context, _ uuid.UUID, limit int) ([]service.Trade, error) {
	s.TradeLimits = append(s.TradeLimits, limit)
	if s.TradesErr != nil {
		return nil, s.TradesErr
	}
	return s.Trades, nil
}

// PriceClient answers from a map. A symbol that is absent gets
// ErrSymbolUnavailable, which is what market-data's 404 means -- so the
// common "unknown ticker" case needs no setup beyond leaving it out.
type PriceClient struct {
	Prices map[string]float64

	// Err, when set, is returned for every symbol regardless of Prices. This
	// is how the fail-closed path is driven: set it to
	// service.ErrUpstreamUnavailable and no order may fill.
	Err error

	Calls []string
}

func (c *PriceClient) LatestPrice(_ context.Context, symbol string) (float64, error) {
	c.Calls = append(c.Calls, symbol)
	if c.Err != nil {
		return 0, c.Err
	}
	price, ok := c.Prices[symbol]
	if !ok {
		return 0, service.ErrSymbolUnavailable
	}
	return price, nil
}
