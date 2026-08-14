package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

	authCall := &backendCall{}
	mdCall := &backendCall{}

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

	authURL, err := url.Parse(authBackend.URL)
	if err != nil {
		t.Fatalf("parsing auth backend URL: %v", err)
	}
	mdURL, err := url.Parse(mdBackend.URL)
	if err != nil {
		t.Fatalf("parsing market-data backend URL: %v", err)
	}

	transport := proxy.NewTransport()
	r := handler.NewRouter(
		proxy.New(authURL, transport, "auth"),
		proxy.New(mdURL, transport, "market-data"),
		testSecret,
		testOrigin,
		rateLimit,
	)
	return r, authCall, mdCall
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
	for _, path := range []string{"/market-data/symbols", "/trading/orders"} {
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

func TestTradingReturns501(t *testing.T) {
	gw, _, _ := newGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/trading/orders", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken(t))
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("got status %d, want 501", rec.Code)
	}

	var body httperr.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("501 body is not the JSON error shape: %v", err)
	}
	if body.Code != "not_implemented" {
		t.Errorf("got code %q, want not_implemented", body.Code)
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
