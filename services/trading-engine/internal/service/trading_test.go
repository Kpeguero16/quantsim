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

// A sell takes the same route as a buy -- one price lookup, one store call --
// and arrives with its side intact. Everything that makes a sell different
// (the position check, the cost basis, the realized P/L) happens inside the
// store's transaction, where the integration suite tests it.
func TestPlaceOrder_SellReachesTheStoreAsASell(t *testing.T) {
	svc, _, trading, _, userID := newService(t)

	if _, err := svc.PlaceOrder(context.Background(), userID,
		service.PlaceOrderRequest{Symbol: testSymbol, Side: service.SideSell, Quantity: 4}); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	if len(trading.ExecuteCalls) != 1 {
		t.Fatalf("got %d ExecuteOrder calls, want 1", len(trading.ExecuteCalls))
	}
	got := trading.ExecuteCalls[0]
	if got.Side != service.SideSell {
		t.Errorf("got side %q, want sell -- a sell must never reach the store as a buy", got.Side)
	}
	if got.Quantity != 4 || got.Price != 150 {
		t.Errorf("sell details did not survive: %+v", got)
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

func TestPositions_PricesEachHoldingAndComputesUnrealisedPL(t *testing.T) {
	svc, _, trading, prices, userID := newService(t)
	prices.Prices = map[string]float64{"AAPL": 150, "MSFT": 90}
	trading.Holdings = []service.Holding{
		{Symbol: "AAPL", Quantity: 10, AvgCost: 100},
		{Symbol: "MSFT", Quantity: 5, AvgCost: 120},
	}

	positions, err := svc.Positions(context.Background(), userID)
	if err != nil {
		t.Fatalf("Positions: %v", err)
	}
	if len(positions) != 2 {
		t.Fatalf("got %d positions, want 2", len(positions))
	}

	// (150 - 100) * 10
	if positions[0].LatestPrice == nil || *positions[0].LatestPrice != 150 {
		t.Fatalf("AAPL price = %v, want 150", positions[0].LatestPrice)
	}
	if positions[0].UnrealizedPL != 500 {
		t.Errorf("AAPL unrealized P/L = %v, want 500", positions[0].UnrealizedPL)
	}
	// A position under water reports a loss, not a zero.
	if positions[1].UnrealizedPL != -150 {
		t.Errorf("MSFT unrealized P/L = %v, want -150", positions[1].UnrealizedPL)
	}
}

// 🔴 Fail OPEN. The mirror of TestPlaceOrder_UpstreamDownRejectsAndNeverExecutes,
// and the pair of them is the point: the same outage must reject a write and
// still answer a read.
func TestPositions_UpstreamDownStillReturnsThePositionsUnpriced(t *testing.T) {
	svc, _, trading, prices, userID := newService(t)
	prices.Err = service.ErrUpstreamUnavailable
	trading.Holdings = []service.Holding{{Symbol: "AAPL", Quantity: 10, AvgCost: 100}}

	positions, err := svc.Positions(context.Background(), userID)

	if err != nil {
		t.Fatalf("a price outage failed the whole read: %v -- this path fails open", err)
	}
	if len(positions) != 1 {
		t.Fatalf("got %d positions, want the holding returned unpriced", len(positions))
	}
	if positions[0].LatestPrice != nil {
		t.Errorf("latest_price = %v, want nil -- 0 is a plausible price and would read as worthless",
			*positions[0].LatestPrice)
	}
	if positions[0].UnrealizedPL != 0 {
		t.Errorf("unrealized P/L = %v, want 0 for an unpriceable position", positions[0].UnrealizedPL)
	}
	// What the caller does still get: what they hold and what it cost.
	if positions[0].Quantity != 10 || positions[0].AvgCost != 100 {
		t.Errorf("the holding itself was degraded too: %+v", positions[0])
	}
}

// One symbol nobody can price must not blank the ones that priced fine.
func TestPositions_OneUnpriceableSymbolDoesNotAffectTheOthers(t *testing.T) {
	svc, _, trading, prices, userID := newService(t)
	prices.Prices = map[string]float64{"AAPL": 150, "TSLA": 200}
	trading.Holdings = []service.Holding{
		{Symbol: "AAPL", Quantity: 10, AvgCost: 100},
		{Symbol: "DELISTED", Quantity: 3, AvgCost: 50},
		{Symbol: "TSLA", Quantity: 2, AvgCost: 180},
	}

	positions, err := svc.Positions(context.Background(), userID)
	if err != nil {
		t.Fatalf("Positions: %v", err)
	}
	if len(positions) != 3 {
		t.Fatalf("got %d positions, want all 3 -- an unpriceable holding was dropped", len(positions))
	}
	if positions[1].LatestPrice != nil {
		t.Errorf("DELISTED was priced at %v", *positions[1].LatestPrice)
	}
	if positions[0].LatestPrice == nil || positions[2].LatestPrice == nil {
		t.Fatalf("one bad symbol blanked the others: %+v", positions)
	}
	if positions[0].UnrealizedPL != 500 || positions[2].UnrealizedPL != 40 {
		t.Errorf("P/L of the priced positions is wrong: %v, %v", positions[0].UnrealizedPL, positions[2].UnrealizedPL)
	}
}

// The store failing is a different failure from market-data failing: there is
// no degraded answer to give, so this one propagates.
func TestPositions_StoreFailurePropagates(t *testing.T) {
	svc, _, trading, _, userID := newService(t)
	trading.HoldingsErr = errors.New("connection reset")

	if _, err := svc.Positions(context.Background(), userID); err == nil {
		t.Fatal("a store failure was swallowed; fail-open covers the price lookup, not the holdings")
	}
}

func TestPositions_NoHoldingsIsAnEmptySliceNotAnError(t *testing.T) {
	svc, _, _, prices, userID := newService(t)

	positions, err := svc.Positions(context.Background(), userID)
	if err != nil {
		t.Fatalf("Positions: %v", err)
	}
	if positions == nil || len(positions) != 0 {
		t.Errorf("got %v, want an empty slice", positions)
	}
	if len(prices.Calls) != 0 {
		t.Error("priced a portfolio with nothing in it")
	}
}
