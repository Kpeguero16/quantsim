package service_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/service/mock"
)

const testSymbol = "AAPL"

// newService wires the three doubles with an account that exists and one
// priced symbol, which is the setup almost every test starts from.
func newService(t *testing.T) (*service.Service, *mock.AccountStore, *mock.TradingStore, *mock.PriceClient, uuid.UUID) {
	t.Helper()

	userID := uuid.New()
	accounts := &mock.AccountStore{Account: service.Account{ID: uuid.New(), Balance: 100000}}
	trading := &mock.TradingStore{}
	prices := &mock.PriceClient{Prices: map[string]float64{testSymbol: 150}}

	return service.NewService(accounts, trading, prices), accounts, trading, prices, userID
}

func buy(symbol string, qty float64) service.PlaceOrderRequest {
	return service.PlaceOrderRequest{Symbol: symbol, Side: service.SideBuy, Quantity: qty}
}

func TestPlaceOrder_BuyPassesTheFetchedPriceToTheStore(t *testing.T) {
	svc, accounts, trading, _, userID := newService(t)

	if _, err := svc.PlaceOrder(context.Background(), userID, buy(testSymbol, 10)); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	if len(trading.ExecuteCalls) != 1 {
		t.Fatalf("got %d ExecuteOrder calls, want 1", len(trading.ExecuteCalls))
	}
	got := trading.ExecuteCalls[0]
	if got.Price != 150 {
		t.Errorf("got price %v, want the 150 the price client returned", got.Price)
	}
	if got.AccountID != accounts.Account.ID {
		t.Errorf("got account %s, want the one resolved from the user", got.AccountID)
	}
	if got.Symbol != testSymbol || got.Side != service.SideBuy || got.Quantity != 10 {
		t.Errorf("order details did not survive: %+v", got)
	}
}

// Fail closed. The most important test in this file: no price, no fill.
func TestPlaceOrder_UpstreamDownRejectsAndNeverExecutes(t *testing.T) {
	svc, _, trading, prices, userID := newService(t)
	prices.Err = service.ErrUpstreamUnavailable

	_, err := svc.PlaceOrder(context.Background(), userID, buy(testSymbol, 10))

	if !errors.Is(err, service.ErrUpstreamUnavailable) {
		t.Fatalf("got %v, want ErrUpstreamUnavailable", err)
	}
	if len(trading.ExecuteCalls) != 0 {
		t.Fatal("the order was executed without a price -- fail-closed is broken")
	}
	if len(trading.RejectedCalls) != 1 {
		t.Fatalf("got %d rejections persisted, want 1", len(trading.RejectedCalls))
	}
	if got := trading.RejectedCalls[0].Reason; got != "upstream_unavailable" {
		t.Errorf("got reason %q, want upstream_unavailable", got)
	}
}

func TestPlaceOrder_UnknownSymbolRejectsWithItsOwnReason(t *testing.T) {
	svc, _, trading, _, userID := newService(t)

	_, err := svc.PlaceOrder(context.Background(), userID, buy("NOSUCH", 10))

	if !errors.Is(err, service.ErrSymbolUnavailable) {
		t.Fatalf("got %v, want ErrSymbolUnavailable", err)
	}
	if len(trading.ExecuteCalls) != 0 {
		t.Fatal("an unpriceable symbol reached the store")
	}
	if len(trading.RejectedCalls) != 1 || trading.RejectedCalls[0].Reason != "symbol_unavailable" {
		t.Errorf("got rejections %+v, want one with reason symbol_unavailable", trading.RejectedCalls)
	}
}

// A rejected order whose audit row also fails to write still reports the
// original problem. Reporting the bookkeeping failure instead would tell the
// caller the wrong thing about an outcome that is identical either way.
func TestPlaceOrder_RejectionPersistenceFailureDoesNotMaskTheRealError(t *testing.T) {
	svc, _, trading, prices, userID := newService(t)
	prices.Err = service.ErrUpstreamUnavailable
	trading.RejectErr = errors.New("database on fire")

	_, err := svc.PlaceOrder(context.Background(), userID, buy(testSymbol, 10))

	if !errors.Is(err, service.ErrUpstreamUnavailable) {
		t.Fatalf("got %v, want the original ErrUpstreamUnavailable", err)
	}
}

