//go:build integration

package integration

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
)

const symbol = "AAPL"

// Money assertions go through numeric(), which reads NUMERIC(20,4) as text.
// The tolerance is well inside four decimal places -- this is checking the
// arithmetic, not Postgres's storage.
const tolerance = 1e-6

func assertMoney(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func buy(accountID uuid.UUID, qty, price float64) service.ExecuteOrderParams {
	return service.ExecuteOrderParams{
		AccountID: accountID,
		Symbol:    symbol,
		Side:      service.SideBuy,
		Quantity:  qty,
		Price:     price,
	}
}

func sell(accountID uuid.UUID, qty, price float64) service.ExecuteOrderParams {
	return service.ExecuteOrderParams{
		AccountID: accountID,
		Symbol:    symbol,
		Side:      service.SideSell,
		Quantity:  qty,
		Price:     price,
	}
}

func countRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("counting (%s): %v", query, err)
	}
	return n
}

func TestExecuteOrder_BuyMovesBalancePositionOrderAndTradeTogether(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 10000)

	result, err := s.ExecuteOrder(ctx, buy(accountID, 10, 150))
	if err != nil {
		t.Fatalf("ExecuteOrder: %v", err)
	}

	// The returned balance and the stored balance must agree. They are
	// computed in different places -- one in Go, one read back out of
	// Postgres -- and a mismatch is how a rounding bug first shows itself.
	assertMoney(t, result.Balance, 8500, "returned balance")
	stored := numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, accountID)
	assertMoney(t, stored, 8500, "stored balance")

	if result.Order.Status != service.StatusFilled {
		t.Errorf("order status = %q, want filled", result.Order.Status)
	}
	if result.Order.FilledPrice == nil || *result.Order.FilledPrice != 150 {
		t.Errorf("order filled_price = %v, want 150", result.Order.FilledPrice)
	}
	if result.Order.RejectionReason != nil {
		t.Errorf("a filled order carries a rejection reason: %q", *result.Order.RejectionReason)
	}
	if result.Trade.OrderID != result.Order.ID {
		t.Error("the trade is not linked to the order that produced it")
	}
	if result.Trade.RealizedPL != nil {
		t.Errorf("a buy booked realized P/L (%v); only sells do", *result.Trade.RealizedPL)
	}

	qty := numeric(t, ctx, pool, `SELECT quantity::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol)
	avg := numeric(t, ctx, pool, `SELECT avg_cost::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol)
	assertMoney(t, qty, 10, "position quantity")
	assertMoney(t, avg, 150, "position avg_cost")

	if n := countRows(t, ctx, pool, `SELECT count(*) FROM trades WHERE account_id = $1`, accountID); n != 1 {
		t.Errorf("got %d trades, want 1", n)
	}
}

// The weighted average is the number this engine most easily gets subtly
// wrong, so it is asserted against a value computed by hand rather than by
// calling the same function the store used.
func TestExecuteOrder_SecondBuyWeightsTheCostBasisByQuantity(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 100000)

	if _, err := s.ExecuteOrder(ctx, buy(accountID, 10, 100)); err != nil {
		t.Fatalf("first buy: %v", err)
	}
	if _, err := s.ExecuteOrder(ctx, buy(accountID, 30, 200)); err != nil {
		t.Fatalf("second buy: %v", err)
	}

	// (10*100 + 30*200) / 40 = 175
	avg := numeric(t, ctx, pool, `SELECT avg_cost::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol)
	assertMoney(t, avg, 175, "avg_cost after a weighted second buy")

	qty := numeric(t, ctx, pool, `SELECT quantity::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol)
	assertMoney(t, qty, 40, "position quantity")

	// One row, not two: the upsert must merge into the existing position.
	if n := countRows(t, ctx, pool, `SELECT count(*) FROM positions WHERE account_id = $1`, accountID); n != 1 {
		t.Errorf("got %d position rows, want 1", n)
	}

	assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, accountID),
		100000-1000-6000, "balance after both buys")
}

