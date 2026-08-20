package service_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/backtesting/internal/service"
	"github.com/kpeguero/quantsim/services/backtesting/internal/service/mock"
)

func newService(t *testing.T) (*service.Service, *mock.HistoryClient, *mock.BacktestStore) {
	t.Helper()
	history := &mock.HistoryClient{}
	store := &mock.BacktestStore{}
	return service.NewService(store, history), history, store
}

func barsOverRange(start time.Time, closes []float64) []service.Bar {
	bars := make([]service.Bar, len(closes))
	for i, c := range closes {
		bars[i] = service.Bar{Timestamp: start.AddDate(0, 0, i), Open: c, Close: c}
	}
	return bars
}

// maCrossoverParamsJSON builds a {short_window, long_window} params body --
// this file's stand-in for the JSON a real client would send, now that
// RunBacktestRequest carries a discriminated {strategy, params} pair
// (Step 18 SPEC.md §2.6) rather than top-level ShortWindow/LongWindow
// fields.
func maCrossoverParamsJSON(short, long int) []byte {
	return []byte(fmt.Sprintf(`{"short_window":%d,"long_window":%d}`, short, long))
}

// symbolList builds n distinct symbols in DESCENDING order, so any test
// asserting sorted output would fail on an implementation that merely
// preserved the caller's order.
func symbolList(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("S%02d", n-i)
	}
	return out
}

func validRequest() service.RunBacktestRequest {
	return service.RunBacktestRequest{
		Symbols:         []string{"aapl"},
		Strategy:        service.StrategyMACrossover,
		Params:          maCrossoverParamsJSON(2, 3),
		StartDate:       "2025-01-01",
		EndDate:         "2025-01-20",
		StartingCapital: 1000,
	}
}

// TestRunBacktest_ValidationRejectsBeforeAnyHistoryFetch covers every check
// in §2.8 that is decidable from the request alone -- both the checks
// validateRequest still makes directly (symbol, starting capital, dates)
// and the ones it now delegates to NewStrategy (SPEC.md Step 18 §2.1:
// window bounds, an unknown strategy, malformed params). history.Calls() must
// stay empty for all of these -- a validation failure has no business
// making a network call.
func TestRunBacktest_ValidationRejectsBeforeAnyHistoryFetch(t *testing.T) {
	cases := []struct {
		name string
		mod  func(r *service.RunBacktestRequest)
	}{
		{"nil symbols", func(r *service.RunBacktestRequest) { r.Symbols = nil }},
		{"empty symbol list", func(r *service.RunBacktestRequest) { r.Symbols = []string{} }},
		{"whitespace-only entry", func(r *service.RunBacktestRequest) { r.Symbols = []string{"AAPL", "  "} }},
		{"eleven symbols", func(r *service.RunBacktestRequest) { r.Symbols = symbolList(11) }},
		{"exact duplicate", func(r *service.RunBacktestRequest) { r.Symbols = []string{"AAPL", "AAPL"} }},
		{"mixed-case duplicate", func(r *service.RunBacktestRequest) { r.Symbols = []string{"aapl", "AAPL"} }},
		{"duplicate after trimming", func(r *service.RunBacktestRequest) { r.Symbols = []string{"AAPL", " aapl "} }},
		{"short window too small", func(r *service.RunBacktestRequest) { r.Params = maCrossoverParamsJSON(1, 3) }},
		{"long window not greater than short", func(r *service.RunBacktestRequest) { r.Params = maCrossoverParamsJSON(2, 2) }},
		{"long window exceeds the fixed bound", func(r *service.RunBacktestRequest) { r.Params = maCrossoverParamsJSON(2, 501) }},
		{"non-positive starting capital", func(r *service.RunBacktestRequest) { r.StartingCapital = 0 }},
		{"malformed start date", func(r *service.RunBacktestRequest) { r.StartDate = "01/01/2025" }},
		{"malformed end date", func(r *service.RunBacktestRequest) { r.EndDate = "not-a-date" }},
		{"start not before end", func(r *service.RunBacktestRequest) { r.StartDate, r.EndDate = r.EndDate, r.StartDate }},
		{"unknown strategy kind", func(r *service.RunBacktestRequest) { r.Strategy = "not-a-real-strategy" }},
		{"malformed params JSON", func(r *service.RunBacktestRequest) { r.Params = []byte("not json") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, history, _ := newService(t)
			req := validRequest()
			tc.mod(&req)

			_, err := svc.RunBacktest(context.Background(), uuid.New(), req)

			if !errors.Is(err, service.ErrInvalidRequest) {
				t.Fatalf("got err %v, want ErrInvalidRequest", err)
			}
			if len(history.Calls()) != 0 {
				t.Errorf("history was fetched (%v) for a request that should have failed validation first", history.Calls())
			}
		})
	}
}

