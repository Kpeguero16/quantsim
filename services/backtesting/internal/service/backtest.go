package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

// dateLayout is the wire format for start_date/end_date -- calendar dates,
// not timestamps, since a backtest range has no meaningful time-of-day.
const dateLayout = "2006-01-02"

// maxSymbols caps one run's symbol list (SPEC.md Step 19 §2.4). The bound is
// about the fan-out this endpoint performs synchronously -- N history fetches
// and an N-way intersection per request -- not about anything the simulator
// cannot express.
const maxSymbols = 10

type Service struct {
	store   BacktestStore
	history HistoryClient
}

func NewService(store BacktestStore, history HistoryClient) *Service {
	return &Service{store: store, history: history}
}

// RunBacktest validates the request, fetches every symbol's history, runs the
// strategy pipeline (alignBars -> GenerateSignals -> SimulatePortfolio ->
// ComputeMetrics), and persists the result -- SPEC.md §2's whole "Historical
// Data -> Strategy Engine -> Trade Simulator -> Metrics Engine" flow in one
// synchronous call (§ Non-goals: no async job queue at this data size).
//
// One run, one shared pool of capital, one set of metrics over the combined
// equity curve (Step 19 SPEC.md §2.2). A single-symbol request is not a
// second path through here: it is the len(symbols)==1 case of this one, which
// is why nothing below branches on N. ComputeMetrics is unchanged and does
// not know how many symbols produced the curve it is handed.
func (s *Service) RunBacktest(ctx context.Context, userID uuid.UUID, req RunBacktestRequest) (BacktestDetail, error) {
	params, err := validateRequest(req)
	if err != nil {
		return BacktestDetail{}, err
	}

	fetched, err := s.fetchHistories(ctx, params.Symbols)
	if err != nil {
		return BacktestDetail{}, err
	}

	// One timeline for the whole run: the dates every symbol has (§2.1). The
	// range check that used to be per-symbol is now this one check on the
	// intersection -- a symbol whose own history covers the range but shares
	// no dates with the others is just as unrunnable as one with no history
	// in it at all.
	symbols, aligned := alignBars(fetched, params.StartDate, params.EndDate)
	if len(aligned) == 0 || len(aligned[0]) == 0 {
		return BacktestDetail{}, ErrDateRangeUnavailable
	}

	// Warm-up is checked once, against the ALIGNED length: every symbol runs
	// the same strategy over the same number of bars, so there is exactly one
	// answer to "are there enough bars" for the whole run.
	if params.Strategy.WarmupBars() > len(aligned[0]) {
		return BacktestDetail{}, fmt.Errorf("%w: this strategy needs %d warm-up bars, more than the %d bars available in the requested range",
			ErrInvalidRequest, params.Strategy.WarmupBars(), len(aligned[0]))
	}

	signals := make([][]Signal, len(symbols))
	for i, bars := range aligned {
		signals[i] = params.Strategy.GenerateSignals(bars)
	}
	result := SimulatePortfolio(symbols, aligned, signals, params.StartingCapital)
	metrics := ComputeMetrics(result, params.StartingCapital)

	saved, err := s.store.SaveBacktest(ctx, Backtest{
		UserID:          userID,
		Symbols:         params.Symbols,
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

// fetchHistories fetches every symbol's history concurrently (SPEC.md Step 19
// §2.6) and fails the whole run if any one of them fails -- never a silent
// N-1. N is bounded at 10 by validateRequest, so the fan-out is bounded too.
//
// Which failure is reported is deliberate: symbols arrives sorted, and errs is
// scanned in that order, so two unavailable symbols always name the same one.
// errgroup.Wait's own error is whichever goroutine lost the race, which would
// make that answer depend on scheduling. This is a zero-value errgroup.Group
// and NOT WithContext: a failing fetch must not cancel its siblings, because a
// sibling canceled mid-flight would report a context error in place of the
// real one it was about to return, reintroducing exactly the nondeterminism
// the ordered scan exists to remove.
//
// Errors pass through untouched: market_data_client.go already wraps
// ErrSymbolUnavailable with the symbol's name, so re-wrapping here would
// double it.
func (s *Service) fetchHistories(ctx context.Context, symbols []string) (map[string][]Bar, error) {
	bars := make([][]Bar, len(symbols))
	errs := make([]error, len(symbols))

	var g errgroup.Group
	for i, symbol := range symbols {
		g.Go(func() error {
			var err error
			bars[i], err = s.history.History(ctx, symbol)
			errs[i] = err
			return err
		})
	}
	_ = g.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	out := make(map[string][]Bar, len(symbols))
	for i, symbol := range symbols {
		out[symbol] = bars[i]
	}
	return out, nil
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
	symbols, err := normalizeSymbols(req.Symbols)
	if err != nil {
		return StrategyParams{}, err
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
		Symbols:         symbols,
		Strategy:        strategy,
		StartDate:       start,
		EndDate:         end,
		StartingCapital: req.StartingCapital,
	}, nil
}

// normalizeSymbols turns a client's raw list into the canonical one the rest
// of the run uses: trimmed, uppercased, sorted, and proven duplicate-free
// (SPEC.md Step 19 §2.4, plan D4). Sorting here rather than at the point of
// use is what makes "the same symbol set in a different order produces an
// identical run" true of the persisted row as well as the trade log.
//
// A duplicate is rejected outright rather than silently collapsed: "AAPL,
// aapl" is a typo in a hand-typed list, and quietly running a 1-symbol
// backtest for someone who asked for 2 is a worse answer than saying no.
// Case-insensitivity falls out of comparing after uppercasing.
func normalizeSymbols(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: symbols is required", ErrInvalidRequest)
	}
	if len(raw) > maxSymbols {
		return nil, fmt.Errorf("%w: at most %d symbols per backtest, got %d", ErrInvalidRequest, maxSymbols, len(raw))
	}

	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, s := range raw {
		symbol := strings.ToUpper(strings.TrimSpace(s))
		if symbol == "" {
			return nil, fmt.Errorf("%w: symbols must not contain an empty entry", ErrInvalidRequest)
		}
		if _, dup := seen[symbol]; dup {
			return nil, fmt.Errorf("%w: duplicate symbol %s", ErrInvalidRequest, symbol)
		}
		seen[symbol] = struct{}{}
		out = append(out, symbol)
	}
	sort.Strings(out)
	return out, nil
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
