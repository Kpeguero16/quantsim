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
	"sync"
	"time"

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

	// OnExecute runs inside ExecuteOrder, before it returns. It exists so a
	// test can make something happen DURING the fill rather than before it --
	// specifically, the client hanging up while the transaction commits, which
	// is the only way to reach the code that runs after a fill with an already
	// cancelled request context.
	OnExecute func()
}

func (s *TradingStore) ExecuteOrder(_ context.Context, p service.ExecuteOrderParams) (service.PlaceOrderResult, error) {
	s.ExecuteCalls = append(s.ExecuteCalls, p)
	if s.OnExecute != nil {
		s.OnExecute()
	}
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

	// Delay, when set, holds each lookup open for that long before answering,
	// and abandons it if the context is cancelled first. It exists to model
	// the degraded market-data this mock could not otherwise represent: a
	// service that is reachable and slow, rather than one that is down.
	Delay time.Duration

	// Barrier, when > 0, holds every lookup until that many callers have
	// arrived, then releases them all at once.
	//
	// This is what makes concurrency provable rather than probable. A timing
	// assertion says "this was fast enough", which a slow machine can
	// falsify and a lucky one can pass by accident; a barrier of N can only
	// be crossed if N lookups are genuinely in flight together. A sequential
	// caller waits at it alone until its context expires, so the two
	// implementations differ in the result, not merely in the duration.
	Barrier int

	// mu guards Calls and arrived. The service prices holdings concurrently,
	// so an unsynchronised append here is a data race in the mock that would
	// be reported against the code under test.
	mu      sync.Mutex
	arrived int
	release chan struct{}

	Calls []string
}

func (c *PriceClient) LatestPrice(ctx context.Context, symbol string) (float64, error) {
	c.mu.Lock()
	c.Calls = append(c.Calls, symbol)
	if c.Barrier > 0 {
		if c.release == nil {
			c.release = make(chan struct{})
		}
		c.arrived++
		if c.arrived == c.Barrier {
			close(c.release)
		}
		release := c.release
		c.mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
			return 0, service.ErrUpstreamUnavailable
		}
	} else {
		c.mu.Unlock()
	}

	if c.Delay > 0 {
		select {
		case <-time.After(c.Delay):
		case <-ctx.Done():
			return 0, service.ErrUpstreamUnavailable
		}
	}

	// Checked after the waits, not before: a lookup that the context
	// cancelled must report the upstream failure rather than a stale answer
	// this mock happens to hold.
	if err := ctx.Err(); err != nil {
		return 0, service.ErrUpstreamUnavailable
	}
	if c.Err != nil {
		return 0, c.Err
	}
	price, ok := c.Prices[symbol]
	if !ok {
		return 0, service.ErrSymbolUnavailable
	}
	return price, nil
}

// CallCount reports how many lookups were made, safely under concurrency.
// Tests that only need the count should use this rather than len(Calls).
func (c *PriceClient) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.Calls)
}

// InsightsInvalidator records which users had their cached report dropped.
//
// It records the CONTEXT as well as the user id, because the context is half
// of what this collaborator has to get right: PlaceOrder must strip the
// request's cancellation before calling it (SPEC.md Step 24 §2.3), and a mock
// that only recorded the id would pass identically whether or not it did.
type InsightsInvalidator struct {
	Err error

	mu    sync.Mutex
	calls []InvalidateCall
}

// InvalidateCall is one InvalidateInsights call as it arrived.
type InvalidateCall struct {
	UserID uuid.UUID
	Ctx    context.Context
}

var _ service.InsightsInvalidator = (*InsightsInvalidator)(nil)

func (i *InsightsInvalidator) InvalidateInsights(ctx context.Context, userID uuid.UUID) error {
	i.mu.Lock()
	i.calls = append(i.calls, InvalidateCall{UserID: userID, Ctx: ctx})
	i.mu.Unlock()
	return i.Err
}

// Calls returns every invalidation this received, in order.
func (i *InsightsInvalidator) Calls() []InvalidateCall {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]InvalidateCall(nil), i.calls...)
}