// A rejection is a durable outcome, not a rollback. The order row survives
// with its reason; nothing else moves.
func TestExecuteOrder_InsufficientBalanceIsPersistedNotRolledBack(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 100)

	_, err := s.ExecuteOrder(ctx, buy(accountID, 10, 150))

	if !errors.Is(err, service.ErrInsufficientBalance) {
		t.Fatalf("got %v, want ErrInsufficientBalance", err)
	}

	var status string
	var reason *string
	row := pool.QueryRow(ctx, `SELECT status, rejection_reason FROM orders WHERE account_id = $1`, accountID)
	if err := row.Scan(&status, &reason); err != nil {
		t.Fatalf("the rejected order was not persisted -- the transaction rolled back instead of committing: %v", err)
	}
	if status != service.StatusRejected {
		t.Errorf("order status = %q, want rejected", status)
	}
	if reason == nil || *reason != "insufficient_balance" {
		t.Errorf("rejection_reason = %v, want insufficient_balance", reason)
	}

	assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, accountID),
		100, "balance after a rejected order")
	if n := countRows(t, ctx, pool, `SELECT count(*) FROM trades WHERE account_id = $1`, accountID); n != 0 {
		t.Errorf("got %d trades for a rejected order, want 0", n)
	}
	if n := countRows(t, ctx, pool, `SELECT count(*) FROM positions WHERE account_id = $1`, accountID); n != 0 {
		t.Errorf("got %d positions for a rejected order, want 0", n)
	}
}

// An order costing exactly the balance must fill. The boundary is worth
// pinning: a `cost >= balance` typo passes every other test in this file.
func TestExecuteOrder_SpendingTheEntireBalanceIsAllowed(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 1500)

	if _, err := s.ExecuteOrder(ctx, buy(accountID, 10, 150)); err != nil {
		t.Fatalf("an order costing exactly the balance was rejected: %v", err)
	}
	assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, accountID),
		0, "balance after spending everything")
}

func TestExecuteOrder_SellBooksRealizedPLAndLeavesTheCostBasisAlone(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 10000)

	if _, err := s.ExecuteOrder(ctx, buy(accountID, 10, 100)); err != nil {
		t.Fatalf("buy: %v", err)
	}

	result, err := s.ExecuteOrder(ctx, sell(accountID, 4, 150))
	if err != nil {
		t.Fatalf("sell: %v", err)
	}

	// 10000 - 1000 + 600
	assertMoney(t, result.Balance, 9600, "returned balance")
	assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, accountID),
		9600, "stored balance")

	// (150 - 100) * 4
	if result.Trade.RealizedPL == nil {
		t.Fatal("a sell booked no realized P/L")
	}
	assertMoney(t, *result.Trade.RealizedPL, 200, "returned realized P/L")
	assertMoney(t, numeric(t, ctx, pool,
		`SELECT realized_pl::text FROM trades WHERE account_id = $1 AND side = 'sell'`, accountID),
		200, "stored realized P/L")

	assertMoney(t, numeric(t, ctx, pool, `SELECT quantity::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol),
		6, "position quantity after selling 4 of 10")
	// The whole rule of the sell path: profit is booked against the basis, the
	// basis does not move. A sell that recomputed avg_cost would land here at
	// 150 or somewhere between, and every later P/L would be wrong with it.
	assertMoney(t, numeric(t, ctx, pool, `SELECT avg_cost::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol),
		100, "avg_cost after a sell")
}

