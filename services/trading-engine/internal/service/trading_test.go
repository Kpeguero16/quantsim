package service_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/service/mock"
)

const testSymbol = "AAPL"

// newService wires the three doubles with an account that exists and one
// priced symbol, which is the setup almost every test starts from.
//
// The invalidator is reachable through newServiceWithInvalidator for the tests
// that assert on it; everything else does not care that it exists.
func newService(t *testing.T) (*service.Service, *mock.AccountStore, *mock.TradingStore, *mock.PriceClient, uuid.UUID) {
	t.Helper()
	svc, accounts, trading, prices, _, userID := newServiceWithInvalidator(t)
	return svc, accounts, trading, prices, userID
}

func newServiceWithInvalidator(t *testing.T) (*service.Service, *mock.AccountStore, *mock.TradingStore, *mock.PriceClient, *mock.InsightsInvalidator, uuid.UUID) {
	t.Helper()

	userID := uuid.New()
	accounts := &mock.AccountStore{Account: service.Account{ID: uuid.New(), Balance: 100000}}
	trading := &mock.TradingStore{}
	prices := &mock.PriceClient{Prices: map[string]float64{testSymbol: 150}}
	insights := &mock.InsightsInvalidator{}

	return service.NewService(accounts, trading, prices, insights), accounts, trading, prices, insights, userID
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
		{"dust below the ledger tick", buy(testSymbol, 0.00001)},
		{"just under the ledger tick", buy(testSymbol, 0.00009)},
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

// The exploit this floor exists to close, kept as its own test because the
// table above proves only that dust is rejected -- not why it has to be.
//
// Before the floor, a sell of 0.00001 shares was charged at the full price and
// then rounded to 0.0000 shares by NUMERIC(20,4). The cash leg landed and the
// share leg vanished, so the account was paid for nothing: thirty of these
// against a live 300-share position moved the balance +0.0930 and left the
// position at exactly 300. Nothing bounded how often it could be repeated.
//
// A sell rather than a buy because that is the profitable direction; the buy
// side of the same bug merely destroyed the money instead.
func TestPlaceOrder_DustSellCannotMintMoney(t *testing.T) {
	svc, _, trading, prices, userID := newService(t)

	_, err := svc.PlaceOrder(context.Background(), userID,
		service.PlaceOrderRequest{Symbol: testSymbol, Side: service.SideSell, Quantity: 0.00001})

	if !errors.Is(err, service.ErrInvalidOrder) {
		t.Fatalf("got %v, want ErrInvalidOrder -- a quantity the ledger rounds to zero was accepted", err)
	}
	if len(prices.Calls) != 0 {
		t.Error("a dust order cost a price lookup")
	}
	if len(trading.ExecuteCalls) != 0 {
		t.Fatal("a dust order reached the store: it would have paid cash for zero shares")
	}
}

// The tick itself is legal. A floor that also refused the smallest storable
// quantity would be off by one in the direction nobody tests.
func TestPlaceOrder_ExactlyTheLedgerTickIsAllowed(t *testing.T) {
	svc, _, trading, _, userID := newService(t)

	if _, err := svc.PlaceOrder(context.Background(), userID, buy(testSymbol, 0.0001)); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	if got := trading.ExecuteCalls[0].Quantity; got != 0.0001 {
		t.Errorf("got quantity %v, want 0.0001", got)
	}
}

// Above the tick, a quantity finer than the ledger stores is snapped up front
// rather than left to Postgres to round on the way in. Otherwise the balance
// moves by price x 1.23456 while positions and trades record 1.2346, and the
// two legs of the same trade disagree by the difference.
func TestPlaceOrder_QuantityIsSnappedToTheLedgerTickBeforeItIsCharged(t *testing.T) {
	svc, _, trading, _, userID := newService(t)

	if _, err := svc.PlaceOrder(context.Background(), userID, buy(testSymbol, 1.23456)); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	// The value Postgres would have stored, reached before Postgres sees it.
	if got := trading.ExecuteCalls[0].Quantity; got != 1.2346 {
		t.Errorf("got quantity %v, want 1.2346 -- the charged quantity is not the stored one", got)
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

func TestPortfolio_RollsUpCashAndPositions(t *testing.T) {
	svc, accounts, trading, prices, userID := newService(t)
	accounts.Account = service.Account{ID: accounts.Account.ID, Balance: 5000}
	prices.Prices = map[string]float64{"AAPL": 150, "MSFT": 90}
	trading.Holdings = []service.Holding{
		{Symbol: "AAPL", Quantity: 10, AvgCost: 100},
		{Symbol: "MSFT", Quantity: 5, AvgCost: 120},
	}

	got, err := svc.Portfolio(context.Background(), userID)
	if err != nil {
		t.Fatalf("Portfolio: %v", err)
	}

	if got.Balance != 5000 {
		t.Errorf("balance = %v, want 5000", got.Balance)
	}
	// 5000 cash + 10*150 + 5*90
	if got.TotalEquity != 6950 {
		t.Errorf("total_equity = %v, want 6950", got.TotalEquity)
	}
	// 500 + (-150)
	if got.TotalUnrealizedPL != 350 {
		t.Errorf("total_unrealized_pl = %v, want 350", got.TotalUnrealizedPL)
	}
	if len(got.Positions) != 2 {
		t.Errorf("got %d positions, want 2", len(got.Positions))
	}
}

// 🔴 An unpriceable position is valued at cost. Dropping it would shrink the
// portfolio silently; zeroing it would report the account as having lost
// everything it holds -- both because one HTTP call failed.
func TestPortfolio_UnpriceablePositionsAreValuedAtCostNotDroppedOrZeroed(t *testing.T) {
	svc, accounts, trading, prices, userID := newService(t)
	accounts.Account = service.Account{ID: accounts.Account.ID, Balance: 1000}
	prices.Prices = map[string]float64{"AAPL": 150}
	trading.Holdings = []service.Holding{
		{Symbol: "AAPL", Quantity: 10, AvgCost: 100},
		{Symbol: "DELISTED", Quantity: 4, AvgCost: 25},
	}

	got, err := svc.Portfolio(context.Background(), userID)
	if err != nil {
		t.Fatalf("Portfolio: %v", err)
	}

	if len(got.Positions) != 2 {
		t.Fatalf("got %d positions, want both -- an unpriceable holding was dropped", len(got.Positions))
	}
	// 1000 cash + 10*150 priced + 4*25 at cost
	if got.TotalEquity != 2600 {
		t.Errorf("total_equity = %v, want 2600 (1000 + 1500 + 100 at cost)", got.TotalEquity)
	}
	// Only the priced position contributes P/L; an unvalued one contributes 0
	// rather than a made-up number.
	if got.TotalUnrealizedPL != 500 {
		t.Errorf("total_unrealized_pl = %v, want 500", got.TotalUnrealizedPL)
	}
}

// With market-data completely down the portfolio still answers, valued
// entirely at cost.
func TestPortfolio_UpstreamDownStillAnswers(t *testing.T) {
	svc, accounts, trading, prices, userID := newService(t)
	accounts.Account = service.Account{ID: accounts.Account.ID, Balance: 2000}
	prices.Err = service.ErrUpstreamUnavailable
	trading.Holdings = []service.Holding{{Symbol: "AAPL", Quantity: 10, AvgCost: 100}}

	got, err := svc.Portfolio(context.Background(), userID)
	if err != nil {
		t.Fatalf("a price outage failed the portfolio: %v -- this path fails open", err)
	}
	if got.TotalEquity != 3000 {
		t.Errorf("total_equity = %v, want 3000 (2000 cash + 10 shares at cost)", got.TotalEquity)
	}
	if got.TotalUnrealizedPL != 0 {
		t.Errorf("total_unrealized_pl = %v, want 0 when nothing can be priced", got.TotalUnrealizedPL)
	}
}

func TestPortfolio_EmptyPortfolioIsJustTheCash(t *testing.T) {
	svc, accounts, _, _, userID := newService(t)
	accounts.Account = service.Account{ID: accounts.Account.ID, Balance: 100000}

	got, err := svc.Portfolio(context.Background(), userID)
	if err != nil {
		t.Fatalf("Portfolio: %v", err)
	}
	if got.TotalEquity != 100000 || got.Balance != 100000 {
		t.Errorf("got equity %v balance %v, want both 100000", got.TotalEquity, got.Balance)
	}
	if got.Positions == nil || len(got.Positions) != 0 {
		t.Errorf("got positions %v, want an empty slice", got.Positions)
	}
}

// The two endpoints must not be able to disagree about the same account.
func TestPortfolio_PositionsMatchThePositionsEndpoint(t *testing.T) {
	svc, _, trading, prices, userID := newService(t)
	prices.Prices = map[string]float64{"AAPL": 150}
	trading.Holdings = []service.Holding{
		{Symbol: "AAPL", Quantity: 10, AvgCost: 100},
		{Symbol: "DELISTED", Quantity: 4, AvgCost: 25},
	}

	standalone, err := svc.Positions(context.Background(), userID)
	if err != nil {
		t.Fatalf("Positions: %v", err)
	}
	rolled, err := svc.Portfolio(context.Background(), userID)
	if err != nil {
		t.Fatalf("Portfolio: %v", err)
	}

	if len(standalone) != len(rolled.Positions) {
		t.Fatalf("got %d and %d positions from the two endpoints", len(standalone), len(rolled.Positions))
	}
	for i := range standalone {
		a, b := standalone[i], rolled.Positions[i]
		if a.Symbol != b.Symbol || a.Quantity != b.Quantity || a.AvgCost != b.AvgCost || a.UnrealizedPL != b.UnrealizedPL {
			t.Errorf("position %d differs: %+v vs %+v", i, a, b)
		}
		if (a.LatestPrice == nil) != (b.LatestPrice == nil) {
			t.Errorf("position %d disagrees on whether it could be priced", i)
		}
	}
}

func TestPortfolio_StoreFailurePropagates(t *testing.T) {
	svc, _, trading, _, userID := newService(t)
	trading.HoldingsErr = errors.New("connection reset")

	if _, err := svc.Portfolio(context.Background(), userID); err == nil {
		t.Fatal("a store failure was swallowed")
	}
}

// --- GET /trading/trades (Step 20 T2) ---

// The limit reaching the store is normalized, never the caller's raw value:
// a 0 or a 99999 passed straight into a LIMIT clause is either an empty
// response or an unbounded read.
func TestTrades_NormalizesTheLimitBeforeItReachesTheStore(t *testing.T) {
	tests := []struct {
		name      string
		requested int
		want      int
	}{
		{"absent or unparseable arrives as zero", 0, service.DefaultTradeLimit},
		{"negative", -5, service.DefaultTradeLimit},
		{"one is honoured, not treated as unset", 1, 1},
		{"below the cap passes through", 500, 500},
		{"exactly the default", service.DefaultTradeLimit, service.DefaultTradeLimit},
		{"exactly the cap", service.MaxTradeLimit, service.MaxTradeLimit},
		{"one over the cap is clamped, not rejected", service.MaxTradeLimit + 1, service.MaxTradeLimit},
		{"far over the cap", 99999, service.MaxTradeLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, trading, _, userID := newService(t)

			if _, err := svc.Trades(context.Background(), userID, tt.requested); err != nil {
				t.Fatalf("Trades: %v", err)
			}

			if len(trading.TradeLimits) != 1 {
				t.Fatalf("got %d ListTrades calls, want 1", len(trading.TradeLimits))
			}
			if got := trading.TradeLimits[0]; got != tt.want {
				t.Errorf("store received limit %d, want %d", got, tt.want)
			}
		})
	}
}

// Scoping runs through AccountForUser, which is what makes reading another
// user's trade log impossible to express rather than merely forbidden.
func TestTrades_ScopesThroughTheAccountLookup(t *testing.T) {
	svc, accounts, _, _, userID := newService(t)

	if _, err := svc.Trades(context.Background(), userID, 0); err != nil {
		t.Fatalf("Trades: %v", err)
	}

	if len(accounts.Calls) != 1 || accounts.Calls[0] != userID {
		t.Errorf("account lookup calls = %v, want exactly [%v]", accounts.Calls, userID)
	}
}

func TestTrades_PropagatesAnAccountLookupFailure(t *testing.T) {
	svc, accounts, trading, _, userID := newService(t)
	accounts.Err = service.ErrAccountNotFound

	if _, err := svc.Trades(context.Background(), userID, 0); !errors.Is(err, service.ErrAccountNotFound) {
		t.Fatalf("got %v, want ErrAccountNotFound", err)
	}
	if len(trading.TradeLimits) != 0 {
		t.Error("the store was queried despite the account lookup failing")
	}
}

func TestTrades_PropagatesAStoreFailure(t *testing.T) {
	svc, _, trading, _, userID := newService(t)
	trading.TradesErr = errors.New("boom")

	if _, err := svc.Trades(context.Background(), userID, 0); err == nil {
		t.Fatal("got nil, want the store's error")
	}
}

// --- Pricing is bounded by one timeout, not by the number of holdings -------
//
// The bug these cover: price() issued one HTTP lookup per holding in sequence,
// each bounded by the client's own 3s timeout. Per-symbol timeouts compose
// additively, so the endpoint's worst case was N x 3s with no bound on N --
// which is how a Redis outage turned GET /trading/portfolio into an 8.7s
// response (against 5.8ms healthy) and tripped ai-insights' 5s upstream
// timeout, taking /insights/portfolio down with a 502 for data that never
// came from Redis at all.
//
// The per-call timeout was never the problem. Nothing bounded the loop.

// A barrier of N can only be crossed if N lookups are in flight together, so
// this distinguishes concurrent from sequential by result rather than by
// duration. Sequential: the first caller waits alone until the context
// expires, and every position comes back unpriced. Concurrent: all N arrive,
// the barrier releases, and all N are priced.
func TestPositions_HoldingsArePricedConcurrently(t *testing.T) {
	svc, _, trading, prices, userID := newService(t)
	const n = 10
	prices.Prices = map[string]float64{}
	prices.Barrier = n
	for i := 0; i < n; i++ {
		symbol := string(rune('A' + i))
		prices.Prices[symbol] = 100
		trading.Holdings = append(trading.Holdings, service.Holding{Symbol: symbol, Quantity: 1, AvgCost: 50})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	positions, err := svc.Positions(ctx, userID)
	if err != nil {
		t.Fatalf("Positions: %v", err)
	}
	if len(positions) != n {
		t.Fatalf("got %d positions, want %d", len(positions), n)
	}
	for i, p := range positions {
		if p.LatestPrice == nil {
			t.Fatalf("position %d (%s) came back unpriced: the %d lookups never overlapped, "+
				"so pricing is still sequential", i, p.Symbol, n)
		}
	}
}

// The whole loop is bounded, not each call within it -- and the bound is the
// service's own, which is what this asserts. The parent context here carries
// NO deadline on purpose: an earlier draft of this test passed a 900ms parent
// and passed against the unfixed sequential code, because the parent was
// supplying the bound and the service was never exercised at all. A test whose
// deadline is tighter than the budget it means to check is testing its own
// setup.
func TestPositions_PricingImposesItsOwnBudget(t *testing.T) {
	svc, _, trading, prices, userID := newService(t)
	// Longer than any budget the service may impose, so the lookup is still
	// outstanding when the budget expires.
	prices.Delay = time.Hour
	trading.Holdings = []service.Holding{{Symbol: "AAPL", Quantity: 1, AvgCost: 50}}

	start := time.Now()
	positions, err := svc.Positions(context.Background(), userID)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Positions: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("got %d positions, want 1 -- a timed-out lookup dropped its holding", len(positions))
	}
	if positions[0].LatestPrice != nil {
		t.Error("AAPL was priced despite its lookup never answering")
	}
	if limit := 2 * service.PricingBudget; elapsed > limit {
		t.Errorf("pricing took %v with no deadline from the caller, want under %v -- "+
			"the service imposes no budget of its own", elapsed, limit)
	}
}

// Independence is the property price()'s existing comment is about, and
// concurrency is exactly where it would be lost -- one shared error variable
// across the goroutines would reintroduce it silently, and every other test
// here would stay green.
func TestPositions_ConcurrentPricingKeepsHoldingsIndependentAndOrdered(t *testing.T) {
	svc, _, trading, prices, userID := newService(t)
	prices.Prices = map[string]float64{"AAPL": 150, "TSLA": 200, "MSFT": 90}
	trading.Holdings = []service.Holding{
		{Symbol: "AAPL", Quantity: 10, AvgCost: 100},
		{Symbol: "DELISTED", Quantity: 3, AvgCost: 50},
		{Symbol: "TSLA", Quantity: 2, AvgCost: 180},
		{Symbol: "ALSOGONE", Quantity: 1, AvgCost: 10},
		{Symbol: "MSFT", Quantity: 4, AvgCost: 80},
	}

	positions, err := svc.Positions(context.Background(), userID)
	if err != nil {
		t.Fatalf("Positions: %v", err)
	}

	// Order is the holdings' order, not completion order. Concurrent appends
	// would scramble this, and the response would reorder run to run.
	want := []string{"AAPL", "DELISTED", "TSLA", "ALSOGONE", "MSFT"}
	for i, symbol := range want {
		if positions[i].Symbol != symbol {
			t.Fatalf("position %d is %s, want %s -- results were collected in completion order",
				i, positions[i].Symbol, symbol)
		}
	}
	for _, i := range []int{1, 3} {
		if positions[i].LatestPrice != nil {
			t.Errorf("%s was priced at %v", positions[i].Symbol, *positions[i].LatestPrice)
		}
	}
	for _, i := range []int{0, 2, 4} {
		if positions[i].LatestPrice == nil {
			t.Fatalf("two unpriceable symbols blanked %s: %+v", positions[i].Symbol, positions)
		}
	}
	if positions[0].UnrealizedPL != 500 || positions[2].UnrealizedPL != 40 || positions[4].UnrealizedPL != 40 {
		t.Errorf("P/L of the priced positions is wrong: %v, %v, %v",
			positions[0].UnrealizedPL, positions[2].UnrealizedPL, positions[4].UnrealizedPL)
	}
}

// A fill makes the placing user's cached report stale, because that report was
// computed from a trade log this order just added to (SPEC.md Step 24 §2.1).
func TestPlaceOrder_ASuccessfulFillInvalidatesThePlacingUsersReport(t *testing.T) {
	svc, _, _, _, insights, userID := newServiceWithInvalidator(t)

	if _, err := svc.PlaceOrder(context.Background(), userID, buy(testSymbol, 10)); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	calls := insights.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d invalidations, want exactly 1", len(calls))
	}
	if calls[0].UserID != userID {
		t.Errorf("invalidated %s, want the placing user %s", calls[0].UserID, userID)
	}
}

// A rejected order writes an orders row and nothing else: no trade, no
// position, no balance change. The report reads none of those, so invalidating
// would discard a valid cached report to recompute an identical one
// (SPEC.md Step 24 §2.6).
//
// Both rejection paths, because they leave the function at different points:
// one never reaches the store at all, the other reaches it and is refused.
func TestPlaceOrder_ARejectedOrderDoesNotInvalidate(t *testing.T) {
	t.Run("no price available", func(t *testing.T) {
		svc, _, _, _, insights, userID := newServiceWithInvalidator(t)

		// A symbol the price client has no price for is rejected before any
		// fill can happen.
		if _, err := svc.PlaceOrder(context.Background(), userID, buy("NOSUCH", 10)); err == nil {
			t.Fatal("PlaceOrder succeeded for an unpriced symbol, want a rejection")
		}

		if n := len(insights.Calls()); n != 0 {
			t.Errorf("a rejected order invalidated %d times, want 0", n)
		}
	})

	t.Run("validation refuses the order", func(t *testing.T) {
		svc, _, _, _, insights, userID := newServiceWithInvalidator(t)

		if _, err := svc.PlaceOrder(context.Background(), userID, buy(testSymbol, -1)); err == nil {
			t.Fatal("PlaceOrder succeeded for a negative quantity, want a rejection")
		}

		if n := len(insights.Calls()); n != 0 {
			t.Errorf("an invalid order invalidated %d times, want 0", n)
		}
	})
}

// The trade is committed and durable before this runs. There is nothing to
// roll back and nothing a retry could fix, so a broken cache must not turn a
// successful fill into an error (SPEC.md Step 24 §2.3).
//
// Asserted against the working case rather than just "err == nil", so that a
// change which swallowed the error but altered the fill would still fail.
func TestPlaceOrder_AFailingInvalidatorDoesNotFailTheOrder(t *testing.T) {
	working, _, _, _, _, userID := newServiceWithInvalidator(t)
	want, wantErr := working.PlaceOrder(context.Background(), userID, buy(testSymbol, 10))
	if wantErr != nil {
		t.Fatalf("control PlaceOrder: %v", wantErr)
	}

	broken, _, _, _, insights, brokenUser := newServiceWithInvalidator(t)
	insights.Err = errors.New("redis is gone")

	got, err := broken.PlaceOrder(context.Background(), brokenUser, buy(testSymbol, 10))
	if err != nil {
		t.Fatalf("a failing invalidator failed the order: %v", err)
	}
	if got != want {
		t.Errorf("the fill differed from the working case:\n got %+v\nwant %+v", got, want)
	}
	if n := len(insights.Calls()); n != 1 {
		t.Errorf("got %d invalidations, want 1 -- the failure must still be attempted", n)
	}
}

// The fill has committed, so a client hanging up must not take the
// invalidation with it (SPEC.md Step 24 §2.3).
//
// Without context.WithoutCancel this is the original defect returning through
// a path nobody would look at: the order lands, the delete never runs, and the
// stale report survives its full TTL. The mock records the context precisely so
// this can be asserted, because an invalidator that merely received a call
// would look identical.
func TestPlaceOrder_InvalidationSurvivesTheRequestBeingCancelled(t *testing.T) {
	svc, _, trading, _, insights, userID := newServiceWithInvalidator(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancelled DURING the fill, not before it: cancelling up front would be
	// refused at the price fetch and never reach a fill at all, which is a
	// different case and not this one.
	trading.OnExecute = cancel

	if _, err := svc.PlaceOrder(ctx, userID, buy(testSymbol, 10)); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	calls := insights.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d invalidations, want 1", len(calls))
	}
	if err := calls[0].Ctx.Err(); err != nil {
		t.Errorf("the invalidation was handed a cancelled context (%v), so a real "+
			"Redis client would have skipped the delete", err)
	}
}
