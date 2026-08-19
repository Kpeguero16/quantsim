//go:build integration

package integration

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/backtesting/internal/service"
)

// testBacktest builds a ma_crossover fixture -- Strategy/Params replace
// Step 16's ShortWindow/LongWindow (Step 18 SPEC.md §2.5). RSI and MACD
// fixtures, and the JSONB round-trip assertions specific to the
// {strategy, params} shape, are covered separately by
// TestSaveBacktest_AllThreeStrategiesRoundTripThroughJSONB.
func testBacktest(userID uuid.UUID, symbol string, profitFactor *float64) service.Backtest {
	return service.Backtest{
		UserID:          userID,
		Symbol:          symbol,
		Strategy:        service.StrategyMACrossover,
		Params:          json.RawMessage(`{"short_window":10,"long_window":50}`),
		StartDate:       time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:         time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		StartingCapital: 10000,
		FinalEquity:     10500,
		Metrics: service.Metrics{
			TotalReturnPct: 5,
			SharpeRatio:    1.2,
			MaxDrawdownPct: 3.5,
			WinRatePct:     66.67,
			ProfitFactor:   profitFactor,
		},
	}
}

func float64Ptr(v float64) *float64 { return &v }

// TestSaveBacktest_PersistsRunAndTradeLog covers the round trip SPEC.md §2.6
// exists for: a run and its simulated fills, written together, read back
// identically -- including RealizedPL on the sell.
func TestSaveBacktest_PersistsRunAndTradeLog(t *testing.T) {
	s, _, ctx := newStore(t)
	userID := seedUser(t, ctx, testPool)

	b := testBacktest(userID, "AAPL", float64Ptr(2.5))
	trades := []service.TradeRecord{
		{Side: service.SideBuy, BarTimestamp: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC), Price: 150, Quantity: 66.6667},
		{Side: service.SideSell, BarTimestamp: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), Price: 157.5, Quantity: 66.6667, RealizedPL: float64Ptr(500)},
	}

	saved, err := s.SaveBacktest(ctx, b, trades)
	if err != nil {
		t.Fatalf("SaveBacktest: %v", err)
	}
	if saved.ID == uuid.Nil {
		t.Fatal("saved backtest has a nil ID")
	}
	if saved.CreatedAt.IsZero() {
		t.Fatal("saved backtest has a zero CreatedAt")
	}

	detail, err := s.GetBacktest(ctx, userID, saved.ID)
	if err != nil {
		t.Fatalf("GetBacktest: %v", err)
	}

	if detail.Symbol != "AAPL" {
		t.Errorf("Symbol = %q, want AAPL", detail.Symbol)
	}
	if detail.Metrics.ProfitFactor == nil || *detail.Metrics.ProfitFactor != 2.5 {
		t.Errorf("ProfitFactor = %v, want 2.5", detail.Metrics.ProfitFactor)
	}
	if len(detail.Trades) != 2 {
		t.Fatalf("got %d trades, want 2", len(detail.Trades))
	}
	sell := detail.Trades[1]
	if sell.Side != service.SideSell || sell.RealizedPL == nil || *sell.RealizedPL != 500 {
		t.Errorf("sell trade = %+v, want RealizedPL 500", sell)
	}
}

// TestSaveBacktest_NilProfitFactorIsStoredAsSQLNull is the direct check for
// SPEC.md §2.5's undefined-ratio case: the column must actually be NULL at
// the database level, not a 0 that happens to decode back as nil in Go.
func TestSaveBacktest_NilProfitFactorIsStoredAsSQLNull(t *testing.T) {
	s, pool, ctx := newStore(t)
	userID := seedUser(t, ctx, testPool)

	saved, err := s.SaveBacktest(ctx, testBacktest(userID, "MSFT", nil), nil)
	if err != nil {
		t.Fatalf("SaveBacktest: %v", err)
	}

	var isNull bool
	err = pool.QueryRow(ctx, `SELECT profit_factor IS NULL FROM backtests WHERE id = $1`, saved.ID).Scan(&isNull)
	if err != nil {
		t.Fatalf("checking profit_factor: %v", err)
	}
	if !isNull {
		t.Error("profit_factor is not SQL NULL; a nil *float64 must round-trip as NULL, not 0")
	}

	detail, err := s.GetBacktest(ctx, userID, saved.ID)
	if err != nil {
		t.Fatalf("GetBacktest: %v", err)
	}
	if detail.Metrics.ProfitFactor != nil {
		t.Errorf("ProfitFactor = %v, want nil", *detail.Metrics.ProfitFactor)
	}
}

// TestGetBacktest_WrongOwnerIsIndistinguishableFromMissing is the direct
// test of SPEC.md §2.7: a non-owner's request for a real id must fail
// exactly like a request for an id that never existed, so nothing about a
// 404 response reveals whether the id is real.
func TestGetBacktest_WrongOwnerIsIndistinguishableFromMissing(t *testing.T) {
	s, _, ctx := newStore(t)
	owner := seedUser(t, ctx, testPool)
	stranger := seedUser(t, ctx, testPool)

	saved, err := s.SaveBacktest(ctx, testBacktest(owner, "GOOGL", nil), nil)
	if err != nil {
		t.Fatalf("SaveBacktest: %v", err)
	}

	_, err = s.GetBacktest(ctx, stranger, saved.ID)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("GetBacktest(stranger, realID) = %v, want ErrNotFound", err)
	}

	_, err = s.GetBacktest(ctx, owner, uuid.New())
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("GetBacktest(owner, randomID) = %v, want ErrNotFound", err)
	}
}

