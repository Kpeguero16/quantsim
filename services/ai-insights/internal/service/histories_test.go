package service_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service/mock"
)

// The mock is symbol-keyed, so "AAPL has bars, MSFT does not" is expressible
// at all -- which every interesting intersection case depends on.
func TestFetchHistories_ReturnsEachSymbolsOwnBars(t *testing.T) {
	history := &mock.HistoryClient{Bars: map[string][]service.Bar{
		"AAPL": bars(1, 2, 3),
		"SPY":  bars(1, 2),
	}}

	got, err := service.FetchHistories(context.Background(), history, []string{"SPY", "AAPL"})
	if err != nil {
		t.Fatalf("FetchHistories: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d symbols, want 2", len(got))
	}
	if len(got["AAPL"]) != 3 || len(got["SPY"]) != 2 {
		t.Errorf("bars did not come back per symbol: AAPL=%d SPY=%d", len(got["AAPL"]), len(got["SPY"]))
	}
	// Every requested symbol was actually fetched, exactly once.
	calls := history.Calls()
	sort.Strings(calls)
	if len(calls) != 2 || calls[0] != "AAPL" || calls[1] != "SPY" {
		t.Errorf("calls = %v, want [AAPL SPY]", calls)
	}
}

func TestFetchHistories_AMissingSymbolIsErrSymbolUnavailable(t *testing.T) {
	history := &mock.HistoryClient{Bars: map[string][]service.Bar{"AAPL": bars(1)}}

	_, err := service.FetchHistories(context.Background(), history, []string{"AAPL", "MSFT"})
	if !errors.Is(err, service.ErrSymbolUnavailable) {
		t.Fatalf("got %v, want ErrSymbolUnavailable", err)
	}
}

// THE determinism test. Two symbols fail; the alphabetically first must be the
// one reported, on every single run.
//
// errgroup.Wait would return whichever goroutine lost the race, so without the
// ordered scan this passes most of the time and fails occasionally -- the
// worst possible failure mode. 200 iterations is what makes a scheduling
// dependency show up as a failure rather than as flakiness someone reruns.
func TestFetchHistories_NamesTheAlphabeticallyFirstFailureEveryTime(t *testing.T) {
	errAAPL := errors.New("aapl exploded")
	errMSFT := errors.New("msft exploded")

	for i := 0; i < 200; i++ {
		history := &mock.HistoryClient{
			Bars: map[string][]service.Bar{"SPY": bars(1)},
			Errs: map[string]error{"AAPL": errAAPL, "MSFT": errMSFT},
		}

		// Deliberately supplied in reverse order: FetchHistories sorts
		// internally, so the caller's ordering must not matter either.
		_, err := service.FetchHistories(context.Background(), history,
			[]string{"SPY", "MSFT", "AAPL"})

		if !errors.Is(err, errAAPL) {
			t.Fatalf("run %d: got %v, want the alphabetically first failure (%v)", i, err, errAAPL)
		}
	}
}

// A failing fetch must not cancel its siblings: FetchHistories uses a
// zero-value errgroup, not WithContext. A cancelled sibling would report a
// context error in place of the real one, which is the nondeterminism the
// ordered scan exists to remove.
func TestFetchHistories_AFailureDoesNotCancelSiblings(t *testing.T) {
	boom := errors.New("boom")
	history := &mock.HistoryClient{
		Bars: map[string][]service.Bar{"SPY": bars(1), "TSLA": bars(1)},
		Errs: map[string]error{"AAPL": boom},
	}

	if _, err := service.FetchHistories(context.Background(), history,
		[]string{"AAPL", "SPY", "TSLA"}); !errors.Is(err, boom) {
		t.Fatalf("got %v, want boom", err)
	}

	// All three were still attempted -- nothing was cut short.
	if calls := history.Calls(); len(calls) != 3 {
		t.Errorf("got %d calls (%v), want all 3 attempted", len(calls), calls)
	}
}

func TestFetchHistories_NoSymbolsIsAnEmptyMapAndNoError(t *testing.T) {
	got, err := service.FetchHistories(context.Background(), &mock.HistoryClient{}, nil)
	if err != nil {
		t.Fatalf("FetchHistories: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want an empty map", got)
	}
}
