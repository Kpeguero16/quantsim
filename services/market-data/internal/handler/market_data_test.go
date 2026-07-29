package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/market-data/internal/alpaca"
	"github.com/kpeguero/quantsim/services/market-data/internal/service"
	"github.com/kpeguero/quantsim/services/market-data/internal/service/mock"
)

func newTestRouter(alpacaMock *mock.AlpacaClient, storeMock *mock.HistoricalPriceStore) http.Handler {
	svc := service.NewService(alpacaMock, storeMock)
	return NewRouter(NewMarketDataHandler(svc))
}

func doRequest(t *testing.T, r http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	r := newTestRouter(mock.NewAlpacaClient(), mock.NewHistoricalPriceStore())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSymbolsHandler(t *testing.T) {
	r := newTestRouter(mock.NewAlpacaClient(), mock.NewHistoricalPriceStore())

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body SymbolsResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	want := []string{"AAPL", "MSFT", "GOOGL", "AMZN", "TSLA", "SPY", "QQQ"}
	if len(body.Symbols) != len(want) {
		t.Fatalf("expected %d symbols, got %d: %v", len(want), len(body.Symbols), body.Symbols)
	}
	for i, s := range want {
		if body.Symbols[i] != s {
			t.Errorf("symbol[%d] = %q, want %q", i, body.Symbols[i], s)
		}
	}
}

func TestIngestHandler_Success(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	alpacaMock.Bars["AAPL"] = []alpaca.Bar{{Close: 100}}
	r := newTestRouter(alpacaMock, mock.NewHistoricalPriceStore())

	body, _ := json.Marshal(map[string]any{"symbols": []string{"AAPL"}})
	rec := doRequest(t, r, http.MethodPost, "/market-data/ingest", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp IngestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Symbol != "AAPL" || resp.Results[0].BarsIngested != 1 {
		t.Fatalf("unexpected results: %+v", resp.Results)
	}
}

func TestIngestHandler_DefaultsWhenBodyEmpty(t *testing.T) {
	r := newTestRouter(mock.NewAlpacaClient(), mock.NewHistoricalPriceStore())

	rec := doRequest(t, r, http.MethodPost, "/market-data/ingest", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp IngestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Results) != len(service.DefaultWatchlist) {
		t.Fatalf("expected watchlist default (%d results), got %d", len(service.DefaultWatchlist), len(resp.Results))
	}
}

func TestIngestHandler_MalformedJSON(t *testing.T) {
	r := newTestRouter(mock.NewAlpacaClient(), mock.NewHistoricalPriceStore())

	rec := doRequest(t, r, http.MethodPost, "/market-data/ingest", []byte("not-json"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestIngestHandler_UpstreamUnavailable(t *testing.T) {
	alpacaMock := mock.NewAlpacaClient()
	alpacaMock.Errs["AAPL"] = &alpaca.StatusError{StatusCode: 500, Body: "boom"}
	r := newTestRouter(alpacaMock, mock.NewHistoricalPriceStore())

	body, _ := json.Marshal(map[string]any{"symbols": []string{"AAPL"}})
	rec := doRequest(t, r, http.MethodPost, "/market-data/ingest", body)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestIngestHandler_InvalidDateRange(t *testing.T) {
	r := newTestRouter(mock.NewAlpacaClient(), mock.NewHistoricalPriceStore())

	body, _ := json.Marshal(map[string]any{"symbols": []string{"AAPL"}, "start": "not-a-date"})
	rec := doRequest(t, r, http.MethodPost, "/market-data/ingest", body)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHistoryHandler_Found(t *testing.T) {
	storeMock := mock.NewHistoricalPriceStore()
	storeMock.Bars = []service.Bar{
		{Symbol: "AAPL", Timeframe: service.Timeframe, Timestamp: time.Now(), Close: 100},
	}
	r := newTestRouter(mock.NewAlpacaClient(), storeMock)

	req := httptest.NewRequest(http.MethodGet, "/market-data/history/AAPL", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp service.HistoryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Symbol != "AAPL" || len(resp.Bars) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHistoryHandler_UnfetchedSymbolReturns200Empty(t *testing.T) {
	r := newTestRouter(mock.NewAlpacaClient(), mock.NewHistoricalPriceStore())

	req := httptest.NewRequest(http.MethodGet, "/market-data/history/ZZZZ", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp service.HistoryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Bars) != 0 {
		t.Fatalf("expected 0 bars for unfetched symbol, got %d", len(resp.Bars))
	}
}

func TestHistoryHandler_LimitQueryParamRespected(t *testing.T) {
	storeMock := mock.NewHistoricalPriceStore()
	r := newTestRouter(mock.NewAlpacaClient(), storeMock)

	req := httptest.NewRequest(http.MethodGet, "/market-data/history/AAPL?limit=50", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if storeMock.LastLimit != 50 {
		t.Errorf("expected limit 50 passed to store, got %d", storeMock.LastLimit)
	}
}

func TestHistoryHandler_InvalidLimit(t *testing.T) {
	r := newTestRouter(mock.NewAlpacaClient(), mock.NewHistoricalPriceStore())

	req := httptest.NewRequest(http.MethodGet, "/market-data/history/AAPL?limit=notanumber", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
