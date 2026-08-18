package service_test

import (
	"context"
	"errors"
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

func validRequest() service.RunBacktestRequest {
	return service.RunBacktestRequest{
		Symbol:          "aapl",
		ShortWindow:     2,
		LongWindow:      3,
		StartDate:       "2025-01-01",
		EndDate:         "2025-01-20",
		StartingCapital: 1000,
	}
}

// TestRunBacktest_ValidationRejectsBeforeAnyHistoryFetch covers every check
// in §2.8 that is decidable from the request alone. history.Calls must stay
// empty for all of these -- a validation failure has no business making a
// network call.
func TestRunBacktest_ValidationRejectsBeforeAnyHistoryFetch(t *testing.T) {
	cases := []struct {
		name string
		mod  func(r *service.RunBacktestRequest)
	}{
		{"empty symbol", func(r *service.RunBacktestRequest) { r.Symbol = "  " }},
		{"short window too small", func(r *service.RunBacktestRequest) { r.ShortWindow = 1 }},
		{"long window not greater than short", func(r *service.RunBacktestRequest) { r.LongWindow = r.ShortWindow }},
		{"long window exceeds the fixed bound", func(r *service.RunBacktestRequest) { r.LongWindow = 501 }},
		{"non-positive starting capital", func(r *service.RunBacktestRequest) { r.StartingCapital = 0 }},
		{"malformed start date", func(r *service.RunBacktestRequest) { r.StartDate = "01/01/2025" }},
		{"malformed end date", func(r *service.RunBacktestRequest) { r.EndDate = "not-a-date" }},
		{"start not before end", func(r *service.RunBacktestRequest) { r.StartDate, r.EndDate = r.EndDate, r.StartDate }},
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
			if len(history.Calls) != 0 {
				t.Errorf("history was fetched (%v) for a request that should have failed validation first", history.Calls)
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
	req.Symbol = "aapl"

	if _, err := svc.RunBacktest(context.Background(), uuid.New(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(history.Calls) != 1 || history.Calls[0] != "AAPL" {
		t.Errorf("history.Calls = %v, want a single call with AAPL", history.Calls)
	}
}

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

// TestRunBacktest_LongWindowExceedingTheRangedBarsIsInvalid: the date range
// is valid but leaves fewer bars than long_window needs -- caught after the
// fetch, since it depends on how many bars actually fall in range.
func TestRunBacktest_LongWindowExceedingTheRangedBarsIsInvalid(t *testing.T) {
	svc, history, store := newService(t)
	history.Bars = barsOverRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), []float64{1, 2, 3}) // only 3 bars

	req := validRequest()
	req.LongWindow = 10 // exceeds the 3 bars in range
	req.ShortWindow = 2

	_, err := svc.RunBacktest(context.Background(), uuid.New(), req)

	if !errors.Is(err, service.ErrInvalidRequest) {
		t.Fatalf("got err %v, want ErrInvalidRequest", err)
	}
	if len(store.Saved) != 0 {
		t.Error("a backtest was saved despite an unsatisfiable long_window")
	}
}

// TestRunBacktest_HappyPathSavesAndReturnsTheResult runs the full pipeline
// against a bar series with a clean, known crossover and checks the result
// that comes back matches what the store was asked to save.
func TestRunBacktest_HappyPathSavesAndReturnsTheResult(t *testing.T) {
	svc, history, store := newService(t)
	closes := []float64{10, 10, 10, 10, 20, 30, 40, 50, 40, 30, 20, 10}
	history.Bars = barsOverRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), closes)

	req := validRequest()
	req.StartDate = "2025-01-01"
	req.EndDate = "2025-01-12"
	req.ShortWindow = 2
	req.LongWindow = 3

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
	if saved.Symbol != "AAPL" {
		t.Errorf("saved.Symbol = %q, want AAPL", saved.Symbol)
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
