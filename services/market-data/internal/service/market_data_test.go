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
	svc := service.NewService(alpacaMock, storeMock)

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
	svc := service.NewService(alpacaMock, storeMock)

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
	svc := service.NewService(alpacaMock, storeMock)

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
	svc := service.NewService(alpacaMock, storeMock)

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
	svc := service.NewService(alpacaMock, storeMock)

	_, err := svc.Ingest(context.Background(), service.IngestRequest{Symbols: []string{"AAPL"}})
	if !errors.Is(err, service.ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable, got %v", err)
	}
}

func TestIngest_InvalidSymbolFormat(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	storeMock := mock.NewHistoricalPriceStore()
	svc := service.NewService(alpacaMock, storeMock)

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
	svc := service.NewService(alpacaMock, storeMock)

	_, err := svc.Ingest(context.Background(), service.IngestRequest{Symbols: []string{"AAPL"}, Start: "not-a-date"})
	if !errors.Is(err, service.ErrInvalidDateRange) {
		t.Fatalf("expected ErrInvalidDateRange, got %v", err)
	}
}
