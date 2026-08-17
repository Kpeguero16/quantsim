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