// 🔴 The trap migration 006 exists to avoid.
//
// The realized P/L of a past sell is only correct as of the basis in force
// when it executed. A later buy moves avg_cost, so anything recomputing that
// number afterwards produces a different, entirely plausible-looking answer.
// Here the recomputed value would be (300 - 290) * 5 = 50 instead of 750.
func TestExecuteOrder_ALaterBuyDoesNotRewriteAnEarlierSellsRealizedPL(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 100000)

	if _, err := s.ExecuteOrder(ctx, buy(accountID, 10, 100)); err != nil {
		t.Fatalf("first buy: %v", err)
	}
	if _, err := s.ExecuteOrder(ctx, buy(accountID, 10, 200)); err != nil {
		t.Fatalf("second buy: %v", err)
	}
	// Basis is now 150; selling at 300 books (300 - 150) * 5 = 750.
	if _, err := s.ExecuteOrder(ctx, sell(accountID, 5, 300)); err != nil {
		t.Fatalf("sell: %v", err)
	}

	booked := numeric(t, ctx, pool,
		`SELECT realized_pl::text FROM trades WHERE account_id = $1 AND side = 'sell'`, accountID)
	assertMoney(t, booked, 750, "realized P/L at execution")

	// (150 * 15 + 500 * 10) / 25 = 290
	if _, err := s.ExecuteOrder(ctx, buy(accountID, 10, 500)); err != nil {
		t.Fatalf("third buy: %v", err)
	}
	assertMoney(t, numeric(t, ctx, pool, `SELECT avg_cost::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol),
		290, "avg_cost after the later buy")

	after := numeric(t, ctx, pool,
		`SELECT realized_pl::text FROM trades WHERE account_id = $1 AND side = 'sell'`, accountID)
	assertMoney(t, after, 750, "realized P/L after a later buy moved the basis")

	// -1000 -2000 +1500 -5000
	assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, accountID),
		93500, "balance across four orders")
}

// A position sold out entirely keeps its row, and the next buy starts a clean
// basis from it -- no delete, no special case for "quantity is zero".
func TestExecuteOrder_SellingEverythingKeepsTheRowAndResetsTheBasisOnTheNextBuy(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 10000)

	if _, err := s.ExecuteOrder(ctx, buy(accountID, 10, 100)); err != nil {
		t.Fatalf("buy: %v", err)
	}
	if _, err := s.ExecuteOrder(ctx, sell(accountID, 10, 120)); err != nil {
		t.Fatalf("selling the whole position was rejected: %v", err)
	}

	if n := countRows(t, ctx, pool, `SELECT count(*) FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol); n != 1 {
		t.Fatalf("got %d position rows after selling out, want the row kept at quantity 0", n)
	}
	assertMoney(t, numeric(t, ctx, pool, `SELECT quantity::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol),
		0, "quantity after selling everything")

	// A holding at quantity 0 is not something anyone holds.
	holdings, err := s.ListHoldings(ctx, accountID)
	if err != nil {
		t.Fatalf("ListHoldings: %v", err)
	}
	if len(holdings) != 0 {
		t.Errorf("got %d holdings, want 0 -- a zeroed position is still being reported as held", len(holdings))
	}

	if _, err := s.ExecuteOrder(ctx, buy(accountID, 5, 300)); err != nil {
		t.Fatalf("buying back in: %v", err)
	}
	assertMoney(t, numeric(t, ctx, pool, `SELECT avg_cost::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol),
		300, "avg_cost of a position bought back from zero")
}

