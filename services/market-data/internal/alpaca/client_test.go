package alpaca

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return &Client{
		baseURL:    srv.URL,
		apiKey:     "test-key",
		apiSecret:  "test-secret",
		httpClient: srv.Client(),
	}
}

func TestGetBars_Success(t *testing.T) {
	var gotHeaders http.Header
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"symbol": "AAPL",
			"bars": [
				{"t":"2024-01-02T05:00:00Z","o":100.1,"h":101.2,"l":99.5,"c":100.9,"v":1000},
				{"t":"2024-01-03T05:00:00Z","o":100.9,"h":102.0,"l":100.0,"c":101.5,"v":1200}
			],
			"next_page_token": null
		}`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)

	bars, err := c.GetBars(context.Background(), "AAPL", "1Day", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bars) != 2 {
		t.Fatalf("expected 2 bars, got %d", len(bars))
	}
	if bars[0].Close != 100.9 || bars[1].Close != 101.5 {
		t.Errorf("unexpected bar values: %+v", bars)
	}
	wantTS := time.Date(2024, 1, 2, 5, 0, 0, 0, time.UTC)
	if !bars[0].Timestamp.Equal(wantTS) {
		t.Errorf("timestamp = %v, want %v", bars[0].Timestamp, wantTS)
	}

	if gotHeaders.Get("APCA-API-KEY-ID") != "test-key" {
		t.Errorf("APCA-API-KEY-ID = %q, want test-key", gotHeaders.Get("APCA-API-KEY-ID"))
	}
	if gotHeaders.Get("APCA-API-SECRET-KEY") != "test-secret" {
		t.Errorf("APCA-API-SECRET-KEY = %q, want test-secret", gotHeaders.Get("APCA-API-SECRET-KEY"))
	}
	if gotQuery.Get("feed") != "iex" {
		t.Errorf("feed = %q, want iex", gotQuery.Get("feed"))
	}
	if gotQuery.Get("timeframe") != "1Day" {
		t.Errorf("timeframe = %q, want 1Day", gotQuery.Get("timeframe"))
	}
}

func TestGetBars_Pagination(t *testing.T) {
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page_token") == "" {
			_, _ = w.Write([]byte(`{
				"symbol": "AAPL",
				"bars": [{"t":"2024-01-02T05:00:00Z","o":1,"h":2,"l":0.5,"c":1.5,"v":10}],
				"next_page_token": "page2"
			}`))
			return
		}
		if r.URL.Query().Get("page_token") != "page2" {
			t.Errorf("unexpected page_token: %s", r.URL.Query().Get("page_token"))
		}
		_, _ = w.Write([]byte(`{
			"symbol": "AAPL",
			"bars": [{"t":"2024-01-03T05:00:00Z","o":1.5,"h":2.5,"l":1,"c":2,"v":20}],
			"next_page_token": null
		}`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)

	bars, err := c.GetBars(context.Background(), "AAPL", "1Day", start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 requests (pagination), got %d", calls)
	}
	if len(bars) != 2 {
		t.Fatalf("expected 2 bars across both pages, got %d", len(bars))
	}
	if bars[0].Volume != 10 || bars[1].Volume != 20 {
		t.Errorf("unexpected bar order/values: %+v", bars)
	}
}

func TestGetBars_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":40110000,"message":"access forbidden"}`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)

	bars, err := c.GetBars(context.Background(), "AAPL", "1Day", start, end)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if bars != nil {
		t.Errorf("expected nil bars on error, got %+v", bars)
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected *StatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusForbidden)
	}
}

func TestGetBars_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)

	_, err := c.GetBars(context.Background(), "AAPL", "1Day", start, end)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestGetBars_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	badURL := srv.URL
	srv.Close() // server is down before the client ever calls it

	c := &Client{
		baseURL:    badURL,
		apiKey:     "k",
		apiSecret:  "s",
		httpClient: http.DefaultClient,
	}

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)

	_, err := c.GetBars(context.Background(), "AAPL", "1Day", start, end)
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
}
