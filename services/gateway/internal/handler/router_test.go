package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/gateway/internal/handler"
	"github.com/kpeguero/quantsim/services/gateway/internal/httperr"
	"github.com/kpeguero/quantsim/services/gateway/internal/limiter"
	"github.com/kpeguero/quantsim/services/gateway/internal/proxy"
)

const (
	testOrigin = "http://localhost:5173"
	testUserID = "11111111-2222-3333-4444-555555555555"
)

var testSecret = []byte("test-secret-at-least-32-bytes-long!!")

// backendCall records what a backend service actually received, so tests can
// assert both that a request arrived and that it arrived unchanged.
type backendCall struct {
	hit    bool
	path   string
	userID string
	auth   string
}

// newGateway spins up fake auth and market-data backends and returns a router
// wired to them, plus the recorders for each backend.
func newGateway(t *testing.T) (http.Handler, *backendCall, *backendCall) {
	t.Helper()
	return newGatewayWithRateLimit(t, noRateLimit)
}

func newGatewayWithRateLimit(t *testing.T, rateLimit handler.RateLimitConfig) (http.Handler, *backendCall, *backendCall) {
	t.Helper()
	gw, authCall, mdCall, _, _, _ := newGatewayBackends(t, rateLimit)
	return gw, authCall, mdCall
}

// newTradingGateway is the same wiring, returning the trading backend's
// recorder instead. Separate rather than a fourth return value everywhere, so
// the routing, CORS and rate-limit tests that predate the trading engine keep
// their existing call shape.
func newTradingGateway(t *testing.T) (http.Handler, *backendCall) {
	t.Helper()
	gw, _, _, tradingCall, _, _ := newGatewayBackends(t, noRateLimit)
	return gw, tradingCall
}

// newBacktestingGateway is the same wiring, returning the backtesting
// backend's recorder -- the Step 16 counterpart to newTradingGateway.
func newBacktestingGateway(t *testing.T) (http.Handler, *backendCall) {
	t.Helper()
	gw, _, _, _, backtestingCall, _ := newGatewayBackends(t, noRateLimit)
	return gw, backtestingCall
}

// newInsightsGateway is the Step 20 counterpart.
func newInsightsGateway(t *testing.T) (http.Handler, *backendCall) {
	t.Helper()
	gw, _, _, _, _, insightsCall := newGatewayBackends(t, noRateLimit)
	return gw, insightsCall
}

func newGatewayBackends(t *testing.T, rateLimit handler.RateLimitConfig) (http.Handler, *backendCall, *backendCall, *backendCall, *backendCall, *backendCall) {
	t.Helper()

	authCall := &backendCall{}
	mdCall := &backendCall{}
	tradingCall := &backendCall{}
	backtestingCall := &backendCall{}
	insightsCall := &backendCall{}

	record := func(c *backendCall) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			c.hit = true
			c.path = r.URL.Path
			c.userID = r.Header.Get("X-User-ID")
			c.auth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}

	authBackend := httptest.NewServer(record(authCall))
	t.Cleanup(authBackend.Close)
	mdBackend := httptest.NewServer(record(mdCall))
	t.Cleanup(mdBackend.Close)
	tradingBackend := httptest.NewServer(record(tradingCall))
	t.Cleanup(tradingBackend.Close)
	backtestingBackend := httptest.NewServer(record(backtestingCall))
	t.Cleanup(backtestingBackend.Close)
	insightsBackend := httptest.NewServer(record(insightsCall))
	t.Cleanup(insightsBackend.Close)

	authURL, err := url.Parse(authBackend.URL)
	if err != nil {
		t.Fatalf("parsing auth backend URL: %v", err)
	}
	mdURL, err := url.Parse(mdBackend.URL)
	if err != nil {
		t.Fatalf("parsing market-data backend URL: %v", err)
	}
	tradingURL, err := url.Parse(tradingBackend.URL)
	if err != nil {
		t.Fatalf("parsing trading backend URL: %v", err)
	}
	backtestingURL, err := url.Parse(backtestingBackend.URL)
	if err != nil {
		t.Fatalf("parsing backtesting backend URL: %v", err)
	}
	insightsURL, err := url.Parse(insightsBackend.URL)
	if err != nil {
		t.Fatalf("parsing insights backend URL: %v", err)
	}

	transport := proxy.NewTransport()
	r := handler.NewRouter(
		proxy.New(authURL, transport, "auth"),
		proxy.New(mdURL, transport, "market-data"),
		proxy.New(tradingURL, transport, "trading-engine"),
		proxy.New(backtestingURL, transport, "backtesting"),
		proxy.New(insightsURL, transport, "ai-insights"),
		testSecret,
		testOrigin,
		rateLimit,
	)
	return r, authCall, mdCall, tradingCall, backtestingCall, insightsCall
}