// Long-only (SPEC.md §2.8). Both shapes of "you do not hold that" -- too few
// shares, and no position row at all -- must reject, persist, and move nothing.
func TestExecuteOrder_SellingMoreThanHeldIsRejectedAndPersisted(t *testing.T) {
	tests := []struct {
		name    string
		holding float64 // shares bought first; 0 means no position row exists
	}{
		{"more than held", 3},
		{"nothing held at all", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, pool, ctx := newStore(t)
			_, accountID := seedAccount(t, ctx, pool, 10000)

			spent := 0.0
			if tt.holding > 0 {
				if _, err := s.ExecuteOrder(ctx, buy(accountID, tt.holding, 100)); err != nil {
					t.Fatalf("seeding the position: %v", err)
				}
				spent = tt.holding * 100
			}

			_, err := s.ExecuteOrder(ctx, sell(accountID, 5, 150))

			if !errors.Is(err, service.ErrInsufficientPosition) {
				t.Fatalf("got %v, want ErrInsufficientPosition", err)
			}

			var status string
			var reason *string
			row := pool.QueryRow(ctx,
				`SELECT status, rejection_reason FROM orders WHERE account_id = $1 AND side = 'sell'`, accountID)
			if err := row.Scan(&status, &reason); err != nil {
				t.Fatalf("the rejected sell was not persisted: %v", err)
			}
			if status != service.StatusRejected {
				t.Errorf("status = %q, want rejected", status)
			}
			if reason == nil || *reason != "insufficient_position" {
				t.Errorf("rejection_reason = %v, want insufficient_position", reason)
			}

			// Nothing about the account moved: no proceeds, no negative
			// quantity, no trade.
			assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, accountID),
				10000-spent, "balance after a rejected sell")
			if n := countRows(t, ctx, pool, `SELECT count(*) FROM trades WHERE account_id = $1 AND side = 'sell'`, accountID); n != 0 {
				t.Errorf("got %d sell trades for a rejected sell, want 0", n)
			}
			if n := countRows(t, ctx, pool, `SELECT count(*) FROM positions WHERE account_id = $1 AND quantity < 0`, accountID); n != 0 {
				t.Errorf("a rejected sell produced a negative position -- this engine is long-only")
			}
			if tt.holding > 0 {
				assertMoney(t, numeric(t, ctx, pool, `SELECT quantity::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol),
					tt.holding, "position quantity after a rejected sell")
			}
		})
	}
}

// The boundary the "more than held" test cannot catch: selling exactly what is
// held must fill. A `quantity >= held` typo passes every other sell test here.
func TestExecuteOrder_SellingExactlyTheHoldingIsAllowed(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 10000)
	seedPosition(t, ctx, pool, accountID, symbol, 7, 100)

	if _, err := s.ExecuteOrder(ctx, sell(accountID, 7, 100)); err != nil {
		t.Fatalf("selling exactly the holding was rejected: %v", err)
	}
	assertMoney(t, numeric(t, ctx, pool, `SELECT quantity::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol),
		0, "quantity after selling exactly the holding")
}

// A sell at a loss books a negative number rather than clamping at zero.
func TestExecuteOrder_SellBelowCostBooksALoss(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 10000)
	seedPosition(t, ctx, pool, accountID, symbol, 10, 100)

	result, err := s.ExecuteOrder(ctx, sell(accountID, 10, 60))
	if err != nil {
		t.Fatalf("sell: %v", err)
	}
	if result.Trade.RealizedPL == nil {
		t.Fatal("a losing sell booked no realized P/L")
	}
	assertMoney(t, *result.Trade.RealizedPL, -400, "realized P/L on a losing sell")
}

