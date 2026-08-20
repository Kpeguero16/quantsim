//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
)

// seedTrade writes one filled order and its trade with an explicit
// executed_at, which ExecuteOrder cannot do -- it takes the column default.
// Controlling the timestamp is the whole point: it is what lets a test insert
// history out of order and prove the ORDER BY is doing the work.
func seedTrade(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	accountID uuid.UUID, side service.Side, qty, price float64,
	realizedPL *float64, executedAt time.Time) uuid.UUID {
	t.Helper()

	var orderID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO orders (account_id, symbol, side, quantity, status, order_type, filled_price)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		accountID, symbol, side, qty, service.StatusFilled, service.OrderTypeMarket, price).Scan(&orderID)
	if err != nil {
		t.Fatalf("seeding order: %v", err)
	}

	var tradeID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO trades (account_id, order_id, symbol, side, quantity, price, realized_pl, executed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		accountID, orderID, symbol, side, qty, price, realizedPL, executedAt).Scan(&tradeID)
	if err != nil {
		t.Fatalf("seeding trade: %v", err)
	}
	return tradeID
}

func day(n int) time.Time {
	return time.Date(2026, 3, n, 15, 0, 0, 0, time.UTC)
}

// The rows are inserted in a deliberately scrambled order, so a missing or
// wrong ORDER BY fails here rather than passing on Postgres's incidental
// return order.
func TestListTrades_ReturnsOldestFirstRegardlessOfInsertionOrder(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 10000)

	for _, d := range []int{3, 1, 5, 2, 4} {
		seedTrade(t, ctx, pool, accountID, service.SideBuy, 1, float64(100+d), nil, day(d))
	}

	trades, err := s.ListTrades(ctx, accountID, service.DefaultTradeLimit)
	if err != nil {
		t.Fatalf("ListTrades: %v", err)
	}
	if len(trades) != 5 {
		t.Fatalf("got %d trades, want 5", len(trades))
	}
	for i := 1; i < len(trades); i++ {
		if trades[i].ExecutedAt.Before(trades[i-1].ExecutedAt) {
			t.Fatalf("trade %d (%v) precedes trade %d (%v) -- not ascending",
				i, trades[i].ExecutedAt, i-1, trades[i-1].ExecutedAt)
		}
	}
	if !trades[0].ExecutedAt.Equal(day(1)) || !trades[4].ExecutedAt.Equal(day(5)) {
		t.Errorf("range is %v..%v, want day 1..day 5", trades[0].ExecutedAt, trades[4].ExecutedAt)
	}
}

// Truncation keeps a complete PREFIX of history. This is the property that
// makes a capped response safe for a forward replay: the oldest trades are the
// ones reconstruction needs first, and cutting from the other end would hand
// back sells whose funding buys were dropped.
func TestListTrades_TruncationKeepsTheOldestPrefix(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 10000)

	for _, d := range []int{5, 4, 3, 2, 1} {
		seedTrade(t, ctx, pool, accountID, service.SideBuy, 1, float64(100+d), nil, day(d))
	}

	trades, err := s.ListTrades(ctx, accountID, 3)
	if err != nil {
		t.Fatalf("ListTrades: %v", err)
	}
	if len(trades) != 3 {
		t.Fatalf("got %d trades, want 3", len(trades))
	}
	for i, wantDay := range []int{1, 2, 3} {
		if !trades[i].ExecutedAt.Equal(day(wantDay)) {
			t.Errorf("trade %d is %v, want day %d -- truncation took the wrong end",
				i, trades[i].ExecutedAt, wantDay)
		}
	}
}

// realized_pl is NULL on buys and set on sells, and must survive the round
// trip as a nil pointer rather than 0 -- a break-even round trip and a buy are
// different things.
func TestListTrades_RoundTripsRealizedPLAndItsAbsence(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 10000)

	pl := -42.5
	seedTrade(t, ctx, pool, accountID, service.SideBuy, 10, 150, nil, day(1))
	seedTrade(t, ctx, pool, accountID, service.SideSell, 10, 145.75, &pl, day(2))

	trades, err := s.ListTrades(ctx, accountID, service.DefaultTradeLimit)
	if err != nil {
		t.Fatalf("ListTrades: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("got %d trades, want 2", len(trades))
	}
	if trades[0].RealizedPL != nil {
		t.Errorf("buy's realized_pl = %v, want nil", *trades[0].RealizedPL)
	}
	if trades[1].RealizedPL == nil {
		t.Fatal("sell's realized_pl is nil, want -42.5")
	}
	assertMoney(t, *trades[1].RealizedPL, pl, "sell realized_pl")
	if trades[1].Side != service.SideSell || trades[1].OrderID == uuid.Nil {
		t.Errorf("sell row came back wrong: %+v", trades[1])
	}
}

func TestListTrades_ReturnsOnlyTheGivenAccountsTrades(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, mine := seedAccount(t, ctx, pool, 10000)
	_, theirs := seedAccount(t, ctx, pool, 10000)

	seedTrade(t, ctx, pool, mine, service.SideBuy, 1, 100, nil, day(1))
	seedTrade(t, ctx, pool, theirs, service.SideBuy, 1, 200, nil, day(2))
	seedTrade(t, ctx, pool, theirs, service.SideBuy, 1, 300, nil, day(3))

	trades, err := s.ListTrades(ctx, mine, service.DefaultTradeLimit)
	if err != nil {
		t.Fatalf("ListTrades: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("got %d trades, want 1 -- another account's history leaked", len(trades))
	}
	assertMoney(t, trades[0].Price, 100, "price")
}

// An account that has never traded gets an empty slice, not nil: the handler's
// [] promise is only as good as what the store hands it.
func TestListTrades_EmptyHistoryIsNonNil(t *testing.T) {
	s, pool, ctx := newStore(t)
	_, accountID := seedAccount(t, ctx, pool, 10000)

	trades, err := s.ListTrades(ctx, accountID, service.DefaultTradeLimit)
	if err != nil {
		t.Fatalf("ListTrades: %v", err)
	}
	if trades == nil {
		t.Fatal("got nil, want an empty non-nil slice")
	}
	if len(trades) != 0 {
		t.Fatalf("got %d trades, want 0", len(trades))
	}
}