// noRateLimit leaves the limiter out of the chain, so the existing routing,
// CORS and auth tests exercise what they were written to exercise rather than
// tripping over a budget.
var noRateLimit = handler.RateLimitConfig{Enabled: false}

// rateLimitAfter returns a config that refuses everything past limit requests
// from one address.
func rateLimitAfter(limit int) handler.RateLimitConfig {
	return handler.RateLimitConfig{
		Enabled:  true,
		Store:    limiter.NewMemoryStore(time.Now),
		IPLimit:  limit,
		IPWindow: 15 * time.Minute,
	}
}

func accessToken(t *testing.T) string {
	t.Helper()
	token, err := pkgauth.GenerateToken(testSecret, testUserID, pkgauth.TokenTypeAccess, time.Minute)
	if err != nil {
		t.Fatalf("generating token: %v", err)
	}
	return token
}

func TestHealthzIsLocalAndUnauthenticated(t *testing.T) {
	gw, authCall, mdCall := newGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
	if authCall.hit || mdCall.hit {
		t.Error("/healthz reached a backend; it must be answered by the gateway itself")
	}
}

func TestAuthRoutesArePublic(t *testing.T) {
	gw, authCall, _ := newGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/register", nil)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if !authCall.hit {
		t.Fatal("/auth/register did not reach the auth backend without a token")
	}
	if authCall.path != "/auth/register" {
		t.Errorf("auth backend saw path %q, want /auth/register unchanged", authCall.path)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want the backend's 200", rec.Code)
	}
}

// TestProtectedRouteWithoutTokenIsBlocked is the central authorization
// guarantee: the request must be rejected at the gateway and never forwarded.
func TestProtectedRouteWithoutTokenIsBlocked(t *testing.T) {
	for _, path := range []string{"/market-data/symbols", "/trading/orders", "/backtests"} {
		t.Run(path, func(t *testing.T) {
			gw, _, mdCall := newGateway(t)

			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			gw.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("got status %d, want 401", rec.Code)
			}
			if mdCall.hit {
				t.Error("request reached a backend without a valid token")
			}

			var body httperr.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("401 body is not the JSON error shape: %v (body %q)", err, rec.Body.String())
			}
			if body.Code != "invalid_token" {
				t.Errorf("got code %q, want invalid_token", body.Code)
			}
		})
	}
}

