// Package store implements the trading engine's persistence against Postgres.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
)

// Compile-time proof that this still satisfies both interfaces the service
// depends on. Without them a signature drift is caught only when cmd/server is
// built -- which no test does -- so `go test ./...` would stay green against a
// store the service can no longer use.
var (
	_ service.AccountStore = (*PostgresTradingStore)(nil)
	_ service.TradingStore = (*PostgresTradingStore)(nil)
)

// errSellNotImplemented is a placeholder removed in Task 10, when the sell
// branch lands. It exists so that a sell reaching this store during the buy
// slice fails loudly instead of falling through to something that looks like
// it worked.
var errSellNotImplemented = errors.New("sell orders are not implemented yet")

type PostgresTradingStore struct {
	pool *pgxpool.Pool
}

func NewPostgresTradingStore(pool *pgxpool.Pool) *PostgresTradingStore {
	return &PostgresTradingStore{pool: pool}
}

// AccountForUser resolves the single account behind an authenticated user.
//
// The balance it returns is an unlocked snapshot, correct only for reads. It
// must never be used to validate an order -- see ExecuteOrder, which reads the
// balance again under a lock for exactly that reason.
func (s *PostgresTradingStore) AccountForUser(ctx context.Context, userID uuid.UUID) (service.Account, error) {
	var a service.Account
	err := s.pool.QueryRow(ctx,
		`SELECT id, balance FROM accounts WHERE user_id = $1`, userID).
		Scan(&a.ID, &a.Balance)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.Account{}, service.ErrAccountNotFound
		}
		return service.Account{}, err
	}
	return a, nil
}

// ExecuteOrder runs one order to completion inside a single transaction.
//
// # The lock
//
// The first statement is `SELECT balance ... FOR UPDATE`, and everything
// downstream validates against the number it returns. That is the invariant
// this whole service is built around: two orders arriving together on the same
// account must not both read the same pre-trade balance and each conclude they
// can afford it. FOR UPDATE makes the second transaction wait for the first to
// commit, so it reads the balance that already reflects the first fill.
//
// Deleting `FOR UPDATE` leaves code that reads as correct, passes every unit
// test, and double-spends under concurrency. That is why the integration suite
// has a test that fails without it, and why that test is run with the lock
// removed rather than trusted to be meaningful.
//
// # What commits and what rolls back
//
// A business rejection -- the account cannot afford the buy, the position
// cannot cover the sell -- COMMITS. The order row is written with
// status='rejected' and a reason, and nothing else is touched. Rolling back
// there would erase the audit trail that persisting rejected orders exists to
// keep (SPEC.md §2.5).
//
// Rollback is reserved for infrastructure failure, which is the only case
// where nothing can be persisted because there is nothing working to persist
// it with.
func (s *PostgresTradingStore) ExecuteOrder(ctx context.Context, p service.ExecuteOrderParams) (service.PlaceOrderResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return service.PlaceOrderResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	var balance float64
	err = tx.QueryRow(ctx,
		`SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`, p.AccountID).
		Scan(&balance)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.PlaceOrderResult{}, service.ErrAccountNotFound
		}
		return service.PlaceOrderResult{}, fmt.Errorf("locking account row: %w", err)
	}

	switch p.Side {
	case service.SideBuy:
		return executeBuy(ctx, tx, p, balance)
	case service.SideSell:
		return service.PlaceOrderResult{}, errSellNotImplemented
	default:
		// Unreachable: the service validates the side before this is called.
		// Kept as a refusal rather than a fallthrough, so a future caller that
		// skips validation cannot get an unintended buy.
		return service.PlaceOrderResult{}, service.ErrInvalidOrder
	}
}

