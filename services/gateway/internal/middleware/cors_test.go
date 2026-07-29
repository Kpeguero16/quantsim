package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kpeguero/quantsim/services/gateway/internal/middleware"
)

const devOrigin = "http://localhost:5173"

// recorder returns a handler that flips called to true, so tests can assert
// whether the middleware short-circuited or passed the request through.
func recorder(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestPreflightFromAllowedOrigin(t *testing.T) {
	var called bool
	h := middleware.CORS(devOrigin)(recorder(&called))

	req := httptest.NewRequest(http.MethodOptions, "/market-data/symbols", nil)
	req.Header.Set("Origin", devOrigin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Error("preflight reached the next handler; it must be answered by the middleware and never proxied")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusNoContent)
	}

	want := map[string]string{
		"Access-Control-Allow-Origin":  devOrigin,
		"Access-Control-Allow-Methods": "GET, POST, OPTIONS",
		"Access-Control-Allow-Headers": "Authorization, Content-Type",
		"Access-Control-Max-Age":       "300",
		"Vary":                         "Origin",
	}
	for header, wantValue := range want {
		if got := rec.Header().Get(header); got != wantValue {
			t.Errorf("%s = %q, want %q", header, got, wantValue)
		}
	}
}

func TestSimpleRequestFromAllowedOrigin(t *testing.T) {
	var called bool
	h := middleware.CORS(devOrigin)(recorder(&called))

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	req.Header.Set("Origin", devOrigin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Error("non-preflight request did not reach the next handler")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != devOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, devOrigin)
	}
}

// TestDisallowedOriginIsNotEchoed is the anti-reflection case: an attacker's
// origin must never come back in Allow-Origin, but Vary must still be set so
// a cache does not serve this response to the allowed origin.
func TestDisallowedOriginIsNotEchoed(t *testing.T) {
	for _, origin := range []string{
		"http://evil.example",
		"null",                           // sandboxed iframe / file://
		"http://localhost:5173.evil.com", // suffix trick
		"http://localhost:51730",         // prefix trick
		"https://localhost:5173",         // wrong scheme
	} {
		t.Run(origin, func(t *testing.T) {
			var called bool
			h := middleware.CORS(devOrigin)(recorder(&called))

			req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
			req.Header.Set("Origin", origin)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("Access-Control-Allow-Origin = %q, want it absent for origin %q", got, origin)
			}
			if got := rec.Header().Get("Vary"); got != "Origin" {
				t.Errorf("Vary = %q, want Origin even when the origin is rejected", got)
			}
		})
	}
}

// TestNoCredentialsHeader guards the setting that would turn a lax origin
// check into an account-takeover primitive. It must never appear.
func TestNoCredentialsHeader(t *testing.T) {
	var called bool
	h := middleware.CORS(devOrigin)(recorder(&called))

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	req.Header.Set("Origin", devOrigin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want it never set", got)
	}
}

// TestVaryAlwaysSet covers the cache-poisoning case for a request that
// carries no Origin at all (curl, server-to-server).
func TestVaryAlwaysSet(t *testing.T) {
	var called bool
	h := middleware.CORS(devOrigin)(recorder(&called))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin even with no Origin header present", got)
	}
	if !called {
		t.Error("request without an Origin did not reach the next handler")
	}
}

// TestBareOptionsIsNotTreatedAsPreflight: an OPTIONS without
// Access-Control-Request-Method is a normal request, not a preflight.
func TestBareOptionsIsNotTreatedAsPreflight(t *testing.T) {
	var called bool
	h := middleware.CORS(devOrigin)(recorder(&called))

	req := httptest.NewRequest(http.MethodOptions, "/market-data/symbols", nil)
	req.Header.Set("Origin", devOrigin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Error("bare OPTIONS was short-circuited; only a real preflight should be")
	}
}