func TestProtectedRouteWithTokenIsProxied(t *testing.T) {
	gw, _, mdCall := newGateway(t)
	token := accessToken(t)

	req := httptest.NewRequest(http.MethodGet, "/market-data/prices/AAPL", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	if !mdCall.hit {
		t.Fatal("request did not reach the market-data backend")
	}
	if mdCall.path != "/market-data/prices/AAPL" {
		t.Errorf("backend saw path %q, want it unchanged", mdCall.path)
	}
	if mdCall.userID != testUserID {
		t.Errorf("backend saw X-User-ID %q, want %q", mdCall.userID, testUserID)
	}
	if mdCall.auth != "Bearer "+token {
		t.Error("Authorization header was not forwarded intact")
	}
}

// TestSpoofedUserIDNeverReachesBackend covers the two ways a caller might try
// to set identity by hand: with no token at all, and alongside a valid one.
func TestSpoofedUserIDNeverReachesBackend(t *testing.T) {
	t.Run("public route, no token", func(t *testing.T) {
		gw, authCall, _ := newGateway(t)

		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.Header.Set("X-User-ID", "attacker-chosen")
		gw.ServeHTTP(httptest.NewRecorder(), req)

		if !authCall.hit {
			t.Fatal("request did not reach the auth backend")
		}
		if authCall.userID != "" {
			t.Errorf("auth backend saw X-User-ID %q; it must be stripped", authCall.userID)
		}
	})

	t.Run("protected route, valid token plus spoof", func(t *testing.T) {
		gw, _, mdCall := newGateway(t)

		req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
		req.Header.Set("Authorization", "Bearer "+accessToken(t))
		req.Header.Set("X-User-ID", "attacker-chosen")
		gw.ServeHTTP(httptest.NewRecorder(), req)

		if mdCall.userID != testUserID {
			t.Errorf("backend saw X-User-ID %q, want the token subject %q", mdCall.userID, testUserID)
		}
	})
}

// Replaces the 501 placeholder assertion this file carried from Step 9 until
// Step 14: /trading/* now reaches the trading engine, with the path intact and
// the identity injected from the token.
func TestTradingReachesTheTradingEngine(t *testing.T) {
	gw, tradingCall := newTradingGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/trading/orders",
		strings.NewReader(`{"symbol":"AAPL","side":"buy","quantity":1}`))
	req.Header.Set("Authorization", "Bearer "+accessToken(t))
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if !tradingCall.hit {
		t.Fatal("/trading/orders did not reach the trading engine")
	}
	if tradingCall.path != "/trading/orders" {
		t.Errorf("backend saw path %q, want /trading/orders unchanged", tradingCall.path)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want the backend's 200", rec.Code)
	}
	if tradingCall.userID != testUserID {
		t.Errorf("backend saw X-User-ID %q, want the token subject %q", tradingCall.userID, testUserID)
	}
	// The engine revalidates the token itself (SPEC.md §2.11), so the header
	// has to survive the hop -- stripping it would break every /trading route.
	if tradingCall.auth == "" {
		t.Error("the Authorization header did not survive the proxy hop")
	}
}

// The Step 16 counterpart to TestTradingReachesTheTradingEngine.
func TestBacktestsReachesTheBacktestingEngine(t *testing.T) {
	gw, backtestingCall := newBacktestingGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/backtests",
		strings.NewReader(`{"symbol":"AAPL","short_window":10,"long_window":50,"start_date":"2025-01-01","end_date":"2025-06-01","starting_capital":10000}`))
	req.Header.Set("Authorization", "Bearer "+accessToken(t))
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if !backtestingCall.hit {
		t.Fatal("/backtests did not reach the backtesting engine")
	}
	if backtestingCall.path != "/backtests" {
		t.Errorf("backend saw path %q, want /backtests unchanged", backtestingCall.path)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want the backend's 200", rec.Code)
	}
	if backtestingCall.userID != testUserID {
		t.Errorf("backend saw X-User-ID %q, want the token subject %q", backtestingCall.userID, testUserID)
	}
	// backtesting revalidates the token itself (SPEC.md Step 16 §2.7), so the
	// header has to survive the hop.
	if backtestingCall.auth == "" {
		t.Error("the Authorization header did not survive the proxy hop")
	}
}

func TestBacktestsIgnoresAClientSuppliedUserID(t *testing.T) {
	gw, backtestingCall := newBacktestingGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/backtests", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken(t))
	req.Header.Set("X-User-ID", "99999999-9999-9999-9999-999999999999")
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if backtestingCall.userID != testUserID {
		t.Errorf("backend saw X-User-ID %q, want the token subject %q -- a forged header survived",
			backtestingCall.userID, testUserID)
	}
}

// The gateway rejects before the engine ever sees the request. Both layers
// check, and this pins the outer one.
func TestTradingWithoutATokenNeverReachesTheEngine(t *testing.T) {
	for _, path := range []string{"/trading/orders", "/trading/positions", "/trading/portfolio"} {
		t.Run(path, func(t *testing.T) {
			gw, tradingCall := newTradingGateway(t)

			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			gw.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got status %d, want 401", rec.Code)
			}
			if tradingCall.hit {
				t.Error("an unauthenticated trading request reached the engine")
			}
		})
	}
}

