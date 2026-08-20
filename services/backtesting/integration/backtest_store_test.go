//go:build integration

package integration

import (
	"encoding/json"
	"errors"
	"slices"
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
func testBacktest(userID uuid.UUID, symbols []string, profitFactor *float64) service.Backtest {
	return service.Backtest{
		UserID:          userID,
		Symbols:         symbols,
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

	b := testBacktest(userID, []string{"AAPL"}, float64Ptr(2.5))
	trades := []service.TradeRecord{
		{Symbol: "AAPL", Side: service.SideBuy, BarTimestamp: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC), Price: 150, Quantity: 66.6667},
		{Symbol: "AAPL", Side: service.SideSell, BarTimestamp: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), Price: 157.5, Quantity: 66.6667, RealizedPL: float64Ptr(500)},
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

	if !slices.Equal(detail.Symbols, []string{"AAPL"}) {
		t.Errorf("Symbols = %q, want [AAPL]", detail.Symbols)
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

	saved, err := s.SaveBacktest(ctx, testBacktest(userID, []string{"MSFT"}, nil), nil)
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

	saved, err := s.SaveBacktest(ctx, testBacktest(owner, []string{"GOOGL"}, nil), nil)
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

	if _, err := s.SaveBacktest(ctx, testBacktest(userA, []string{"AAPL"}, nil), nil); err != nil {
		t.Fatalf("seeding userA backtest 1: %v", err)
	}
	if _, err := s.SaveBacktest(ctx, testBacktest(userA, []string{"MSFT"}, nil), nil); err != nil {
		t.Fatalf("seeding userA backtest 2: %v", err)
	}
	if _, err := s.SaveBacktest(ctx, testBacktest(userB, []string{"TSLA"}, nil), nil); err != nil {
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
		if slices.Contains(b.Symbols, "TSLA") {
			t.Error("userA's list includes userB's TSLA run")
		}
	}

	listB, err := s.ListBacktests(ctx, userB)
	if err != nil {
		t.Fatalf("ListBacktests(userB): %v", err)
	}
	if len(listB) != 1 || !slices.Equal(listB[0].Symbols, []string{"TSLA"}) {
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
			b := testBacktest(userID, []string{"AAPL"}, nil)
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

// TestGetBacktest_TradeLogRoundTripsInTheOrderItWasWritten pins what Step 19
// changed here. The store used to re-sort the log by (bar_timestamp, id) and
// leaned on ties being impossible, which held only while one run meant one
// symbol and so at most one fill per bar. A portfolio run fills several
// symbols on the same bar routinely and id is a random UUID, so a same-bar
// group came back in a different order on every write -- a sell could be
// listed under the same-bar buy it funded.
//
// The log is now stored as the sequence it is, and read back by seq. The
// fixture puts THREE trades on one identical timestamp, in the order
// SimulatePortfolio emits them (sells first, then buys alphabetically); if the
// ordering were still derived from the row values, no ORDER BY over them could
// tell these three apart.
func TestGetBacktest_TradeLogRoundTripsInTheOrderItWasWritten(t *testing.T) {
	s, _, ctx := newStore(t)
	userID := seedUser(t, ctx, testPool)

	sameBar := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	earlier := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	trades := []service.TradeRecord{
		{Symbol: "MSFT", Side: service.SideBuy, BarTimestamp: earlier, Price: 150, Quantity: 10},
		{Symbol: "MSFT", Side: service.SideSell, BarTimestamp: sameBar, Price: 160, Quantity: 10, RealizedPL: float64Ptr(100)},
		{Symbol: "AAPL", Side: service.SideBuy, BarTimestamp: sameBar, Price: 200, Quantity: 5},
		{Symbol: "NVDA", Side: service.SideBuy, BarTimestamp: sameBar, Price: 300, Quantity: 2},
	}

	saved, err := s.SaveBacktest(ctx, testBacktest(userID, []string{"AAPL", "MSFT", "NVDA"}, nil), trades)
	if err != nil {
		t.Fatalf("SaveBacktest: %v", err)
	}

	detail, err := s.GetBacktest(ctx, userID, saved.ID)
	if err != nil {
		t.Fatalf("GetBacktest: %v", err)
	}

	if len(detail.Trades) != len(trades) {
		t.Fatalf("got %d trades, want %d", len(detail.Trades), len(trades))
	}
	for i, want := range trades {
		got := detail.Trades[i]
		if got.Symbol != want.Symbol || got.Side != want.Side || !got.BarTimestamp.Equal(want.BarTimestamp) {
			t.Errorf("trades[%d] = %s %s %s, want %s %s %s", i,
				got.Symbol, got.Side, got.BarTimestamp.Format(time.DateOnly),
				want.Symbol, want.Side, want.BarTimestamp.Format(time.DateOnly))
		}
	}
}

// TestSaveBacktest_PortfolioTradeLogRoundTripsPerSymbol is D5's real check:
// that a portfolio run's interleaved log comes back with every trade still
// attached to the symbol that produced it, through actual Postgres rather
// than a Scan that merely compiles. Four fills across three symbols, no two
// adjacent rows sharing a symbol, so a store that dropped the column or
// reused one row's symbol for its neighbours cannot pass by coincidence.
//
// Quantities are asserted through numeric(), against the 4-decimal value the
// column can actually hold rather than the float the fixture was written
// with. This matters more at N>1 than it ever did at N=1: a position is now
// funded by equity/N, so the same fixed 0.0001 granularity covers a larger
// share of a smaller position. Comparing the Go float to itself would hide
// exactly that. The stored value is the authority, and the value GetBacktest
// hands back is checked to agree with it -- a round trip is only a round
// trip if the number that comes back is the number the database kept.
func TestSaveBacktest_PortfolioTradeLogRoundTripsPerSymbol(t *testing.T) {
	s, pool, ctx := newStore(t)
	userID := seedUser(t, ctx, testPool)

	bar1 := time.Date(2025, 2, 3, 0, 0, 0, 0, time.UTC)
	bar2 := time.Date(2025, 2, 10, 0, 0, 0, 0, time.UTC)

	// Quantities carry six decimals so 4-decimal storage has something to do:
	// one rounds down, one rounds up at the half, one rounds up from a 6.
	trades := []service.TradeRecord{
		{Symbol: "AAPL", Side: service.SideBuy, BarTimestamp: bar1, Price: 437.2891, Quantity: 7.622713},
		{Symbol: "MSFT", Side: service.SideBuy, BarTimestamp: bar1, Price: 414.1234, Quantity: 8.049155},
		{Symbol: "NVDA", Side: service.SideBuy, BarTimestamp: bar1, Price: 125.0, Quantity: 26.666666},
		{Symbol: "MSFT", Side: service.SideSell, BarTimestamp: bar2, Price: 420.5, Quantity: 8.049155, RealizedPL: float64Ptr(51.34)},
	}
	// What NUMERIC(20,4) keeps, rounding half away from zero.
	wantStored := []float64{7.6227, 8.0492, 26.6667, 8.0492}

	saved, err := s.SaveBacktest(ctx, testBacktest(userID, []string{"AAPL", "MSFT", "NVDA"}, float64Ptr(1.8)), trades)
	if err != nil {
		t.Fatalf("SaveBacktest: %v", err)
	}

	detail, err := s.GetBacktest(ctx, userID, saved.ID)
	if err != nil {
		t.Fatalf("GetBacktest: %v", err)
	}
	if !slices.Equal(detail.Symbols, []string{"AAPL", "MSFT", "NVDA"}) {
		t.Errorf("Symbols = %q, want [AAPL MSFT NVDA]", detail.Symbols)
	}
	if len(detail.Trades) != len(trades) {
		t.Fatalf("got %d trades, want %d", len(detail.Trades), len(trades))
	}

	for i, want := range trades {
		got := detail.Trades[i]
		if got.Symbol != want.Symbol {
			t.Errorf("trades[%d].Symbol = %q, want %q -- a portfolio fill that cannot say which symbol it was is unreadable",
				i, got.Symbol, want.Symbol)
		}
		if got.Side != want.Side || !got.BarTimestamp.Equal(want.BarTimestamp) {
			t.Errorf("trades[%d] = %s %s, want %s %s", i,
				got.Side, got.BarTimestamp.Format(time.DateOnly),
				want.Side, want.BarTimestamp.Format(time.DateOnly))
		}

		stored := numeric(t, ctx, pool,
			`SELECT quantity::text FROM backtest_trades WHERE backtest_id = $1 AND seq = $2`, saved.ID, i)
		if stored != wantStored[i] {
			t.Errorf("trades[%d] quantity stored as %v, want %v", i, stored, wantStored[i])
		}
		if got.Quantity != stored {
			t.Errorf("trades[%d].Quantity = %v but the database holds %v; the value read back must be the value kept",
				i, got.Quantity, stored)
		}
	}

	// The sell's realized P/L is the one figure with no counterpart anywhere
	// else in the row, so it gets its own read.
	if pl := numeric(t, ctx, pool,
		`SELECT realized_pl::text FROM backtest_trades WHERE backtest_id = $1 AND seq = 3`, saved.ID); pl != 51.34 {
		t.Errorf("sell realized_pl stored as %v, want 51.34", pl)
	}
}

// TestSaveBacktest_TradeSequenceIsUniquePerRun: seq is what makes the log a
// sequence rather than a set, so the schema refuses a duplicate outright
// instead of silently restoring the arbitrary order it was added to remove.
func TestSaveBacktest_TradeSequenceIsUniquePerRun(t *testing.T) {
	s, _, ctx := newStore(t)
	userID := seedUser(t, ctx, testPool)

	saved, err := s.SaveBacktest(ctx, testBacktest(userID, []string{"AAPL"}, nil), []service.TradeRecord{
		{Symbol: "AAPL", Side: service.SideBuy, BarTimestamp: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC), Price: 150, Quantity: 10},
	})
	if err != nil {
		t.Fatalf("SaveBacktest: %v", err)
	}

	_, err = testPool.Exec(ctx, `
	INSERT INTO backtest_trades (backtest_id, seq, symbol, side, bar_timestamp, price, quantity)
	VALUES ($1, 0, 'AAPL', 'sell', $2, 160, 10)
	`, saved.ID, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("a second trade at seq 0 was accepted; (backtest_id, seq) is not unique")
	}
}
