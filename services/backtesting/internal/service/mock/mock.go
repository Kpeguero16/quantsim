// Package mock provides in-memory doubles for the backtesting service's two
// dependencies, so the service and handler suites need neither Postgres nor
// a running market-data.
package mock

import (
	"context"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/backtesting/internal/service"
)

var (
	_ service.HistoryClient = (*HistoryClient)(nil)
	_ service.BacktestStore = (*BacktestStore)(nil)
)

type HistoryClient struct {
	Bars []service.Bar
	Err  error

	// Calls records every symbol History was asked for, so a test can assert
	// the service never fetched history for a request it should have
	// rejected during validation first.
	Calls []string
}

func (c *HistoryClient) History(_ context.Context, symbol string) ([]service.Bar, error) {
	c.Calls = append(c.Calls, symbol)
	if c.Err != nil {
		return nil, c.Err
	}
	return c.Bars, nil
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