// A client-set X-User-ID must not survive: StripUserID runs outermost, and the
// only identity the engine can see is the one derived from the token.
func TestTradingIgnoresAClientSuppliedUserID(t *testing.T) {
	gw, tradingCall := newTradingGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/trading/orders", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken(t))
	req.Header.Set("X-User-ID", "99999999-9999-9999-9999-999999999999")
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if tradingCall.userID != testUserID {
		t.Errorf("backend saw X-User-ID %q, want the token subject %q -- a forged header survived",
			tradingCall.userID, testUserID)
	}
}

// trading-engine down is the proxy's 502 upstream_unavailable -- the same code
// the engine itself returns when market-data is down, which reads coherently
// from the outside rather than as two different failures.
func TestTradingEngineDownIs502(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("parsing backend URL: %v", err)
	}
	backend.Close() // nothing is listening there now

	gw := handler.NewRouter(
		proxy.New(backendURL, proxy.NewTransport(), "auth"),
		proxy.New(backendURL, proxy.NewTransport(), "market-data"),
		proxy.New(backendURL, proxy.NewTransport(), "trading-engine"),
		proxy.New(backendURL, proxy.NewTransport(), "backtesting"),
		proxy.New(backendURL, proxy.NewTransport(), "ai-insights"),
		testSecret, testOrigin, noRateLimit,
	)

	req := httptest.NewRequest(http.MethodGet, "/trading/positions", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken(t))
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("got status %d, want 502", rec.Code)
	}
	var body httperr.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("502 body is not the JSON error shape: %v", err)
	}
	if body.Code != "upstream_unavailable" {
		t.Errorf("got code %q, want upstream_unavailable", body.Code)
	}
}

// TestUnmatchedRoutesUseTheJSONErrorShape: chi answers these in plain text by
// default. A frontend calling response.json() on an error path -- reasonable,
// since every other QuantSim endpoint honours the shape -- would otherwise
// throw a parse error on a typo'd URL instead of showing the real problem.
func TestUnmatchedRoutesUseTheJSONErrorShape(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
	}{
		{"unknown path", http.MethodGet, "/nope", http.StatusNotFound, "not_found"},
		{"wrong method on healthz", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "method_not_allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw, _, _ := newGateway(t)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			gw.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", rec.Code, tt.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("got Content-Type %q, want application/json", ct)
			}

			var body httperr.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not the JSON error shape: %v (body %q)", err, rec.Body.String())
			}
			if body.Code != tt.wantCode {
				t.Errorf("got code %q, want %q", body.Code, tt.wantCode)
			}
		})
	}
}

// TestUnauthorizedResponseCarriesCORSHeaders is why CORS sits outside
// RequireAuth. Without it the browser turns a 401 into an opaque network
// error and the real status never reaches the frontend.
func TestUnauthorizedResponseCarriesCORSHeaders(t *testing.T) {
	gw, _, _ := newGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	req.Header.Set("Origin", testOrigin)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != testOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q on a 401", got, testOrigin)
	}
}

// TestPreflightNeedsNoTokenAndIsNotProxied: a browser sends preflights
// without Authorization, so a preflight that required auth would make every
// cross-origin call from the frontend fail.
func TestPreflightNeedsNoTokenAndIsNotProxied(t *testing.T) {
	gw, _, mdCall := newGateway(t)

	req := httptest.NewRequest(http.MethodOptions, "/market-data/symbols", nil)
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("got status %d, want 204", rec.Code)
	}
	if mdCall.hit {
		t.Error("preflight was proxied to the backend")
	}
}

// Test #11 -- a 429 must carry CORS headers.
//
// This is why RateLimitByIP sits inside the CORS middleware rather than
// outside it. Without the headers a browser cannot read the response at all:
// fetch rejects with an opaque network error, the frontend shows "failed to
// fetch" instead of "too many requests", and whoever debugs it starts at the
// network layer rather than at the limiter.
func TestRateLimitedResponseCarriesCORSHeaders(t *testing.T) {
	const limit = 2
	r, _, _ := newGatewayWithRateLimit(t, rateLimitAfter(limit))

	var rec *httptest.ResponseRecorder
	for i := 0; i <= limit; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.Header.Set("Origin", testOrigin)
		req.RemoteAddr = "203.0.113.9:54321"
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got status %d after %d requests, want %d", rec.Code, limit+1, http.StatusTooManyRequests)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != testOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q -- a browser cannot read a 429 without it", got, testOrigin)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
}

