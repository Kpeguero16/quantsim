package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
// async job queue at this data size). Simulate and ComputeMetrics are
// unchanged by Step 18 -- neither knows or cares which strategy produced the
// []Signal it's handed.
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
	if params.Strategy.WarmupBars() > len(ranged) {
		return BacktestDetail{}, fmt.Errorf("%w: this strategy needs %d warm-up bars, more than the %d bars available in the requested range",
			ErrInvalidRequest, params.Strategy.WarmupBars(), len(ranged))
	}

	signals := params.Strategy.GenerateSignals(ranged)
	result := Simulate(ranged, signals, params.StartingCapital)
	metrics := ComputeMetrics(result, params.StartingCapital)

	saved, err := s.store.SaveBacktest(ctx, Backtest{
		UserID:          userID,
		Symbol:          params.Symbol,
		Strategy:        params.Strategy.Kind(),
		Params:          params.Strategy.Params(),
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
// (SPEC.md §2.8). Strategy construction and its own parameter bounds are
// delegated to NewStrategy (Step 18 SPEC.md §2.1) -- the single place an
// unknown kind, a malformed params object, or an out-of-bounds parameter
// surface as ErrInvalidRequest, rather than validateRequest re-deriving
// per-strategy checks it has no business knowing about. Symbol availability
// and date-range coverage are checked afterward in RunBacktest, once
// history has actually been fetched -- neither is knowable from the request
// alone.
func validateRequest(req RunBacktestRequest) (StrategyParams, error) {
	symbol := strings.ToUpper(strings.TrimSpace(req.Symbol))
	if symbol == "" {
		return StrategyParams{}, fmt.Errorf("%w: symbol is required", ErrInvalidRequest)
	}

	strategy, err := NewStrategy(req.Strategy, req.Params)
	if err != nil {
		return StrategyParams{}, err
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
		Strategy:        strategy,
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