// TestRunBacktest_SymbolIsUppercasedBeforeFetch ensures the client always
// sees a normalized symbol, matching market-data's own uppercasing.
func TestRunBacktest_SymbolIsUppercasedBeforeFetch(t *testing.T) {
	svc, history, _ := newService(t)
	history.Bars = barsOverRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), []float64{1, 2, 3, 4, 5})

	req := validRequest()
	req.Symbols = []string{"aapl"}

	if _, err := svc.RunBacktest(context.Background(), uuid.New(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls := history.Calls(); len(calls) != 1 || calls[0] != "AAPL" {
		t.Errorf("history.Calls() = %v, want a single call with AAPL", calls)
	}
}

// TestRunBacktest_SymbolListIsNormalizedAndCappedAtTen covers the accepting
// side of §2.4's boundary -- ten is allowed where eleven is not -- and plan
// D4's contract that what gets STORED is uppercased and sorted, not the order
// the client happened to type.
func TestRunBacktest_SymbolListIsNormalizedAndCappedAtTen(t *testing.T) {
	svc, history, store := newService(t)
	history.Bars = barsOverRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), []float64{1, 2, 3, 4, 5})

	req := validRequest()
	req.Symbols = symbolList(maxSymbolsUnderTest)
	req.Symbols[0] = "  " + strings.ToLower(req.Symbols[0]) + "  "

	if _, err := svc.RunBacktest(context.Background(), uuid.New(), req); err != nil {
		t.Fatalf("ten symbols should be accepted, got %v", err)
	}

	want := symbolList(maxSymbolsUnderTest)
	sort.Strings(want)
	saved := store.Saved[0].Symbols
	if !slices.Equal(saved, want) {
		t.Errorf("saved.Symbols = %q, want %q (uppercased, sorted)", saved, want)
	}
}

// maxSymbolsUnderTest restates §2.4's cap rather than reading it from the
// package -- an external test binding to the constant would follow it if it
// were ever changed by accident, and the cap is a spec decision, not an
// implementation detail.
const maxSymbolsUnderTest = 10

// TestRunBacktest_HistoryErrorsPropagateUnwrapped: whatever the client
// returns (symbol unavailable or upstream unavailable) is exactly what the
// caller sees -- RunBacktest does not reinterpret it.
func TestRunBacktest_HistoryErrorsPropagateUnwrapped(t *testing.T) {
	for _, wantErr := range []error{service.ErrSymbolUnavailable, service.ErrUpstreamUnavailable} {
		t.Run(wantErr.Error(), func(t *testing.T) {
			svc, history, store := newService(t)
			history.Err = wantErr

			_, err := svc.RunBacktest(context.Background(), uuid.New(), validRequest())

			if !errors.Is(err, wantErr) {
				t.Fatalf("got err %v, want %v", err, wantErr)
			}
			if len(store.Saved) != 0 {
				t.Error("a backtest was saved despite the history fetch failing")
			}
		})
	}
}