// Health checks must never be rate limited. /healthz is what a load balancer
// polls, and refusing it under load would pull a healthy instance out of
// rotation at precisely the moment the limiter is doing its job.
func TestHealthzIsNotRateLimited(t *testing.T) {
	const limit = 1
	r, _, _ := newGatewayWithRateLimit(t, rateLimitAfter(limit))

	for i := 0; i < limit+5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "203.0.113.9:54321"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/healthz got status %d on request %d, want 200", rec.Code, i+1)
		}
	}
}

// The limiter is scoped to /auth/*, so exhausting that budget must not affect
// the authenticated routes.
func TestRateLimitDoesNotApplyToMarketData(t *testing.T) {
	const limit = 1
	r, _, mdCall := newGatewayWithRateLimit(t, rateLimitAfter(limit))

	for i := 0; i <= limit; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "203.0.113.9:54321"
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken(t))
	req.RemoteAddr = "203.0.113.9:54321"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("/market-data got status %d after the auth budget was spent, want 200", rec.Code)
	}
	if !mdCall.hit {
		t.Error("market-data backend was never reached; the auth limiter must not gate other routes")
	}
}

// gatewayWithFailingAuth builds a router whose auth backend answers every
// request with 401, which is what the real service returns for both a wrong
// password and an unknown email.
func gatewayWithFailingAuth(t *testing.T, failures int) http.Handler {
	t.Helper()
	return gatewayWithLimits(t, 1000, failures)
}

// gatewayWithLimits builds a router with both limiter dimensions active, so a
// test can raise whichever one it is not exercising out of the way.
func gatewayWithLimits(t *testing.T, ipLimit, failures int) http.Handler {
	t.Helper()

	authBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(authBackend.Close)
	mdBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(mdBackend.Close)

	authURL, err := url.Parse(authBackend.URL)
	if err != nil {
		t.Fatalf("parsing auth backend URL: %v", err)
	}
	mdURL, err := url.Parse(mdBackend.URL)
	if err != nil {
		t.Fatalf("parsing market-data backend URL: %v", err)
	}

	transport := proxy.NewTransport()
	return handler.NewRouter(
		proxy.New(authURL, transport, "auth"),
		proxy.New(mdURL, transport, "market-data"),
		// This suite only exercises the login limiter; the trading,
		// backtesting and insights backends are never reached, so all three
		// point at market-data's stub rather than standing up servers for
		// nothing.
		proxy.New(mdURL, transport, "trading-engine"),
		proxy.New(mdURL, transport, "backtesting"),
		proxy.New(mdURL, transport, "ai-insights"),
		testSecret,
		testOrigin,
		handler.RateLimitConfig{
			Enabled:  true,
			Store:    limiter.NewMemoryStore(time.Now),
			IPLimit:  ipLimit,
			IPWindow: 15 * time.Minute,
			Backoff: limiter.NewBackoff(time.Now, limiter.BackoffConfig{
				FreeFailures: failures - 1,
				BaseDelay:    time.Minute,
				MaxDelay:     15 * time.Minute,
			}),
			MaxLoginBody: 64 << 10,
		},
	)
}

func postJSON(t *testing.T, gw http.Handler, path, body, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)
	return rec
}

// Per-account backoff must work through the full chain, and must key on the
// account rather than the connection -- that is the whole point of having it
// alongside the per-IP limit. Failures are spread across different addresses
// here, as credential stuffing from a botnet would be.
func TestLoginBackoffAppliesAcrossDifferentAddresses(t *testing.T) {
	gw := gatewayWithFailingAuth(t, 5)
	const body = `{"email":"victim@example.test","password":"guess"}`

	for i := 1; i <= 5; i++ {
		addr := "203.0.113." + strconv.Itoa(i) + ":40000"
		if rec := postJSON(t, gw, "/auth/login", body, addr); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d from %s got status %d, want 401", i, addr, rec.Code)
		}
	}

	rec := postJSON(t, gw, "/auth/login", body, "203.0.113.99:40000")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("got status %d from a fresh address after 5 failures, want %d -- backoff must follow the account, not the connection",
			rec.Code, http.StatusTooManyRequests)
	}
}

