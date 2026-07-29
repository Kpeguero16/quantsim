// Package mock provides in-memory AlpacaClient/HistoricalPriceStore doubles
// for service- and handler-layer tests, so neither needs a live Alpaca
// account or Postgres.
package mock

import (
	"context"
	"time"

	"github.com/kpeguero/quantsim/services/market-data/internal/alpaca"
	"github.com/kpeguero/quantsim/services/market-data/internal/service"
)

// AlpacaClient is a scriptable AlpacaClient double keyed by symbol.
type AlpacaClient struct {
	Bars map[string][]alpaca.Bar
	Errs map[string]error
}

func NewAlpacaClient() *AlpacaClient {
	return &AlpacaClient{
		Bars: make(map[string][]alpaca.Bar),
		Errs: make(map[string]error),
	}
}

func (m *AlpacaClient) GetBars(ctx context.Context, symbol, timeframe string, start, end time.Time) ([]alpaca.Bar, error) {
	if err, ok := m.Errs[symbol]; ok {
		return nil, err
	}
	return m.Bars[symbol], nil
}

// HistoricalPriceStore is an in-memory HistoricalPriceStore double.
type HistoricalPriceStore struct {
	Bars []service.Bar

	// UpsertErr, when set, makes UpsertBars fail -- lets tests verify a store
	// failure surfaces as that symbol's IngestResult.Error rather than being
	// silently dropped.
	UpsertErr error
}

func NewHistoricalPriceStore() *HistoricalPriceStore {
	return &HistoricalPriceStore{}
}

func (m *HistoricalPriceStore) UpsertBars(ctx context.Context, bars []service.Bar) error {
	if m.UpsertErr != nil {
		return m.UpsertErr
	}
	m.Bars = append(m.Bars, bars...)
	return nil
}
