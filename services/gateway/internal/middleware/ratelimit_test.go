package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/gateway/internal/limiter"
	"github.com/kpeguero/quantsim/services/gateway/internal/middleware"
)

const (
	ipLimit  = 2
	ipWindow = 15 * time.Minute
)

func newIPLimiter(t *testing.T) http.Handler {
	t.Helper()
	store := limiter.NewMemoryStore(time.Now)
	var called bool
	return middleware.RateLimitByIP(store, ipLimit, ipWindow)(recorder(&called))
}

// loginRequest builds a POST /auth/login arriving from remoteAddr.
func loginRequest(remoteAddr string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = remoteAddr
	return req
}

// Test #4 -- THE bypass test, and the reason this middleware keys on
// RemoteAddr at all.
//
// docs/security-backlog.md claimed the gateway's SetXForwarded() call
// sanitises inbound X-Forwarded-For, so a limiter could trust that header.
// It does not: that call runs on r.Out inside proxy.New's Rewrite, which
// happens after every middleware and shapes only the *upstream* request. The
// inbound header a middleware sees is whatever the client sent.
//
// A limiter keying on it would be defeated by one extra curl flag per
// request -- an unlimited budget, while still looking like it works. See
// SPEC.md §2.5.
func TestForgedForwardedHeaderDoesNotBuyAFreshBudget(t *testing.T) {
	h := newIPLimiter(t)

	for i := 1; i <= ipLimit; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginRequest("203.0.113.9:54321"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d got status %d, want 200; the budget should not be spent yet", i, rec.Code)
		}
	}

	// Same connection, a different forged X-Forwarded-For on every attempt.
	for i, forged := range []string{"9.9.9.9", "8.8.8.8", "1.1.1.1"} {
		req := loginRequest("203.0.113.9:54321")
		req.Header.Set("X-Forwarded-For", forged)
		req.Header.Set("X-Real-IP", forged)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("attempt %d with X-Forwarded-For %q got status %d, want %d -- a client-supplied header must never earn a fresh budget",
				i+1, forged, rec.Code, http.StatusTooManyRequests)
		}
	}
}

// Test #10 -- the refusal honours the JSON error contract every other
// QuantSim endpoint uses, and tells the caller when to come back.
func TestRefusalShapeAndRetryAfter(t *testing.T) {
	h := newIPLimiter(t)

	var rec *httptest.ResponseRecorder
	for i := 0; i <= ipLimit; i++ {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, loginRequest("203.0.113.9:54321"))
	}

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("refusal body is not JSON (%v); a frontend calling response.json() would throw", err)
	}
	if body.Code != "rate_limited" {
		t.Errorf("code = %q, want %q", body.Code, "rate_limited")
	}
	if body.Message == "" {
		t.Error("message is empty; the error shape requires both fields")
	}

	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("no Retry-After header on a 429; a refused caller has no idea when to retry")
	}
	secs, err := strconv.Atoi(retryAfter)
	if err != nil {
		t.Fatalf("Retry-After = %q, want an integer number of seconds", retryAfter)
	}
	if secs <= 0 || secs > int(ipWindow.Seconds()) {
		t.Errorf("Retry-After = %d seconds, want between 1 and %d", secs, int(ipWindow.Seconds()))
	}
}

// Two connections must not share a budget, or one noisy client would lock
// every other user out of authentication entirely.
func TestSeparateAddressesHaveSeparateBudgets(t *testing.T) {
	h := newIPLimiter(t)

	for i := 0; i <= ipLimit; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginRequest("203.0.113.9:54321"))
		_ = rec
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginRequest("198.51.100.7:44444"))
	if rec.Code != http.StatusOK {
		t.Errorf("a second address got status %d, want 200; budgets must be per-client", rec.Code)
	}
}

// The port changes on every TCP connection from the same client, so keying on
// the raw RemoteAddr would give one attacker an unlimited number of budgets.
func TestPortIsStrippedFromTheKey(t *testing.T) {
	h := newIPLimiter(t)

	for i := 1; i <= ipLimit; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginRequest("203.0.113.9:1000"+strconv.Itoa(i)))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d got status %d, want 200", i, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginRequest("203.0.113.9:99999"))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("got status %d from the same IP on a new port, want %d -- the port must not be part of the key",
			rec.Code, http.StatusTooManyRequests)
	}
}

// IPv6 addresses are bracketed in RemoteAddr, so naive splitting on ":" would
// mangle them into per-request keys and silently disable the limiter for any
// client on IPv6 -- including everything on localhost.
func TestIPv6AddressesShareOneKey(t *testing.T) {
	h := newIPLimiter(t)

	for i := 1; i <= ipLimit; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginRequest("[2001:db8::1]:5000"+strconv.Itoa(i)))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d got status %d, want 200", i, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginRequest("[2001:db8::1]:60000"))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("got status %d from the same IPv6 address, want %d", rec.Code, http.StatusTooManyRequests)
	}
}
