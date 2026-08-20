package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/client"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// serve stands up a real HTTP server so the clients are exercised through an
// actual request/response cycle -- status codes, headers and JSON decoding
// included -- rather than against a hand-built http.Response.
func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func json200(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// --- TradingClient ---

// SPEC §6.5's whole point: /trading/* is authenticated at trading-engine, so a
// client that drops the caller's token works against a mock and 401s against
// the real service. Asserted on the wire, not on the mock.
func TestTradingClient_ForwardsTheCallersAuthorizationHeader(t *testing.T) {
	var seen string
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		json200(`{"trades":[]}`)(w, r)
	})

	if _, err := client.NewTradingClient(url).Trades(context.Background(), "Bearer abc.def.ghi"); err != nil {
		t.Fatalf("Trades: %v", err)
	}
	if seen != "Bearer abc.def.ghi" {
		t.Errorf("trading-engine saw Authorization %q, want the caller's token forwarded verbatim", seen)
	}
}

func TestTradingClient_PortfolioAlsoForwardsTheToken(t *testing.T) {
	var seen string
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		json200(`{"balance":90000,"positions":[{"symbol":"AAPL","quantity":100}]}`)(w, r)
	})

	got, err := client.NewTradingClient(url).Portfolio(context.Background(), "Bearer tok")
	if err != nil {
		t.Fatalf("Portfolio: %v", err)
	}
	if seen != "Bearer tok" {
		t.Errorf("Authorization = %q, want it forwarded", seen)
	}
	if got.Balance != 90000 || len(got.Positions) != 1 || got.Positions[0].Quantity != 100 {
		t.Errorf("decoded portfolio is wrong: %+v", got)
	}
}

func TestTradingClient_DecodesTradesIncludingNullRealizedPL(t *testing.T) {
	url := serve(t, json200(`{"trades":[
		{"id":"t1","symbol":"AAPL","side":"buy","quantity":10,"price":150,"realized_pl":null,"executed_at":"2026-03-01T15:00:00Z"},
		{"id":"t2","symbol":"AAPL","side":"sell","quantity":10,"price":160,"realized_pl":100,"executed_at":"2026-03-02T15:00:00Z"}
	]}`))

	got, err := client.NewTradingClient(url).Trades(context.Background(), "Bearer tok")
	if err != nil {
		t.Fatalf("Trades: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d trades, want 2", len(got))
	}
	// The buy's absent P/L must stay absent -- decoding it as 0 would report a
	// break-even round trip that never happened.
	if got[0].RealizedPL != nil {
		t.Errorf("buy's realized_pl = %v, want nil", *got[0].RealizedPL)
	}
	if got[1].RealizedPL == nil || *got[1].RealizedPL != 100 {
		t.Errorf("sell's realized_pl = %v, want 100", got[1].RealizedPL)
	}
	if got[0].Side != service.SideBuy || got[1].Side != service.SideSell {
		t.Errorf("sides decoded wrong: %v %v", got[0].Side, got[1].Side)
	}
}

func TestTradingClient_EmptyTradesIsNonNil(t *testing.T) {
	url := serve(t, json200(`{"trades":[]}`))

	got, err := client.NewTradingClient(url).Trades(context.Background(), "Bearer tok")
	if err != nil {
		t.Fatalf("Trades: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want an empty non-nil slice")
	}
}

// A refused credential is not an outage and must not be reported as one: 401
// maps to ErrUnauthorized (a 401 to our own caller), everything else non-200
// to ErrUpstreamUnavailable (a 502).
func TestTradingClient_StatusMapping(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, service.ErrUnauthorized},
		{http.StatusForbidden, service.ErrUnauthorized},
		{http.StatusInternalServerError, service.ErrUpstreamUnavailable},
		{http.StatusBadGateway, service.ErrUpstreamUnavailable},
		{http.StatusNotFound, service.ErrUpstreamUnavailable},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			url := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			})

			_, err := client.NewTradingClient(url).Trades(context.Background(), "Bearer tok")
			if !errors.Is(err, tt.want) {
				t.Errorf("status %d gave %v, want %v", tt.status, err, tt.want)
			}
		})
	}
}

func TestTradingClient_UnreachableIsUpstreamUnavailable(t *testing.T) {
	// A port nothing is listening on: the connection is refused outright.
	_, err := client.NewTradingClient("http://127.0.0.1:1").Trades(context.Background(), "Bearer tok")
	if !errors.Is(err, service.ErrUpstreamUnavailable) {
		t.Fatalf("got %v, want ErrUpstreamUnavailable", err)
	}
}

// --- MarketDataClient ---

func TestMarketDataClient_DecodesBars(t *testing.T) {
	url := serve(t, json200(`{"bars":[
		{"timestamp":"2026-03-02T00:00:00Z","open":100,"high":105,"low":99,"close":103,"volume":1234}
	]}`))

	got, err := client.NewMarketDataClient(url).History(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 1 || got[0].Close != 103 || got[0].Volume != 1234 {
		t.Errorf("decoded bar is wrong: %+v", got)
	}
}

// market-data never 404s for an unknown symbol -- it answers 200 with an empty
// array -- so "no data" has to be detected from the body, not the status.
func TestMarketDataClient_EmptyBarsIsErrSymbolUnavailable(t *testing.T) {
	url := serve(t, json200(`{"bars":[]}`))

	_, err := client.NewMarketDataClient(url).History(context.Background(), "NOPE")
	if !errors.Is(err, service.ErrSymbolUnavailable) {
		t.Fatalf("got %v, want ErrSymbolUnavailable", err)
	}
	// The symbol is named, so FetchHistories' ordered error scan has something
	// to report and a log line is diagnosable on its own.
	if err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Errorf("error %v does not name the symbol", err)
	}
}

func TestMarketDataClient_NonOKIsUpstreamUnavailable(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := client.NewMarketDataClient(url).History(context.Background(), "AAPL")
	if !errors.Is(err, service.ErrUpstreamUnavailable) {
		t.Fatalf("got %v, want ErrUpstreamUnavailable", err)
	}
}

func TestMarketDataClient_MalformedBodyIsUpstreamUnavailable(t *testing.T) {
	url := serve(t, json200(`{"bars": not json`))

	_, err := client.NewMarketDataClient(url).History(context.Background(), "AAPL")
	if !errors.Is(err, service.ErrUpstreamUnavailable) {
		t.Fatalf("got %v, want ErrUpstreamUnavailable", err)
	}
}

// A symbol needing escaping must not corrupt the path.
func TestMarketDataClient_EscapesTheSymbolInThePath(t *testing.T) {
	var raw string
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// EscapedPath, not Path: the server decodes %2F back to "/", so
		// asserting on Path would pass whether or not anything was escaped.
		raw = r.URL.EscapedPath()
		json200(`{"bars":[{"timestamp":"2026-03-02T00:00:00Z","close":1}]}`)(w, r)
	})

	if _, err := client.NewMarketDataClient(url).History(context.Background(), "BRK/B"); err != nil {
		t.Fatalf("History: %v", err)
	}
	if raw != "/market-data/history/BRK%2FB" {
		t.Errorf("escaped path = %q, want the slash escaped -- an unescaped "+
			"symbol would silently address a different endpoint", raw)
	}
}