// Store-side rejections come from inside the row lock, which no mock can
// model. What is checked here is only that the service passes them through
// untouched -- that they are produced correctly is the store's integration
// tests' job.
func TestPlaceOrder_StoreRejectionsPropagateUnchanged(t *testing.T) {
	for _, want := range []error{service.ErrInsufficientBalance, service.ErrInsufficientPosition} {
		t.Run(want.Error(), func(t *testing.T) {
			svc, _, trading, _, userID := newService(t)
			trading.ExecuteErr = want

			_, err := svc.PlaceOrder(context.Background(), userID, buy(testSymbol, 10))

			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
			// The store persisted its own rejection inside the transaction, so
			// the service must not write a second one.
			if len(trading.RejectedCalls) != 0 {
				t.Errorf("service double-recorded a rejection the store already persisted: %+v", trading.RejectedCalls)
			}
		})
	}
}

// Validation runs before anything else: an order this malformed must not cost
// a price lookup, a store round trip, or a row in the history.
func TestPlaceOrder_InvalidOrdersAreRejectedBeforeAnyDependencyIsTouched(t *testing.T) {
	tests := []struct {
		name string
		req  service.PlaceOrderRequest
	}{
		{"zero quantity", buy(testSymbol, 0)},
		{"negative quantity", buy(testSymbol, -5)},
		{"absurd quantity", buy(testSymbol, 1e18)},
		{"empty symbol", buy("", 1)},
		{"whitespace symbol", buy("   ", 1)},
		{"overlong symbol", buy("ABCDEFGHIJKLMNOPQRSTUVWXYZ", 1)},
		{"unknown side", service.PlaceOrderRequest{Symbol: testSymbol, Side: "short", Quantity: 1}},
		{"empty side", service.PlaceOrderRequest{Symbol: testSymbol, Quantity: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, trading, prices, userID := newService(t)

			_, err := svc.PlaceOrder(context.Background(), userID, tt.req)

			if !errors.Is(err, service.ErrInvalidOrder) {
				t.Fatalf("got %v, want ErrInvalidOrder", err)
			}
			if len(prices.Calls) != 0 {
				t.Error("a malformed order cost a price lookup")
			}
			if len(trading.ExecuteCalls) != 0 || len(trading.RejectedCalls) != 0 {
				t.Error("a malformed order reached the store; it never became an order")
			}
		})
	}
}

// NaN deserves its own test because it is the one value that passes a
// `quantity <= 0` check: every comparison against NaN is false. A NaN
// quantity reaching the store would be written into NUMERIC and fail there,
// or worse, silently poison the balance arithmetic first.
func TestPlaceOrder_NaNQuantityIsRejected(t *testing.T) {
	svc, _, trading, _, userID := newService(t)
	nan := math.NaN()

	_, err := svc.PlaceOrder(context.Background(), userID, buy(testSymbol, nan))

	if !errors.Is(err, service.ErrInvalidOrder) {
		t.Fatalf("got %v, want ErrInvalidOrder", err)
	}
	if len(trading.ExecuteCalls) != 0 {
		t.Fatal("a NaN quantity reached the store")
	}
}

// The symbol is normalised before it is stored, for the same reason auth
// lowercases email addresses: positions carry a UNIQUE (account_id, symbol),
// so accepting "aapl" and "AAPL" as different strings would split one holding
// into two rows that never merge again.
func TestPlaceOrder_SymbolIsNormalisedBeforeItReachesTheStore(t *testing.T) {
	svc, _, trading, _, userID := newService(t)

	if _, err := svc.PlaceOrder(context.Background(), userID, buy("  aapl  ", 1)); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	if got := trading.ExecuteCalls[0].Symbol; got != testSymbol {
		t.Errorf("got symbol %q, want %q", got, testSymbol)
	}
}

func TestPlaceOrder_MissingAccountIsAnInvariantBreak(t *testing.T) {
	svc, accounts, trading, prices, userID := newService(t)
	accounts.Err = service.ErrAccountNotFound

	_, err := svc.PlaceOrder(context.Background(), userID, buy(testSymbol, 1))

	if !errors.Is(err, service.ErrAccountNotFound) {
		t.Fatalf("got %v, want ErrAccountNotFound", err)
	}
	if len(prices.Calls) != 0 {
		t.Error("priced an order for an account that does not exist")
	}
	if len(trading.RejectedCalls) != 0 {
		t.Error("recorded a rejection against an account that does not exist")
	}
}