// TestRunBacktest_DateRangeWithNoOverlapIsRejected: the symbol has history,
// but none of it falls inside [start_date, end_date] -- running on zero bars
// would silently answer a different question than the one asked (SPEC.md
// §2.8).
func TestRunBacktest_DateRangeWithNoOverlapIsRejected(t *testing.T) {
	svc, history, store := newService(t)
	history.Bars = barsOverRange(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), []float64{1, 2, 3, 4, 5})

	req := validRequest() // asks for Jan 2025, history is Jan 2024
	_, err := svc.RunBacktest(context.Background(), uuid.New(), req)

	if !errors.Is(err, service.ErrDateRangeUnavailable) {
		t.Fatalf("got err %v, want ErrDateRangeUnavailable", err)
	}
	if len(store.Saved) != 0 {
		t.Error("a backtest was saved despite an empty date range")
	}
}

// TestRunBacktest_WarmupExceedingTheRangedBarsIsInvalid: the date range is
// valid but leaves fewer bars than the strategy's WarmupBars needs (SPEC.md
// Step 18 §2.4) -- caught after the fetch, since it depends on how many
// bars actually fall in range.
func TestRunBacktest_WarmupExceedingTheRangedBarsIsInvalid(t *testing.T) {
	svc, history, store := newService(t)
	history.Bars = barsOverRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), []float64{1, 2, 3}) // only 3 bars

	req := validRequest()
	req.Params = maCrossoverParamsJSON(2, 10) // exceeds the 3 bars in range

	_, err := svc.RunBacktest(context.Background(), uuid.New(), req)

	if !errors.Is(err, service.ErrInvalidRequest) {
		t.Fatalf("got err %v, want ErrInvalidRequest", err)
	}
	if len(store.Saved) != 0 {
		t.Error("a backtest was saved despite an unsatisfiable warm-up")
	}
}

// TestRunBacktest_HappyPathSavesAndReturnsTheResult runs the full pipeline
// against a bar series with a clean, known crossover and checks the result
// that comes back matches what the store was asked to save, including the
// strategy identity now that Strategy/Params replace ShortWindow/LongWindow
// (Step 18 SPEC.md §2.5/§2.6).
func TestRunBacktest_HappyPathSavesAndReturnsTheResult(t *testing.T) {
	svc, history, store := newService(t)
	closes := []float64{10, 10, 10, 10, 20, 30, 40, 50, 40, 30, 20, 10}
	history.Bars = barsOverRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), closes)

	req := validRequest()
	req.StartDate = "2025-01-01"
	req.EndDate = "2025-01-12"
	req.Params = maCrossoverParamsJSON(2, 3)

	userID := uuid.New()
	detail, err := svc.RunBacktest(context.Background(), userID, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.Saved) != 1 {
		t.Fatalf("got %d saved backtests, want 1", len(store.Saved))
	}
	saved := store.Saved[0]
	if saved.UserID != userID {
		t.Errorf("saved.UserID = %v, want %v", saved.UserID, userID)
	}
	if len(saved.Symbols) != 1 || saved.Symbols[0] != "AAPL" {
		t.Errorf("saved.Symbols = %q, want [AAPL]", saved.Symbols)
	}
	if saved.Strategy != service.StrategyMACrossover {
		t.Errorf("saved.Strategy = %q, want %q", saved.Strategy, service.StrategyMACrossover)
	}
	if got := string(saved.Params); got != `{"short_window":2,"long_window":3}` {
		t.Errorf("saved.Params = %s, want the canonical re-encoding", got)
	}
	if detail.ID != saved.ID {
		t.Errorf("returned detail id %v does not match what was saved %v", detail.ID, saved.ID)
	}
	// The known series (see strategy_test.go's identical closes) crosses up
	// once and down once, so exactly one buy and one sell should have been
	// simulated and handed to the store alongside the summary row.
	if len(store.SavedTrades[0]) != 2 {
		t.Errorf("got %d saved trades, want 2 (one buy, one sell)", len(store.SavedTrades[0]))
	}
}

