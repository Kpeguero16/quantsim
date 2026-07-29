// Package mock provides in-memory AlpacaClient/HistoricalPriceStore doubles
// for service- and handler-layer tests, so neither needs a live Alpaca
// account or Postgres.
package mock

import (
	"context"
	"errors"
	"time"

	"github.com/kpeguero/quantsim/services/market-data/internal/alpaca"
	"github.com/kpeguero/quantsim/services/market-data/internal/service"
)

// AlpacaClient is a scriptable AlpacaClient double keyed by symbol.
type AlpacaClient struct {
	Bars map[string][]alpaca.Bar
	Errs map[string]error

	// LastStart/LastEnd record the date range passed to the most recent
	// GetBars call, so tests can verify the service resolved/parsed a
	// requested date range correctly before reaching the client.
	LastStart time.Time
	LastEnd   time.Time
}

func NewAlpacaClient() *AlpacaClient {
	return &AlpacaClient{
		Bars: make(map[string][]alpaca.Bar),
		Errs: make(map[string]error),
	}
}

func (m *AlpacaClient) GetBars(ctx context.Context, symbol, timeframe string, start, end time.Time) ([]alpaca.Bar, error) {
	m.LastStart = start
	m.LastEnd = end
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

	// GetHistoryErr, when set, makes GetHistory fail.
	GetHistoryErr error

	// LastLimit records the limit passed to the most recent GetHistory call,
	// so tests can verify the service clamps/defaults it before reaching the
	// store.
	LastLimit int
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

// GetHistory assumes Bars is already populated in ascending timestamp order
// (as tests set it up) and returns the most recent `limit` entries by taking
// the tail of the slice -- mirroring the real store's "ORDER BY timestamp
// DESC LIMIT n, then reverse" behavior without needing SQL.
func (m *HistoricalPriceStore) GetHistory(ctx context.Context, symbol, timeframe string, limit int) ([]service.Bar, error) {
	m.LastLimit = limit
	if m.GetHistoryErr != nil {
		return nil, m.GetHistoryErr
	}

	var matched []service.Bar
	for _, b := range m.Bars {
		if b.Symbol == symbol && b.Timeframe == timeframe {
			matched = append(matched, b)
		}
	}
	if limit > 0 && len(matched) > limit {
		matched = matched[len(matched)-limit:]
	}
	return matched, nil
}

// ErrCacheMiss is PriceCache's not-found sentinel, mirroring cache.ErrNotFound
// without importing the cache package (mocks stay dependency-free of the real
// implementation).
var ErrCacheMiss = errors.New("mock: price not cached")

// PriceCache is an in-memory PriceCache double keyed by symbol.
type PriceCache struct {
	Prices map[string]service.Price

	// GetErr, when set, makes GetPrice fail with this error instead of the
	// usual ErrCacheMiss-on-miss behavior.
	GetErr error
	// SetErr/PublishErr, when set, make the respective method fail.
	SetErr     error
	PublishErr error

	// Published records every symbol/price pair passed to PublishPrice, in
	// call order, so tests can verify what was published.
	Published []service.Price
}

func NewPriceCache() *PriceCache {
	return &PriceCache{Prices: make(map[string]service.Price)}
}

func (m *PriceCache) SetPrice(ctx context.Context, symbol string, price service.Price, ttl time.Duration) error {
	if m.SetErr != nil {
		return m.SetErr
	}
	m.Prices[symbol] = price
	return nil
}

func (m *PriceCache) GetPrice(ctx context.Context, symbol string) (service.Price, error) {
	if m.GetErr != nil {
		return service.Price{}, m.GetErr
	}
	price, ok := m.Prices[symbol]
	if !ok {
		return service.Price{}, ErrCacheMiss
	}
	return price, nil
}

func (m *PriceCache) PublishPrice(ctx context.Context, symbol string, price service.Price) error {
	if m.PublishErr != nil {
		return m.PublishErr
	}
	m.Published = append(m.Published, price)
	return nil
}