// /auth/refresh must not accumulate account backoff. It returns 401 for an
// expired or malformed token, which is not a credential guess -- counting it
// would throttle users for holding a stale token.
func TestRefreshDoesNotAccumulateAccountBackoff(t *testing.T) {
	gw := gatewayWithFailingAuth(t, 5)

	for i := 1; i <= 10; i++ {
		rec := postJSON(t, gw, "/auth/refresh", `{"refresh_token":"expired"}`, "203.0.113.9:40000")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("refresh attempt %d got status %d, want the backend's 401", i, rec.Code)
		}
	}
}

// The static /auth/login route must win over the /auth/* wildcard, or the
// per-account limiter silently never runs.
func TestLoginRouteTakesPrecedenceOverWildcard(t *testing.T) {
	gw := gatewayWithFailingAuth(t, 2)
	const body = `{"email":"user@example.test","password":"guess"}`

	for i := 1; i <= 2; i++ {
		postJSON(t, gw, "/auth/login", body, "203.0.113.9:40000")
	}

	rec := postJSON(t, gw, "/auth/login", body, "203.0.113.9:40000")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("got status %d, want %d -- /auth/login is being served by the wildcard route, so the account limiter never runs",
			rec.Code, http.StatusTooManyRequests)
	}
}

// Per-IP limiting must still apply to /auth/login once the per-account route
// is registered.
//
// This combination had no coverage: the CORS and precedence tests above run
// with Backoff nil, so /auth/login is served by the /auth/* wildcard there.
// With Backoff set it becomes its own static route registered via
// r.With(...), and if chi did not carry the group's r.Use middleware onto it,
// the single most attacked endpoint in the system would silently lose its
// per-IP limit while every other test stayed green.
func TestPerIPStillAppliesToTheStaticLoginRoute(t *testing.T) {
	const ipLimit = 3
	gw := gatewayWithLimits(t, ipLimit, 10000) // account threshold high enough never to fire
	body := `{"email":"user@example.test","password":"guess"}`

	for i := 1; i <= ipLimit; i++ {
		if rec := postJSON(t, gw, "/auth/login", body, "203.0.113.9:54321"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d got status %d, want the backend's 401", i, rec.Code)
		}
	}

	rec := postJSON(t, gw, "/auth/login", body, "203.0.113.9:54321")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("got status %d past the per-IP limit, want %d -- the static /auth/login route is not inheriting the group's per-IP middleware",
			rec.Code, http.StatusTooManyRequests)
	}
}

// The same measurement that exposed the gap, kept as a regression test.
//
// Before check-and-count became atomic, a burst of 60 concurrent guesses
// against one account at a threshold of 5 saw all 60 reach the backend: the
// counter went untouched for the length of each proxy round-trip, so every
// request in flight saw a clean slate. Per-account backoff bounded sequential
// guessing only, which is the wrong half of the problem for a distributed
// attack.
func TestConcurrentBurstIsBoundedByTheAccountThreshold(t *testing.T) {
	const threshold = 5
	gw := gatewayWithLimits(t, 10000, threshold) // per-IP raised out of the way
	body := `{"email":"victim@example.test","password":"guess"}`

	const burst = 60
	var wg sync.WaitGroup
	codes := make([]int, burst)
	wg.Add(burst)
	for i := 0; i < burst; i++ {
		go func(i int) {
			defer wg.Done()
			codes[i] = postJSON(t, gw, "/auth/login", body, "203.0.113.9:54321").Code
		}(i)
	}
	wg.Wait()

	reached := 0
	for _, c := range codes {
		if c == http.StatusUnauthorized {
			reached++
		}
	}

	if reached > threshold {
		t.Errorf("%d of %d concurrent guesses reached the backend, want at most %d -- the burst outran the counter",
			reached, burst, threshold)
	}
	t.Logf("burst of %d concurrent guesses: %d reached the backend, %d refused", burst, reached, burst-reached)
}