// TestGetBacktest_NotFoundPropagates: a nonexistent or non-owned id is
// indistinguishable at this layer, by design (SPEC.md §2.7) -- the service
// does nothing but pass the store's ErrNotFound through.
func TestGetBacktest_NotFoundPropagates(t *testing.T) {
	svc, _, store := newService(t)
	store.GetErr = service.ErrNotFound

	_, err := svc.GetBacktest(context.Background(), uuid.New(), uuid.New())

	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("got err %v, want ErrNotFound", err)
	}
}

// --- Step 19 T6: multi-symbol orchestration ---------------------------------

// barsOnDays builds bars on specific day-of-January offsets, so two symbols
// can be given deliberately different trading calendars.
func barsOnDays(days []int, closes []float64) []service.Bar {
	bars := make([]service.Bar, len(days))
	for i, d := range days {
		bars[i] = service.Bar{
			Timestamp: time.Date(2025, 1, d, 0, 0, 0, 0, time.UTC),
			Open:      closes[i],
			Close:     closes[i],
		}
	}
	return bars
}

// TestRunBacktest_EverySymbolIsFetched: the run fans out to all N symbols,
// not just the first. Order is not asserted -- the fetches are concurrent
// (§2.6), so only the set is deterministic.
func TestRunBacktest_EverySymbolIsFetched(t *testing.T) {
	svc, history, _ := newService(t)
	history.Bars = barsOverRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), []float64{1, 2, 3, 4, 5})

	req := validRequest()
	req.Symbols = []string{"msft", "aapl", "nvda"}

	if _, err := svc.RunBacktest(context.Background(), uuid.New(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := history.Calls()
	sort.Strings(calls)
	if want := []string{"AAPL", "MSFT", "NVDA"}; !slices.Equal(calls, want) {
		t.Errorf("history.Calls() = %v, want %v", calls, want)
	}
}

// TestRunBacktest_TheAlphabeticallyFirstFailureIsReported is the reason
// fetchHistories scans its own error slice instead of returning
// errgroup.Wait's error (§2.6). Two symbols fail; whichever goroutine happens
// to finish first must not decide which one the caller hears about. Repeated,
// because a scheduling-dependent implementation would pass this once.
func TestRunBacktest_TheAlphabeticallyFirstFailureIsReported(t *testing.T) {
	for run := 0; run < 200; run++ {
		svc, history, store := newService(t)
		history.Bars = barsOverRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), []float64{1, 2, 3, 4, 5})
		history.ErrBySymbol = map[string]error{
			"MSFT": fmt.Errorf("%w: MSFT", service.ErrSymbolUnavailable),
			"NVDA": fmt.Errorf("%w: NVDA", service.ErrSymbolUnavailable),
		}

		req := validRequest()
		req.Symbols = []string{"nvda", "aapl", "msft"}

		_, err := svc.RunBacktest(context.Background(), uuid.New(), req)

		if !errors.Is(err, service.ErrSymbolUnavailable) {
			t.Fatalf("run %d: got err %v, want ErrSymbolUnavailable", run, err)
		}
		if !strings.Contains(err.Error(), "MSFT") {
			t.Fatalf("run %d: err %q should name MSFT, the alphabetically first failure", run, err)
		}
		if len(store.Saved) != 0 {
			t.Fatalf("run %d: a backtest was saved despite a failed fetch", run)
		}
	}
}

// TestRunBacktest_OneUnavailableSymbolFailsTheWholeRun: never a silent N-1
// (§2.1). AAPL and MSFT are perfectly runnable on their own; the run still
// fails because NVDA is not.
func TestRunBacktest_OneUnavailableSymbolFailsTheWholeRun(t *testing.T) {
	svc, history, store := newService(t)
	history.Bars = barsOverRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), []float64{1, 2, 3, 4, 5})
	history.ErrBySymbol = map[string]error{"NVDA": service.ErrSymbolUnavailable}

	req := validRequest()
	req.Symbols = []string{"aapl", "msft", "nvda"}

	_, err := svc.RunBacktest(context.Background(), uuid.New(), req)

	if !errors.Is(err, service.ErrSymbolUnavailable) {
		t.Fatalf("got err %v, want ErrSymbolUnavailable", err)
	}
	if len(store.Saved) != 0 {
		t.Error("a backtest was saved despite one symbol being unavailable")
	}
}