// THE test in this suite.
//
// Two buys arrive together on one account. Together they cost more than it
// holds; individually either fits. Exactly one must fill.
//
// This is the only test here that cannot be written against a mock, and the
// only one that fails when FOR UPDATE is removed from the account read --
// which is precisely why the lock is verified by deleting it and watching this
// go red, rather than by reading the query and agreeing it looks right.
func TestExecuteOrder_ConcurrentBuysCannotDoubleSpendTheSameBalance(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 1000)

	const orders = 2
	start := make(chan struct{})
	errs := make([]error, orders)

	var wg sync.WaitGroup
	for i := range orders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// The barrier is what makes this a race rather than a sequence.
			<-start
			_, errs[i] = s.ExecuteOrder(ctx, buy(accountID, 1, 600))
		}(i)
	}
	close(start)
	wg.Wait()

	var filled, rejected int
	for _, err := range errs {
		switch {
		case err == nil:
			filled++
		case errors.Is(err, service.ErrInsufficientBalance):
			rejected++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if filled != 1 || rejected != 1 {
		t.Fatalf("got %d filled and %d rejected, want exactly 1 of each -- "+
			"two concurrent orders both spent a balance that could only cover one", filled, rejected)
	}

	assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, accountID),
		400, "final balance")

	if n := countRows(t, ctx, pool, `SELECT count(*) FROM trades WHERE account_id = $1`, accountID); n != 1 {
		t.Errorf("got %d trades, want 1 -- the balance can be right by coincidence while both orders filled", n)
	}
	if n := countRows(t, ctx, pool, `SELECT count(*) FROM orders WHERE account_id = $1 AND status = 'filled'`, accountID); n != 1 {
		t.Errorf("got %d filled orders, want 1", n)
	}
	if n := countRows(t, ctx, pool, `SELECT count(*) FROM orders WHERE account_id = $1 AND status = 'rejected'`, accountID); n != 1 {
		t.Errorf("got %d rejected orders, want 1", n)
	}

	qty := numeric(t, ctx, pool, `SELECT quantity::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol)
	assertMoney(t, qty, 1, "position quantity after one of two orders filled")
}

// Concurrency again, with more contention and orders that all fit. Nothing
// may be lost: the balance must reflect every fill, not the last writer.
func TestExecuteOrder_ConcurrentAffordableBuysAllLand(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 10000)

	const orders = 10
	start := make(chan struct{})
	errs := make([]error, orders)

	var wg sync.WaitGroup
	for i := range orders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = s.ExecuteOrder(ctx, buy(accountID, 1, 100))
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("order %d failed though the account could afford all ten: %v", i, err)
		}
	}

	// A lost update leaves this at 9900 -- one fill's worth -- rather than 9000.
	assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, accountID),
		9000, "balance after ten concurrent fills")
	qty := numeric(t, ctx, pool, `SELECT quantity::text FROM positions WHERE account_id = $1 AND symbol = $2`, accountID, symbol)
	assertMoney(t, qty, 10, "position quantity after ten concurrent fills")
}

// Two accounts trading at once must not block or corrupt each other: the lock
// is per account row, not a global one.
func TestExecuteOrder_OrdersOnDifferentAccountsDoNotInterfere(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, first := seedAccount(t, ctx, pool, 1000)
	_, second := seedAccount(t, ctx, pool, 1000)

	if _, err := s.ExecuteOrder(ctx, buy(first, 1, 600)); err != nil {
		t.Fatalf("first account: %v", err)
	}
	if _, err := s.ExecuteOrder(ctx, buy(second, 1, 600)); err != nil {
		t.Fatalf("second account: %v", err)
	}

	assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, first), 400, "first balance")
	assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, second), 400, "second balance")
}

func TestAccountForUser(t *testing.T) {
	s, pool, ctx := newStore(t)
	userID, accountID := seedAccount(t, ctx, pool, 4321)

	account, err := s.AccountForUser(ctx, userID)
	if err != nil {
		t.Fatalf("AccountForUser: %v", err)
	}
	if account.ID != accountID {
		t.Errorf("got account %s, want %s", account.ID, accountID)
	}
	assertMoney(t, account.Balance, 4321, "balance")

	// A valid token whose user has no account row is a broken invariant, and
	// the store must name it rather than returning a zero-valued account that
	// every caller downstream would treat as real.
	if _, err := s.AccountForUser(ctx, uuid.New()); !errors.Is(err, service.ErrAccountNotFound) {
		t.Errorf("got %v, want ErrAccountNotFound", err)
	}
}

// The pre-transaction rejection path: the price lookup failed, so there was
// never anything to execute. The row still has to exist.
func TestRecordRejectedOrder(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 1000)

	order, err := s.RecordRejectedOrder(ctx, accountID, symbol, service.SideBuy, 5, "upstream_unavailable")
	if err != nil {
		t.Fatalf("RecordRejectedOrder: %v", err)
	}

	if order.Status != service.StatusRejected {
		t.Errorf("status = %q, want rejected", order.Status)
	}
	if order.RejectionReason == nil || *order.RejectionReason != "upstream_unavailable" {
		t.Errorf("reason = %v, want upstream_unavailable", order.RejectionReason)
	}
	if order.FilledPrice != nil {
		t.Errorf("a rejected order has a filled price: %v", *order.FilledPrice)
	}

	assertMoney(t, numeric(t, ctx, pool, `SELECT balance::text FROM accounts WHERE id = $1`, accountID),
		1000, "balance after recording a rejection")
	if n := countRows(t, ctx, pool, `SELECT count(*) FROM trades WHERE account_id = $1`, accountID); n != 0 {
		t.Errorf("got %d trades, want 0", n)
	}
}

func TestListOrders_NewestFirstWithRejectionsIncluded(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 1000)

	// One of each outcome, in a known order.
	if _, err := s.ExecuteOrder(ctx, buy(accountID, 2, 100)); err != nil {
		t.Fatalf("filled buy: %v", err)
	}
	if _, err := s.ExecuteOrder(ctx, buy(accountID, 50, 100)); !errors.Is(err, service.ErrInsufficientBalance) {
		t.Fatalf("got %v, want ErrInsufficientBalance", err)
	}
	if _, err := s.ExecuteOrder(ctx, sell(accountID, 99, 100)); !errors.Is(err, service.ErrInsufficientPosition) {
		t.Fatalf("got %v, want ErrInsufficientPosition", err)
	}

	orders, err := s.ListOrders(ctx, accountID)
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if len(orders) != 3 {
		t.Fatalf("got %d orders, want 3 -- rejected orders belong in the history (SPEC.md §2.5)", len(orders))
	}

	// Newest first.
	for i := 1; i < len(orders); i++ {
		if orders[i].CreatedAt.After(orders[i-1].CreatedAt) {
			t.Fatalf("order %d is newer than order %d: history is not newest-first", i, i-1)
		}
	}

	if orders[0].Side != service.SideSell || orders[0].Status != service.StatusRejected {
		t.Errorf("newest order is %+v, want the rejected sell", orders[0])
	}
	if orders[0].RejectionReason == nil || *orders[0].RejectionReason != "insufficient_position" {
		t.Errorf("rejected sell lost its reason: %v", orders[0].RejectionReason)
	}
	if orders[1].RejectionReason == nil || *orders[1].RejectionReason != "insufficient_balance" {
		t.Errorf("rejected buy lost its reason: %v", orders[1].RejectionReason)
	}
	oldest := orders[2]
	if oldest.Status != service.StatusFilled || oldest.FilledPrice == nil || *oldest.FilledPrice != 100 {
		t.Errorf("oldest order is %+v, want the fill at 100", oldest)
	}
	if oldest.RejectionReason != nil {
		t.Errorf("a filled order carries a rejection reason: %q", *oldest.RejectionReason)
	}
	if oldest.OrderType != service.OrderTypeMarket {
		t.Errorf("order_type = %q, want market", oldest.OrderType)
	}
}

// Isolation proven by putting a second account's orders in the same table and
// asking for one account's history -- not by reading the WHERE clause.
func TestListOrders_ReturnsOnlyTheAccountsOwnOrders(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, mine := seedAccount(t, ctx, pool, 10000)
	_, theirs := seedAccount(t, ctx, pool, 10000)

	if _, err := s.ExecuteOrder(ctx, buy(mine, 1, 100)); err != nil {
		t.Fatalf("my order: %v", err)
	}
	if _, err := s.ExecuteOrder(ctx, buy(theirs, 7, 100)); err != nil {
		t.Fatalf("their order: %v", err)
	}

	orders, err := s.ListOrders(ctx, mine)
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("got %d orders, want only my own -- another account's history leaked", len(orders))
	}
	if orders[0].Quantity != 1 {
		t.Errorf("got quantity %v, want 1 -- this is the other account's order", orders[0].Quantity)
	}
}

// An account that has never traded gets an empty slice, not nil: the handler
// encodes it as [], and a client mapping over null would break.
func TestListOrders_EmptyHistoryIsAnEmptySliceNotNil(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 1000)

	orders, err := s.ListOrders(ctx, accountID)
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if orders == nil {
		t.Fatal("got nil, want an empty slice")
	}
	if len(orders) != 0 {
		t.Errorf("got %d orders for an account that never traded", len(orders))
	}
}