// The body cap applies to every prefix, not just the login route it started
// on (docs/security-backlog.md item 4). Asserted per-prefix because "a service
// added later inherits it" is the property the backlog item asks for, and only
// a per-prefix test can show it.
func TestOversizedBodyIsRejectedOnEveryPrefix(t *testing.T) {
	paths := []string{"/auth/login", "/auth/register", "/market-data/prices/AAPL", "/trading/orders"}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			gw, authCall, mdCall := newGateway(t)

			body := strings.Repeat("x", 128<<10)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+accessToken(t))
			rec := httptest.NewRecorder()
			gw.ServeHTTP(rec, req)

			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("got %d, want 413", rec.Code)
			}
			var errBody httperr.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
				t.Fatalf("413 body is not the JSON error shape: %v", err)
			}
			if errBody.Code != "payload_too_large" {
				t.Errorf("got code %q, want payload_too_large", errBody.Code)
			}
			if authCall.hit || mdCall.hit {
				t.Error("an oversized body reached a backend")
			}
		})
	}
}

// The 413 has to carry CORS headers, or a browser reports an opaque network
// error and the caller debugs the wrong layer. This is why LimitBody sits
// inside CORS rather than outside it.
func TestOversizedBodyStillCarriesCORSHeaders(t *testing.T) {
	gw, _, _ := newGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(strings.Repeat("x", 128<<10)))
	req.Header.Set("Origin", testOrigin)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != testOrigin {
		t.Errorf("got Allow-Origin %q, want %q", got, testOrigin)
	}
}

// A normal login is unaffected: the cap must not change the behaviour of the
// requests it exists to let through.
func TestNormalBodyStillReachesTheBackend(t *testing.T) {
	gw, authCall, _ := newGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(`{"email":"a@example.test","password":"hunter2hunter2"}`))
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want the backend's 200", rec.Code)
	}
	if !authCall.hit {
		t.Error("a normal login body did not reach auth")
	}
}

// The Step 20 counterpart to TestBacktestsReachesTheBacktestingEngine.
func TestInsightsReachesTheInsightsService(t *testing.T) {
	gw, insightsCall := newInsightsGateway(t)

	// Captured once: accessToken mints a fresh JTI on every call, so a second
	// call would not produce the string this one sent.
	token := accessToken(t)

	req := httptest.NewRequest(http.MethodGet, "/insights/portfolio", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if !insightsCall.hit {
		t.Fatal("/insights/portfolio did not reach the insights service")
	}
	if insightsCall.path != "/insights/portfolio" {
		t.Errorf("backend saw path %q, want /insights/portfolio unchanged", insightsCall.path)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want the backend's 200", rec.Code)
	}
	if insightsCall.userID != testUserID {
		t.Errorf("backend saw X-User-ID %q, want the token subject %q", insightsCall.userID, testUserID)
	}
	// Load-bearing twice over here: ai-insights revalidates the token itself
	// like the other engines, AND forwards this very header onward to
	// trading-engine to read the caller's trades (SPEC.md Step 20 §6.5).
	// Stripping it would turn every insights request into a 401 from a
	// service two hops away.
	if insightsCall.auth != "Bearer "+token {
		t.Errorf("backend saw Authorization %q, want the caller's own token unchanged", insightsCall.auth)
	}
}

func TestInsightsIgnoresAClientSuppliedUserID(t *testing.T) {
	gw, insightsCall := newInsightsGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/insights/portfolio", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken(t))
	req.Header.Set("X-User-ID", "99999999-9999-9999-9999-999999999999")
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if insightsCall.userID != testUserID {
		t.Errorf("backend saw X-User-ID %q, want the token subject %q -- a forged header survived",
			insightsCall.userID, testUserID)
	}
}

// The gateway rejects before ai-insights ever sees the request, so an
// unauthenticated caller cannot make it fan out to two other services.
func TestInsightsWithoutATokenNeverReachesTheService(t *testing.T) {
	gw, insightsCall := newInsightsGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/insights/portfolio", nil)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", rec.Code)
	}
	if insightsCall.hit {
		t.Error("an unauthenticated request reached the insights service")
	}
}
