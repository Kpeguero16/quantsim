package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/market-data/internal/alpaca"
	"github.com/kpeguero/quantsim/services/market-data/internal/service"
	"github.com/kpeguero/quantsim/services/market-data/internal/service/mock"
)

func TestIngest_SingleSymbolSuccess(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	alpacaMock.Bars["AAPL"] = []alpaca.Bar{
		{Timestamp: time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC), Open: 100, High: 101, Low: 99, Close: 100.5, Volume: 1000},
	}
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(alpacaMock, storeMock, mock.NewPriceCache())

	results, err := svc.Ingest(context.Background(), service.IngestRequest{Symbols: []string{"aapl"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Symbol != "AAPL" || results[0].BarsIngested != 1 || results[0].Error != "" {
		t.Fatalf("unexpected result: %+v", results[0])
	}
	if len(storeMock.Bars) != 1 {
		t.Fatalf("expected 1 bar stored, got %d", len(storeMock.Bars))
	}
	if storeMock.Bars[0].Symbol != "AAPL" || storeMock.Bars[0].Timeframe != service.Timeframe {
		t.Errorf("stored bar has wrong symbol/timeframe: %+v", storeMock.Bars[0])
	}
}

func TestIngest_MultiSymbolSuccess(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	alpacaMock.Bars["AAPL"] = []alpaca.Bar{{Timestamp: time.Now(), Close: 1}}
	alpacaMock.Bars["MSFT"] = []alpaca.Bar{{Timestamp: time.Now(), Close: 2}, {Timestamp: time.Now(), Close: 3}}
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(alpacaMock, storeMock, mock.NewPriceCache())

	results, err := svc.Ingest(context.Background(), service.IngestRequest{Symbols: []string{"AAPL", "MSFT"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if len(storeMock.Bars) != 3 {
		t.Fatalf("expected 3 bars stored total, got %d", len(storeMock.Bars))
	}
}

func TestIngest_DefaultsToWatchlistWhenEmpty(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(alpacaMock, storeMock, mock.NewPriceCache())

	results, err := svc.Ingest(context.Background(), service.IngestRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != len(service.DefaultWatchlist) {
		t.Fatalf("expected %d results (watchlist default), got %d", len(service.DefaultWatchlist), len(results))
	}
}

func TestIngest_PartialFailureOneBadSymbol(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	alpacaMock.Bars["AAPL"] = []alpaca.Bar{{Timestamp: time.Now(), Close: 1}}
	alpacaMock.Errs["MSFT"] = &alpaca.StatusError{StatusCode: 422, Body: "unknown symbol"}
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(alpacaMock, storeMock, mock.NewPriceCache())

	results, err := svc.Ingest(context.Background(), service.IngestRequest{Symbols: []string{"AAPL", "MSFT"}})
	if err != nil {
		t.Fatalf("expected no top-level error on partial failure, got %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	var aaplResult, msftResult service.IngestResult
	for _, r := range results {
		switch r.Symbol {
		case "AAPL":
			aaplResult = r
		case "MSFT":
			msftResult = r
		}
	}
	if aaplResult.Error != "" || aaplResult.BarsIngested != 1 {
		t.Errorf("AAPL should have succeeded: %+v", aaplResult)
	}
	if msftResult.Error == "" {
		t.Errorf("MSFT should carry an error message: %+v", msftResult)
	}
}

func TestIngest_AllSymbolsUpstreamFailure(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	alpacaMock.Errs["AAPL"] = errors.New("connection refused")
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(alpacaMock, storeMock, mock.NewPriceCache())

	_, err := svc.Ingest(context.Background(), service.IngestRequest{Symbols: []string{"AAPL"}})
	if !errors.Is(err, service.ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable, got %v", err)
	}
}

func TestIngest_InvalidSymbolFormat(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(alpacaMock, storeMock, mock.NewPriceCache())

	results, err := svc.Ingest(context.Background(), service.IngestRequest{Symbols: []string{"not a symbol!"}})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(results) != 1 || results[0].Error == "" {
		t.Fatalf("expected a per-symbol error for invalid format, got %+v", results)
	}
	if len(storeMock.Bars) != 0 {
		t.Errorf("expected no bars stored for invalid symbol, got %d", len(storeMock.Bars))
	}
}

func TestIngest_InvalidDateRange(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(alpacaMock, storeMock, mock.NewPriceCache())

	_, err := svc.Ingest(context.Background(), service.IngestRequest{Symbols: []string{"AAPL"}, Start: "not-a-date"})
	if !errors.Is(err, service.ErrInvalidDateRange) {
		t.Fatalf("expected ErrInvalidDateRange, got %v", err)
	}
}

func TestHistory_Found(t *testing.T) {
	storeMock := mock.NewHistoricalPriceStore()
	storeMock.Bars = []service.Bar{
		{Symbol: "AAPL", Timeframe: service.Timeframe, Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Close: 1},
		{Symbol: "AAPL", Timeframe: service.Timeframe, Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Close: 2},
	}
	svc := service.NewService(mock.NewAlpacaClient(), storeMock, mock.NewPriceCache())

	resp, err := svc.History(context.Background(), "aapl", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Symbol != "AAPL" || resp.Timeframe != service.Timeframe {
		t.Fatalf("unexpected response header: %+v", resp)
	}
	if len(resp.Bars) != 2 {
		t.Fatalf("expected 2 bars, got %d", len(resp.Bars))
	}
}

func TestHistory_UnfetchedSymbolReturnsEmptyNotError(t *testing.T) {
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(mock.NewAlpacaClient(), storeMock, mock.NewPriceCache())

	resp, err := svc.History(context.Background(), "ZZZZ", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Bars == nil {
		t.Fatal("expected a non-nil empty slice, got nil")
	}
	if len(resp.Bars) != 0 {
		t.Fatalf("expected 0 bars, got %d", len(resp.Bars))
	}
}

func TestHistory_DefaultLimitWhenZero(t *testing.T) {
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(mock.NewAlpacaClient(), storeMock, mock.NewPriceCache())

	if _, err := svc.History(context.Background(), "AAPL", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if storeMock.LastLimit != service.DefaultHistoryLimit {
		t.Errorf("expected default limit %d, got %d", service.DefaultHistoryLimit, storeMock.LastLimit)
	}
}

func TestHistory_LimitClampedToMax(t *testing.T) {
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(mock.NewAlpacaClient(), storeMock, mock.NewPriceCache())

	if _, err := svc.History(context.Background(), "AAPL", 999999); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if storeMock.LastLimit != service.MaxHistoryLimit {
		t.Errorf("expected limit clamped to %d, got %d", service.MaxHistoryLimit, storeMock.LastLimit)
	}
}

func TestIngest_StoreFailure(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	alpacaMock.Bars["AAPL"] = []alpaca.Bar{{Timestamp: time.Now(), Close: 1}}
	storeMock := mock.NewHistoricalPriceStore()
	storeMock.UpsertErr = errors.New("connection reset")
	svc := service.NewService(alpacaMock, storeMock, mock.NewPriceCache())

	results, err := svc.Ingest(context.Background(), service.IngestRequest{Symbols: []string{"AAPL"}})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(results) != 1 || results[0].Error == "" || results[0].BarsIngested != 0 {
		t.Fatalf("expected a per-symbol store-failure error, got %+v", results)
	}
}

func TestHistory_StoreError(t *testing.T) {
	storeMock := mock.NewHistoricalPriceStore()
	storeMock.GetHistoryErr = errors.New("connection reset")
	svc := service.NewService(mock.NewAlpacaClient(), storeMock, mock.NewPriceCache())

	_, err := svc.History(context.Background(), "AAPL", 0)
	if !errors.Is(err, storeMock.GetHistoryErr) {
		t.Fatalf("expected store error to propagate, got %v", err)
	}
}

func TestLatestPrice_Found(t *testing.T) {
	cacheMock := mock.NewPriceCache()
	cacheMock.Prices["AAPL"] = service.Price{Symbol: "AAPL", Price: 123.45, Timestamp: time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)}
	svc := service.NewService(mock.NewAlpacaClient(), mock.NewHistoricalPriceStore(), cacheMock)

	price, err := svc.LatestPrice(context.Background(), "aapl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if price.Symbol != "AAPL" || price.Price != 123.45 {
		t.Fatalf("unexpected price: %+v", price)
	}
}

func TestLatestPrice_NotCached(t *testing.T) {
	svc := service.NewService(mock.NewAlpacaClient(), mock.NewHistoricalPriceStore(), mock.NewPriceCache())

	_, err := svc.LatestPrice(context.Background(), "ZZZZ")
	if !errors.Is(err, service.ErrPriceNotCached) {
		t.Fatalf("expected ErrPriceNotCached, got %v", err)
	}
}

func TestIngest_ValidYYYYMMDDDateHonored(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(alpacaMock, storeMock, mock.NewPriceCache())

	_, err := svc.Ingest(context.Background(), service.IngestRequest{
		Symbols: []string{"AAPL"},
		Start:   "2020-01-01",
		End:     "2020-06-01",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantStart := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC)
	if !alpacaMock.LastStart.Equal(wantStart) {
		t.Errorf("start = %v, want %v", alpacaMock.LastStart, wantStart)
	}
	if !alpacaMock.LastEnd.Equal(wantEnd) {
		t.Errorf("end = %v, want %v", alpacaMock.LastEnd, wantEnd)
	}
}