// executeBuy owns the transaction it is handed, including committing it.
func executeBuy(ctx context.Context, tx pgx.Tx, p service.ExecuteOrderParams, balance float64) (service.PlaceOrderResult, error) {
	cost := p.Price * p.Quantity

	if cost > balance {
		if err := rejectWithin(ctx, tx, p, "insufficient_balance"); err != nil {
			return service.PlaceOrderResult{}, err
		}
		return service.PlaceOrderResult{}, service.ErrInsufficientBalance
	}

	order, err := insertOrder(ctx, tx, p, service.StatusFilled, &p.Price, nil)
	if err != nil {
		return service.PlaceOrderResult{}, fmt.Errorf("inserting filled order: %w", err)
	}

	trade, err := insertTrade(ctx, tx, p, order.ID, nil)
	if err != nil {
		return service.PlaceOrderResult{}, fmt.Errorf("inserting trade: %w", err)
	}

	// Reading the position without FOR UPDATE is safe only because the account
	// row above is already locked: every order against this account -- and so
	// against every one of its positions -- is serialized behind that lock.
	// A second lock here would protect nothing and add a deadlock ordering to
	// get wrong later.
	qty, avgCost, err := currentPosition(ctx, tx, p.AccountID, p.Symbol)
	if err != nil {
		return service.PlaceOrderResult{}, err
	}

	newQty := qty + p.Quantity
	newAvg := service.WeightedAvgCost(qty, avgCost, p.Quantity, p.Price)
	if err := upsertPosition(ctx, tx, p.AccountID, p.Symbol, newQty, newAvg); err != nil {
		return service.PlaceOrderResult{}, err
	}

	// Writing a computed balance rather than `balance = balance - $1` is safe
	// here for the same reason as above, and only for that reason: this
	// transaction holds the row lock, so the value read at the top cannot have
	// moved underneath it.
	newBalance := balance - cost
	if err := updateBalance(ctx, tx, p.AccountID, newBalance); err != nil {
		return service.PlaceOrderResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return service.PlaceOrderResult{}, fmt.Errorf("committing order: %w", err)
	}

	return service.PlaceOrderResult{Order: order, Trade: trade, Balance: newBalance}, nil
}

// rejectWithin writes the rejected order and commits, leaving balance,
// positions and trades untouched. The commit is the point -- see ExecuteOrder.
func rejectWithin(ctx context.Context, tx pgx.Tx, p service.ExecuteOrderParams, reason string) error {
	if _, err := insertOrder(ctx, tx, p, service.StatusRejected, nil, &reason); err != nil {
		return fmt.Errorf("inserting rejected order: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing rejected order: %w", err)
	}
	return nil
}

// RecordRejectedOrder persists an order rejected before any transaction
// opened, because the price fetch failed and there was nothing to execute.
//
// It takes no lock: nothing is being mutated, and making a failed price lookup
// queue behind live orders would turn a market-data outage into a slowdown on
// the orders that can still fill.
func (s *PostgresTradingStore) RecordRejectedOrder(ctx context.Context, accountID uuid.UUID, symbol string, side service.Side, quantity float64, reason string) (service.Order, error) {
	p := service.ExecuteOrderParams{
		AccountID: accountID,
		Symbol:    symbol,
		Side:      side,
		Quantity:  quantity,
	}
	order, err := insertOrder(ctx, s.pool, p, service.StatusRejected, nil, &reason)
	if err != nil {
		return service.Order{}, fmt.Errorf("recording rejected order: %w", err)
	}
	return order, nil
}