// TestListBacktests_ScopedToTheCallingUser: another user's runs must never
// appear, regardless of how many exist.
func TestListBacktests_ScopedToTheCallingUser(t *testing.T) {
	s, _, ctx := newStore(t)
	userA := seedUser(t, ctx, testPool)
	userB := seedUser(t, ctx, testPool)

	if _, err := s.SaveBacktest(ctx, testBacktest(userA, "AAPL", nil), nil); err != nil {
		t.Fatalf("seeding userA backtest 1: %v", err)
	}
	if _, err := s.SaveBacktest(ctx, testBacktest(userA, "MSFT", nil), nil); err != nil {
		t.Fatalf("seeding userA backtest 2: %v", err)
	}
	if _, err := s.SaveBacktest(ctx, testBacktest(userB, "TSLA", nil), nil); err != nil {
		t.Fatalf("seeding userB backtest: %v", err)
	}

	listA, err := s.ListBacktests(ctx, userA)
	if err != nil {
		t.Fatalf("ListBacktests(userA): %v", err)
	}
	if len(listA) != 2 {
		t.Fatalf("got %d backtests for userA, want 2", len(listA))
	}
	for _, b := range listA {
		if b.Symbol == "TSLA" {
			t.Error("userA's list includes userB's TSLA run")
		}
	}

	listB, err := s.ListBacktests(ctx, userB)
	if err != nil {
		t.Fatalf("ListBacktests(userB): %v", err)
	}
	if len(listB) != 1 || listB[0].Symbol != "TSLA" {
		t.Errorf("userB's list = %+v, want exactly the TSLA run", listB)
	}
}

// TestSaveBacktest_AllThreeStrategiesRoundTripThroughJSONB saves one run per
// named strategy (SPEC.md Step 18 §2.1), reads each back, and confirms the
// reloaded params reconstruct via NewStrategy -- proving a stored run stays
// interpretable by the exact function that would need to interpret it
// again (§2.6: Params is always the canonical re-encoding, never the raw
// bytes a client sent). testBacktest's own fixture already exercises
// ma_crossover; this is what actually puts RSI's and MACD's params through
// real Postgres JSONB, which nothing else in this suite does.
func TestSaveBacktest_AllThreeStrategiesRoundTripThroughJSONB(t *testing.T) {
	s, _, ctx := newStore(t)
	userID := seedUser(t, ctx, testPool)

	cases := []struct {
		name     string
		strategy service.StrategyKind
		params   json.RawMessage
	}{
		{
			name:     "ma_crossover",
			strategy: service.StrategyMACrossover,
			params:   json.RawMessage(`{"short_window":5,"long_window":20}`),
		},
		{
			name:     "rsi",
			strategy: service.StrategyRSI,
			params:   json.RawMessage(`{"period":14,"oversold":30,"overbought":70}`),
		},
		{
			name:     "macd",
			strategy: service.StrategyMACD,
			params:   json.RawMessage(`{"fast_period":12,"slow_period":26,"signal_period":9}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := testBacktest(userID, "AAPL", nil)
			b.Strategy = tc.strategy
			b.Params = tc.params

			saved, err := s.SaveBacktest(ctx, b, nil)
			if err != nil {
				t.Fatalf("SaveBacktest: %v", err)
			}

			detail, err := s.GetBacktest(ctx, userID, saved.ID)
			if err != nil {
				t.Fatalf("GetBacktest: %v", err)
			}

			if detail.Strategy != tc.strategy {
				t.Errorf("Strategy = %q, want %q", detail.Strategy, tc.strategy)
			}

			strat, err := service.NewStrategy(detail.Strategy, detail.Params)
			if err != nil {
				t.Fatalf("NewStrategy(%q, %s) failed to reconstruct the reloaded params: %v",
					detail.Strategy, detail.Params, err)
			}
			if strat.Kind() != tc.strategy {
				t.Errorf("reconstructed Kind() = %q, want %q", strat.Kind(), tc.strategy)
			}
		})
	}
}

// TestGetBacktest_TradeLogIsOrderedByBarTimestamp: SaveBacktest is handed
// trades out of chronological order here on purpose, so this pins that the
// store's own ORDER BY -- not insertion order -- is what the caller sees.
func TestGetBacktest_TradeLogIsOrderedByBarTimestamp(t *testing.T) {
	s, _, ctx := newStore(t)
	userID := seedUser(t, ctx, testPool)

	later := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	earlier := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	trades := []service.TradeRecord{
		{Side: service.SideSell, BarTimestamp: later, Price: 160, Quantity: 10, RealizedPL: float64Ptr(100)},
		{Side: service.SideBuy, BarTimestamp: earlier, Price: 150, Quantity: 10},
	}

	saved, err := s.SaveBacktest(ctx, testBacktest(userID, "AAPL", nil), trades)
	if err != nil {
		t.Fatalf("SaveBacktest: %v", err)
	}

	detail, err := s.GetBacktest(ctx, userID, saved.ID)
	if err != nil {
		t.Fatalf("GetBacktest: %v", err)
	}

	if len(detail.Trades) != 2 {
		t.Fatalf("got %d trades, want 2", len(detail.Trades))
	}
	if !detail.Trades[0].BarTimestamp.Equal(earlier) || detail.Trades[0].Side != service.SideBuy {
		t.Errorf("trades[0] = %+v, want the earlier buy first", detail.Trades[0])
	}
	if !detail.Trades[1].BarTimestamp.Equal(later) || detail.Trades[1].Side != service.SideSell {
		t.Errorf("trades[1] = %+v, want the later sell second", detail.Trades[1])
	}
}