// TestRunBacktest_WarmupIsCheckedAgainstTheIntersectedTimeline: AAPL has 10
// bars and MSFT has 10, but they share only 3 days. The run has 3 bars, not
// 10, and the error message must say so -- this is the assertion that proves
// alignment happens BEFORE the warm-up check rather than after it.
func TestRunBacktest_WarmupIsCheckedAgainstTheIntersectedTimeline(t *testing.T) {
	svc, history, store := newService(t)
	history.BarsBySymbol = map[string][]service.Bar{
		"AAPL": barsOnDays([]int{1, 2, 3, 4, 5, 6, 7}, []float64{1, 2, 3, 4, 5, 6, 7}),
		"MSFT": barsOnDays([]int{5, 6, 7, 8, 9, 10, 11}, []float64{5, 6, 7, 8, 9, 10, 11}),
	}

	req := validRequest()
	req.Symbols = []string{"aapl", "msft"}
	req.Params = maCrossoverParamsJSON(2, 5) // needs 5 bars; the overlap is 3

	_, err := svc.RunBacktest(context.Background(), uuid.New(), req)

	if !errors.Is(err, service.ErrInvalidRequest) {
		t.Fatalf("got err %v, want ErrInvalidRequest", err)
	}
	if !strings.Contains(err.Error(), "the 3 bars available") {
		t.Errorf("err = %q, want it to report the 3 INTERSECTED bars, not either symbol's own 7", err)
	}
	if len(store.Saved) != 0 {
		t.Error("a backtest was saved despite an unsatisfiable warm-up")
	}
}

// TestRunBacktest_DisjointCalendarsAreADateRangeFailure: both symbols have
// history in range, but not one day in common, so there is no timeline to
// simulate on (§2.1). Same outcome as a single symbol with no bars in range.
func TestRunBacktest_DisjointCalendarsAreADateRangeFailure(t *testing.T) {
	svc, history, store := newService(t)
	history.BarsBySymbol = map[string][]service.Bar{
		"AAPL": barsOnDays([]int{1, 3, 5, 7}, []float64{1, 2, 3, 4}),
		"MSFT": barsOnDays([]int{2, 4, 6, 8}, []float64{1, 2, 3, 4}),
	}

	req := validRequest()
	req.Symbols = []string{"aapl", "msft"}

	_, err := svc.RunBacktest(context.Background(), uuid.New(), req)

	if !errors.Is(err, service.ErrDateRangeUnavailable) {
		t.Fatalf("got err %v, want ErrDateRangeUnavailable", err)
	}
	if len(store.Saved) != 0 {
		t.Error("a backtest was saved despite an empty intersection")
	}
}

// TestRunBacktest_PortfolioRunTradesEverySymbol proves the portfolio engine is
// what runs -- not the single-symbol pipeline pointed at Symbols[0]. Both
// symbols follow the same known crossover series, so both trade, and every
// trade carries its own symbol.
func TestRunBacktest_PortfolioRunTradesEverySymbol(t *testing.T) {
	svc, history, store := newService(t)
	closes := []float64{10, 10, 10, 10, 20, 30, 40, 50, 40, 30, 20, 10}
	history.Bars = barsOverRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), closes)

	req := validRequest()
	req.Symbols = []string{"msft", "aapl"}
	req.StartDate = "2025-01-01"
	req.EndDate = "2025-01-12"

	detail, err := svc.RunBacktest(context.Background(), uuid.New(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	traded := map[string]int{}
	for _, tr := range detail.Trades {
		traded[tr.Symbol]++
	}
	if traded["AAPL"] == 0 || traded["MSFT"] == 0 {
		t.Errorf("trades by symbol = %v, want both AAPL and MSFT to have traded", traded)
	}
	if len(store.SavedTrades[0]) != len(detail.Trades) {
		t.Errorf("saved %d trades but returned %d", len(store.SavedTrades[0]), len(detail.Trades))
	}
}