func (s *PostgresTradingStore) ListOrders(ctx context.Context, accountID uuid.UUID) ([]service.Order, error) {
	rows, err := s.pool.Query(ctx, `
	SELECT id, symbol, side, quantity, status, order_type, filled_price, rejection_reason, created_at
	FROM orders
	WHERE account_id = $1
	ORDER BY created_at DESC, id DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil so an account with no orders encodes as [] rather than null. An
	// empty history is a valid answer, not a missing one.
	orders := []service.Order{}
	for rows.Next() {
		var o service.Order
		if err := rows.Scan(&o.ID, &o.Symbol, &o.Side, &o.Quantity, &o.Status,
			&o.OrderType, &o.FilledPrice, &o.RejectionReason, &o.CreatedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// ListHoldings returns open positions only. A position sold down to zero keeps
// its row -- that is what lets a later buy start a fresh cost basis with no
// special case -- but it is not something anyone holds, so it is filtered here
// rather than in every caller.
func (s *PostgresTradingStore) ListHoldings(ctx context.Context, accountID uuid.UUID) ([]service.Holding, error) {
	rows, err := s.pool.Query(ctx, `
	SELECT symbol, quantity, avg_cost
	FROM positions
	WHERE account_id = $1 AND quantity > 0
	ORDER BY symbol
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	holdings := []service.Holding{}
	for rows.Next() {
		var h service.Holding
		if err := rows.Scan(&h.Symbol, &h.Quantity, &h.AvgCost); err != nil {
			return nil, err
		}
		holdings = append(holdings, h)
	}
	return holdings, rows.Err()
}

// querier is whatever can run a statement: the pool for single writes, a
// transaction for everything inside an order. Having the helpers take this
// rather than one or the other is what lets insertOrder serve both the fill
// path and RecordRejectedOrder without a second copy of the column list.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func insertOrder(ctx context.Context, q querier, p service.ExecuteOrderParams, status string, filledPrice *float64, rejectionReason *string) (service.Order, error) {
	var o service.Order
	err := q.QueryRow(ctx, `
	INSERT INTO orders (account_id, symbol, side, quantity, status, order_type, filled_price, rejection_reason)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	RETURNING id, symbol, side, quantity, status, order_type, filled_price, rejection_reason, created_at
	`, p.AccountID, p.Symbol, p.Side, p.Quantity, status, service.OrderTypeMarket, filledPrice, rejectionReason).
		Scan(&o.ID, &o.Symbol, &o.Side, &o.Quantity, &o.Status, &o.OrderType,
			&o.FilledPrice, &o.RejectionReason, &o.CreatedAt)
	return o, err
}

func insertTrade(ctx context.Context, q querier, p service.ExecuteOrderParams, orderID uuid.UUID, realizedPL *float64) (service.Trade, error) {
	var t service.Trade
	err := q.QueryRow(ctx, `
	INSERT INTO trades (account_id, order_id, symbol, side, quantity, price, realized_pl)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	RETURNING id, order_id, symbol, side, quantity, price, realized_pl, executed_at
	`, p.AccountID, orderID, p.Symbol, p.Side, p.Quantity, p.Price, realizedPL).
		Scan(&t.ID, &t.OrderID, &t.Symbol, &t.Side, &t.Quantity, &t.Price, &t.RealizedPL, &t.ExecutedAt)
	return t, err
}

// currentPosition returns the holding's quantity and cost basis, or zeroes if
// there is no row yet. A first buy is not an error.
func currentPosition(ctx context.Context, q querier, accountID uuid.UUID, symbol string) (quantity, avgCost float64, err error) {
	err = q.QueryRow(ctx,
		`SELECT quantity, avg_cost FROM positions WHERE account_id = $1 AND symbol = $2`,
		accountID, symbol).Scan(&quantity, &avgCost)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("reading position: %w", err)
	}
	return quantity, avgCost, nil
}

func upsertPosition(ctx context.Context, q querier, accountID uuid.UUID, symbol string, quantity, avgCost float64) error {
	_, err := q.Exec(ctx, `
	INSERT INTO positions (account_id, symbol, quantity, avg_cost)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (account_id, symbol)
	DO UPDATE SET quantity = EXCLUDED.quantity, avg_cost = EXCLUDED.avg_cost, updated_at = now()
	`, accountID, symbol, quantity, avgCost)
	if err != nil {
		return fmt.Errorf("upserting position: %w", err)
	}
	return nil
}

func updateBalance(ctx context.Context, q querier, accountID uuid.UUID, balance float64) error {
	_, err := q.Exec(ctx,
		`UPDATE accounts SET balance = $1, updated_at = now() WHERE id = $2`, balance, accountID)
	if err != nil {
		return fmt.Errorf("updating balance: %w", err)
	}
	return nil
}
