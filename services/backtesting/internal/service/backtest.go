package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// minShortWindow, maxLongWindow bound the crossover windows (SPEC.md §2.8).
// maxLongWindow is fixed rather than derived from the request's date range --
// simpler to validate before any history fetch, and matches the ~501 bars
// any symbol has today; revisit only once a symbol's ingested range grows
// meaningfully past this.
const (
	minShortWindow = 2
	maxLongWindow  = 500
)

// dateLayout is the wire format for start_date/end_date -- calendar dates,
// not timestamps, since a backtest range has no meaningful time-of-day.
const dateLayout = "2006-01-02"

type Service struct {
	store   BacktestStore
	history HistoryClient
}

func NewService(store BacktestStore, history HistoryClient) *Service {
	return &Service{store: store, history: history}
}

// RunBacktest validates the request, fetches history, runs the strategy
// pipeline (GenerateSignals -> Simulate -> ComputeMetrics), and persists the
// result -- SPEC.md §2's whole "Historical Data -> Strategy Engine -> Trade
// Simulator -> Metrics Engine" flow in one synchronous call (§ Non-goals: no
// async job queue at this data size).
func (s *Service) RunBacktest(ctx context.Context, userID uuid.UUID, req RunBacktestRequest) (BacktestDetail, error) {
	params, err := validateRequest(req)
	if err != nil {
		return BacktestDetail{}, err
	}

	bars, err := s.history.History(ctx, params.Symbol)
	if err != nil {
		return BacktestDetail{}, err
	}

	ranged := sliceRange(bars, params.StartDate, params.EndDate)
	if len(ranged) == 0 {
		return BacktestDetail{}, ErrDateRangeUnavailable
	}
	if params.LongWindow > len(ranged) {
		return BacktestDetail{}, fmt.Errorf("%w: long_window (%d) exceeds the %d bars available in the requested range",
			ErrInvalidRequest, params.LongWindow, len(ranged))
	}

	signals := GenerateSignals(ranged, params.ShortWindow, params.LongWindow)
	result := Simulate(ranged, signals, params.StartingCapital)
	metrics := ComputeMetrics(result, params.StartingCapital)

	saved, err := s.store.SaveBacktest(ctx, Backtest{
		UserID:          userID,
		Symbol:          params.Symbol,
		ShortWindow:     params.ShortWindow,
		LongWindow:      params.LongWindow,
		StartDate:       params.StartDate,
		EndDate:         params.EndDate,
		StartingCapital: params.StartingCapital,
		FinalEquity:     result.FinalEquity,
		Metrics:         metrics,
	}, result.Trades)
	if err != nil {
		return BacktestDetail{}, fmt.Errorf("saving backtest: %w", err)
	}

	return BacktestDetail{Backtest: saved, Trades: result.Trades}, nil
}

func (s *Service) ListBacktests(ctx context.Context, userID uuid.UUID) ([]Backtest, error) {
	return s.store.ListBacktests(ctx, userID)
}

func (s *Service) GetBacktest(ctx context.Context, userID, id uuid.UUID) (BacktestDetail, error) {
	return s.store.GetBacktest(ctx, userID, id)
}

// validateRequest checks everything decidable before any network call
// (SPEC.md §2.8). Symbol availability and date-range coverage are checked
// afterward in RunBacktest, once history has actually been fetched --
// neither is knowable from the request alone.
func validateRequest(req RunBacktestRequest) (StrategyParams, error) {
	symbol := strings.ToUpper(strings.TrimSpace(req.Symbol))
	if symbol == "" {
		return StrategyParams{}, fmt.Errorf("%w: symbol is required", ErrInvalidRequest)
	}
	if req.ShortWindow < minShortWindow {
		return StrategyParams{}, fmt.Errorf("%w: short_window must be at least %d", ErrInvalidRequest, minShortWindow)
	}
	if req.LongWindow <= req.ShortWindow {
		return StrategyParams{}, fmt.Errorf("%w: long_window must be greater than short_window", ErrInvalidRequest)
	}
	if req.LongWindow > maxLongWindow {
		return StrategyParams{}, fmt.Errorf("%w: long_window must be at most %d", ErrInvalidRequest, maxLongWindow)
	}
	if req.StartingCapital <= 0 {
		return StrategyParams{}, fmt.Errorf("%w: starting_capital must be greater than 0", ErrInvalidRequest)
	}

	start, err := time.Parse(dateLayout, req.StartDate)
	if err != nil {
		return StrategyParams{}, fmt.Errorf("%w: start_date must be YYYY-MM-DD", ErrInvalidRequest)
	}
	end, err := time.Parse(dateLayout, req.EndDate)
	if err != nil {
		return StrategyParams{}, fmt.Errorf("%w: end_date must be YYYY-MM-DD", ErrInvalidRequest)
	}
	if !start.Before(end) {
		return StrategyParams{}, fmt.Errorf("%w: start_date must be before end_date", ErrInvalidRequest)
	}

	return StrategyParams{
		Symbol:          symbol,
		ShortWindow:     req.ShortWindow,
		LongWindow:      req.LongWindow,
		StartDate:       start,
		EndDate:         end,
		StartingCapital: req.StartingCapital,
	}, nil
}

// sliceRange returns the bars whose timestamp falls in [start, end], end
// inclusive through the end of that calendar day -- start_date/end_date are
// dates, not instants, and a caller asking for "through 2026-08-01" expects
// that day's own bar included.
func sliceRange(bars []Bar, start, end time.Time) []Bar {
	endExclusive := end.AddDate(0, 0, 1)
	var out []Bar
	for _, b := range bars {
		if !b.Timestamp.Before(start) && b.Timestamp.Before(endExclusive) {
			out = append(out, b)
		}
	}
	return out
}
