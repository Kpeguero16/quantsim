package service_test

import (
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// day builds a bar timestamp for 2026-03-NN. Bars are stamped mid-session
// rather than at midnight so the tests exercise Day's normalization rather
// than accidentally passing already-normalized values through it.
func day(n int) time.Time {
	return time.Date(2026, 3, n, 20, 0, 0, 0, time.UTC)
}

// midnight is what Calendar returns: the same date, canonicalized.
func midnight(n int) time.Time {
	return time.Date(2026, 3, n, 0, 0, 0, 0, time.UTC)
}

// bars turns day numbers into a bar series. Close is derived from the day so
// a test can tell which bar it is looking at.
func bars(days ...int) []service.Bar {
	out := make([]service.Bar, 0, len(days))
	for _, d := range days {
		out = append(out, service.Bar{Timestamp: day(d), Close: float64(100 + d)})
	}
	return out
}

func assertDates(t *testing.T, got []time.Time, want ...int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d dates (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i, n := range want {
		if !got[i].Equal(midnight(n)) {
			t.Errorf("date %d: got %v, want %v", i, got[i], midnight(n))
		}
	}
}

func TestCalendar_IntersectsEverySymbol(t *testing.T) {
	tests := []struct {
		name string
		in   map[string][]service.Bar
		want []int
	}{
		{
			name: "one symbol is its own dates",
			in:   map[string][]service.Bar{"AAPL": bars(1, 2, 3)},
			want: []int{1, 2, 3},
		},
		{
			name: "identical calendars intersect to themselves",
			in: map[string][]service.Bar{
				"AAPL": bars(1, 2, 3),
				"SPY":  bars(1, 2, 3),
			},
			want: []int{1, 2, 3},
		},
		// The core rule: a gap in ONE symbol removes that date for everyone.
		// No carry-forward -- day 2 is simply not a day this portfolio can be
		// valued on, rather than a day AAPL's day-1 close stands in for.
		{
			name: "a one-day gap in a single symbol removes the date entirely",
			in: map[string][]service.Bar{
				"AAPL": bars(1, 3),
				"SPY":  bars(1, 2, 3),
			},
			want: []int{1, 3},
		},
		{
			name: "a short benchmark truncates the whole calendar",
			in: map[string][]service.Bar{
				"AAPL": bars(1, 2, 3, 4, 5),
				"SPY":  bars(1, 2),
			},
			want: []int{1, 2},
		},
		// A symbol bought and fully sold mid-window still constrains the
		// calendar for the days it was held.
		{
			name: "a symbol present only mid-window narrows the intersection",
			in: map[string][]service.Bar{
				"AAPL": bars(1, 2, 3, 4, 5),
				"TSLA": bars(2, 3),
				"SPY":  bars(1, 2, 3, 4, 5),
			},
			want: []int{2, 3},
		},
		{
			name: "fully disjoint calendars intersect to nothing",
			in: map[string][]service.Bar{
				"AAPL": bars(1, 2),
				"SPY":  bars(3, 4),
			},
			want: nil,
		},
		{
			name: "a symbol with no bars at all empties the calendar",
			in: map[string][]service.Bar{
				"AAPL": bars(1, 2, 3),
				"SPY":  {},
			},
			want: nil,
		},
		{
			name: "no symbols returns nothing, not every date",
			in:   map[string][]service.Bar{},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDates(t, service.Calendar(tt.in), tt.want...)
		})
	}
}

// Unsorted input must not produce unsorted output. Built descending so any
// implementation that preserves encounter order fails.
func TestCalendar_SortsAscendingFromDescendingInput(t *testing.T) {
	got := service.Calendar(map[string][]service.Bar{
		"AAPL": bars(5, 4, 3, 2, 1),
		"SPY":  bars(5, 4, 3, 2, 1),
	})
	assertDates(t, got, 1, 2, 3, 4, 5)
}

// Six symbols, twenty runs.
//
// Six is not arbitrary: Step 19's T1 found that three symbols in a map land in
// sorted order by luck roughly one run in six, so a three-symbol fixture makes
// a sort-order bug look flaky rather than broken. Go randomizes map iteration
// per range, so this also covers Calendar being deterministic at all.
func TestCalendar_IsDeterministicAcrossRuns(t *testing.T) {
	in := map[string][]service.Bar{
		"AAPL":  bars(1, 2, 3, 4, 5, 6),
		"MSFT":  bars(1, 2, 3, 4, 5, 6),
		"GOOGL": bars(1, 2, 3, 4, 5, 6),
		"AMZN":  bars(1, 2, 3, 4, 5, 6),
		"TSLA":  bars(1, 2, 3, 4, 5, 6),
		"SPY":   bars(1, 2, 3, 4, 5, 6),
	}

	for i := 0; i < 20; i++ {
		assertDates(t, service.Calendar(in), 1, 2, 3, 4, 5, 6)
	}
}

// Timestamps are normalized to UTC midnight, so bars stamped at different
// times of day -- or in different zones -- on the same calendar date are one
// date, not several.
func TestCalendar_NormalizesTimestampsToTheirDate(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}

	got := service.Calendar(map[string][]service.Bar{
		// 2026-03-02, stamped at three different times, one of them in a
		// non-UTC zone whose UTC date is still the 2nd.
		"AAPL": {
			{Timestamp: time.Date(2026, 3, 2, 14, 30, 0, 0, time.UTC)},
			{Timestamp: time.Date(2026, 3, 2, 21, 0, 0, 0, time.UTC)},
		},
		"SPY": {
			{Timestamp: time.Date(2026, 3, 2, 9, 30, 0, 0, ny)}, // 14:30 UTC
		},
	})

	assertDates(t, got, 2)
}

// A symbol arriving with several bars for one day must not satisfy the
// intersection on its own. market-data ingests only 1Day bars today, so this
// cannot happen -- but it is the exact silent failure Step 19 recorded against
// alignBars, and counting bars instead of distinct symbols would reintroduce
// it. Here AAPL has two bars on day 2 and SPY has none.
func TestCalendar_DuplicateBarsForOneDayDoNotSatisfyTheIntersection(t *testing.T) {
	got := service.Calendar(map[string][]service.Bar{
		"AAPL": {
			{Timestamp: day(2), Close: 100},
			{Timestamp: day(2), Close: 101},
			{Timestamp: day(3), Close: 102},
		},
		"SPY": bars(3),
	})

	assertDates(t, got, 3)
}
