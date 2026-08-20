// Package mock provides in-memory doubles for the backtesting service's two
// dependencies, so the service and handler suites need neither Postgres nor
// a running market-data.
package mock

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/backtesting/internal/service"
)

var (
	_ service.HistoryClient = (*HistoryClient)(nil)
	_ service.BacktestStore = (*BacktestStore)(nil)
)

// HistoryClient answers per symbol. A portfolio run's error cases are all
// per-symbol ones -- "AAPL has bars, MSFT does not", "these two trade on
// different calendars" -- and a single shared Bars/Err field cannot express
// either (Step 19 plan D3).
//
// Bars/Err are consulted symbol-first, falling back to the symbol-less
// defaults, so a single-symbol test still reads as one assignment and does
// not have to name a symbol it does not care about.
type HistoryClient struct {
	Bars []service.Bar
	Err  error

	BarsBySymbol map[string][]service.Bar
	ErrBySymbol  map[string]error

	// mu guards Calls: RunBacktest fetches N symbols concurrently (SPEC.md
	// Step 19 §2.6), so this append runs on N goroutines at once.
	mu sync.Mutex

	// calls records every symbol History was asked for, so a test can assert
	// the service never fetched history for a request it should have
	// rejected during validation first. Read it through Calls() -- an
	// exported slice field could not be read safely while a run is in
	// flight.
	calls []string
}

func (c *HistoryClient) History(_ context.Context, symbol string) ([]service.Bar, error) {
	c.mu.Lock()
	c.calls = append(c.calls, symbol)
	c.mu.Unlock()

	if err, ok := c.ErrBySymbol[symbol]; ok {
		return nil, err
	}
	if bars, ok := c.BarsBySymbol[symbol]; ok {
		return bars, nil
	}
	if c.Err != nil {
		return nil, c.Err
	}
	return c.Bars, nil
}

// Calls returns the symbols History was asked for, in call order. The order
// across concurrently fetched symbols is not deterministic -- assert on the
// set, or sort first.
func (c *HistoryClient) Calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

type BacktestStore struct {
	SaveErr  error
	ListErr  error
	GetErr   error
	GetValue service.BacktestDetail

	Saved       []service.Backtest
	SavedTrades [][]service.TradeRecord
}

func (s *BacktestStore) SaveBacktest(_ context.Context, b service.Backtest, trades []service.TradeRecord) (service.Backtest, error) {
	if s.SaveErr != nil {
		return service.Backtest{}, s.SaveErr
	}
	b.ID = uuid.New()
	s.Saved = append(s.Saved, b)
	s.SavedTrades = append(s.SavedTrades, trades)
	return b, nil
}

func (s *BacktestStore) ListBacktests(_ context.Context, _ uuid.UUID) ([]service.Backtest, error) {
	if s.ListErr != nil {
		return nil, s.ListErr
	}
	return s.Saved, nil
}

func (s *BacktestStore) GetBacktest(_ context.Context, _, _ uuid.UUID) (service.BacktestDetail, error) {
	if s.GetErr != nil {
		return service.BacktestDetail{}, s.GetErr
	}
	return s.GetValue, nil
}
