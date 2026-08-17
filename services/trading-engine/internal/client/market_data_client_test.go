package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/trading-engine/internal/client"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
)

// Every test here runs against an httptest.Server, so the suite never needs
// the real market-data running (SPEC.md §5).
func newClient(t *testing.T, h http.HandlerFunc) *client.MarketDataClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return client.NewMarketDataClient(srv.URL)
}

func TestLatestPrice_Success(t *testing.T) {
	var gotPath string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"AAPL","price":187.25,"timestamp":"2026-08-17T12:00:00Z"}`))
	})

	price, err := c.LatestPrice(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("LatestPrice: %v", err)
	}
	if price != 187.25 {
		t.Errorf("got %v, want 187.25", price)
	}
	if gotPath != "/market-data/prices/AAPL" {
		t.Errorf("got path %q, want /market-data/prices/AAPL", gotPath)
	}
}

// market-data's own 404 is this engine's rejection: SPEC.md §2.7 deliberately
// keeps no separate symbol whitelist, so this status is the only thing that
// distinguishes "no such ticker" from "we cannot reach the service".
func TestLatestPrice_404IsSymbolUnavailable(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"price_not_cached","message":"no cached price for symbol yet"}`))
	})

	_, err := c.LatestPrice(context.Background(), "NOSUCH")

	if !errors.Is(err, service.ErrSymbolUnavailable) {
		t.Fatalf("got %v, want ErrSymbolUnavailable", err)
	}
	if errors.Is(err, service.ErrUpstreamUnavailable) {
		t.Error("a missing symbol must not also read as an outage")
	}
}

func TestLatestPrice_OtherStatusesAreUpstreamUnavailable(t *testing.T) {
	for _, status := range []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusUnauthorized,
		http.StatusTooManyRequests,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})

			_, err := c.LatestPrice(context.Background(), "AAPL")

			if !errors.Is(err, service.ErrUpstreamUnavailable) {
				t.Fatalf("got %v, want ErrUpstreamUnavailable", err)
			}
		})
	}
}

func TestLatestPrice_UnreachableServiceIsUpstreamUnavailable(t *testing.T) {
	// A port nothing is listening on: the ordinary "forgot to start
	// market-data" case, and the one a developer hits most.
	c := client.NewMarketDataClient("http://127.0.0.1:1")

	_, err := c.LatestPrice(context.Background(), "AAPL")

	if !errors.Is(err, service.ErrUpstreamUnavailable) {
		t.Fatalf("got %v, want ErrUpstreamUnavailable", err)
	}
}

// A 200 is not automatically a price. Each of these would otherwise fill an
// order at zero -- a trade that looks entirely legitimate in the history.
func TestLatestPrice_UnusableBodiesFailClosed(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed JSON", `{"price":`},
		{"not JSON at all", `<html>502 Bad Gateway</html>`},
		{"empty body", ``},
		{"price absent", `{"symbol":"AAPL"}`},
		{"price zero", `{"symbol":"AAPL","price":0}`},
		{"price negative", `{"symbol":"AAPL","price":-12.5}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			})

			price, err := c.LatestPrice(context.Background(), "AAPL")

			if !errors.Is(err, service.ErrUpstreamUnavailable) {
				t.Fatalf("got (%v, %v), want ErrUpstreamUnavailable", price, err)
			}
			if price != 0 {
				t.Errorf("got price %v alongside an error; callers must not see a usable number here", price)
			}
		})
	}
}

// The timeout is the reason this client has an explicit http.Client: without
// one, a market-data that accepts the connection and then hangs would hang
// every order behind it.
func TestLatestPrice_HangingServiceTimesOutRatherThanBlockingForever(t *testing.T) {
	release := make(chan struct{})
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		<-release
	})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.LatestPrice(ctx, "AAPL")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, service.ErrUpstreamUnavailable) {
			t.Fatalf("got %v, want ErrUpstreamUnavailable", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LatestPrice never returned; the request is not bounded")
	}
}

// The symbol reaches the URL escaped. Nothing valid needs this, which is the
// point: a caller-supplied string ends up in a path, and path traversal into
// another of market-data's endpoints should not be one query away.
func TestLatestPrice_SymbolIsPathEscaped(t *testing.T) {
	var gotPath string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNotFound)
	})

	_, _ = c.LatestPrice(context.Background(), "../symbols")

	if gotPath != "/market-data/prices/..%2Fsymbols" {
		t.Errorf("got path %q, want the symbol escaped into a single segment", gotPath)
	}
}
