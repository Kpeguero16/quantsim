package middleware_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
	return middleware.RateLimitByIP(store, ipLimit, ipWindow, middleware.TrustedProxies{})(recorder(&called))
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

// --- per-account backoff on /auth/login ---------------------------------

const maxLoginBody = 64 << 10

func loginBackoff() *limiter.Backoff {
	return limiter.NewBackoff(time.Now, limiter.BackoffConfig{
		FreeFailures: 4,
		BaseDelay:    time.Minute,
		MaxDelay:     15 * time.Minute,
	})
}

// alwaysStatus is a stand-in for the auth service that answers every request
// with one status, and records the body it received.
func alwaysStatus(status int, gotBody *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if gotBody != nil {
			*gotBody = string(b)
		}
		w.WriteHeader(status)
	})
}

func postLogin(t *testing.T, h http.Handler, email string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"email":` + strconv.Quote(email) + `,"password":"whatever-it-does-not-matter"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:54321"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Test #8 -- THE enumeration-oracle test.
//
// Step 9 §2.12 made login's failure uniform on purpose: an unknown email and
// a wrong password return the same 401, behind the same dummy-hash timing
// defence, so login cannot be used to discover which accounts exist.
//
// A per-account throttle is an easy way to undo that. If the limiter only
// counted failures for accounts that exist, a 429 would mean "this address is
// real" and a 401 would mean "it is not" -- handing an attacker the exact
// oracle Step 9 closed, through a control added to make login safer.
//
// SPEC.md §2.6 is the rule this pins: throttle on the submitted email whether
// or not it resolves to an account. The middleware never consults a database,
// so it cannot tell the difference and neither can a caller.
func TestUnknownAndKnownAccountsThrottleIdentically(t *testing.T) {
	// One backend, answering 401 to everything -- exactly what the real auth
	// service does for both an unknown email and a wrong password.
	h := middleware.RateLimitLoginByAccount(loginBackoff(), maxLoginBody)(alwaysStatus(http.StatusUnauthorized, nil))

	const (
		existing = "real-user@example.test"
		unknown  = "no-such-person@example.test"
	)

	drive := func(email string) *httptest.ResponseRecorder {
		var last *httptest.ResponseRecorder
		for i := 0; i < 6; i++ {
			last = postLogin(t, h, email)
		}
		return last
	}

	gotExisting := drive(existing)
	gotUnknown := drive(unknown)

	if gotExisting.Code != http.StatusTooManyRequests {
		t.Fatalf("existing account got status %d after 6 failures, want %d", gotExisting.Code, http.StatusTooManyRequests)
	}
	if gotUnknown.Code != gotExisting.Code {
		t.Errorf("unknown account got status %d but existing got %d -- a 429 that only happens for real accounts is an enumeration oracle",
			gotUnknown.Code, gotExisting.Code)
	}
	if gotUnknown.Body.String() != gotExisting.Body.String() {
		t.Errorf("bodies differ between unknown and existing accounts:\n unknown  = %q\n existing = %q",
			gotUnknown.Body.String(), gotExisting.Body.String())
	}
	if gotUnknown.Header().Get("Retry-After") != gotExisting.Header().Get("Retry-After") {
		t.Errorf("Retry-After differs: unknown = %q, existing = %q",
			gotUnknown.Header().Get("Retry-After"), gotExisting.Header().Get("Retry-After"))
	}
}

// Test #9 -- capitalisation must not buy a fresh budget. Steps 9 and 10 made
// identity case-insensitive all the way down to the SQL lookup; a limiter
// keyed on the raw string would let an attacker cycle Foo@, foo@, FOO@ for an
// unlimited allowance against one account.
func TestEmailKeyIsCaseAndSpaceInsensitive(t *testing.T) {
	h := middleware.RateLimitLoginByAccount(loginBackoff(), maxLoginBody)(alwaysStatus(http.StatusUnauthorized, nil))

	for _, variant := range []string{
		"user@example.test",
		"User@Example.test",
		"USER@EXAMPLE.TEST",
		"  user@example.test  ",
		"uSeR@eXaMpLe.TeSt",
	} {
		postLogin(t, h, variant)
	}

	rec := postLogin(t, h, "user@example.test")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("got status %d after 5 failures across capitalisations, want %d -- all variants must share one counter",
			rec.Code, http.StatusTooManyRequests)
	}
}

// A success clears the count, so an ordinary user who mistypes and then gets
// it right is not left carrying a throttle.
func TestSuccessfulLoginClearsTheBackoff(t *testing.T) {
	b := loginBackoff()
	failing := middleware.RateLimitLoginByAccount(b, maxLoginBody)(alwaysStatus(http.StatusUnauthorized, nil))
	succeeding := middleware.RateLimitLoginByAccount(b, maxLoginBody)(alwaysStatus(http.StatusOK, nil))

	for i := 0; i < 4; i++ {
		postLogin(t, failing, "user@example.test")
	}
	postLogin(t, succeeding, "user@example.test")

	// The slate is clean, so the next four failures must again cost nothing.
	for i := 1; i <= 4; i++ {
		if rec := postLogin(t, failing, "user@example.test"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d got status %d, want 401 -- a success must reset the counter", i, rec.Code)
		}
	}
}

// The proxy must receive the body byte-for-byte. Reading it to find the email
// consumes it, so a missing replay would send an empty body upstream and
// break every login -- the most likely way this middleware could go wrong.
func TestBodyIsReplayedToTheBackendUnchanged(t *testing.T) {
	var seen string
	h := middleware.RateLimitLoginByAccount(loginBackoff(), maxLoginBody)(alwaysStatus(http.StatusOK, &seen))

	want := `{"email":"user@example.test","password":"whatever-it-does-not-matter"}`
	postLogin(t, h, "user@example.test")

	if seen != want {
		t.Errorf("backend received body %q, want %q", seen, want)
	}
}

// An oversized body is passed through untouched rather than rejected. The
// gateway does not own login's validation rules, so it forwards the request
// and lets the auth service answer -- it simply cannot key it to an account.
func TestOversizedBodyPassesThroughUntouched(t *testing.T) {
	const maxBytes = 64
	var seen string
	h := middleware.RateLimitLoginByAccount(loginBackoff(), maxBytes)(alwaysStatus(http.StatusOK, &seen))

	body := `{"email":"user@example.test","password":"` + strings.Repeat("x", 200) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:54321"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200 -- an oversized body is the auth service's call, not the gateway's", rec.Code)
	}
	if seen != body {
		t.Errorf("backend received a truncated or altered body:\n got  %q\n want %q", seen, body)
	}
}

// Malformed JSON is likewise forwarded. There is no account to key on, and
// inventing a rejection here would mean two services disagreeing about what a
// valid login request looks like.
func TestMalformedBodyPassesThrough(t *testing.T) {
	var seen string
	h := middleware.RateLimitLoginByAccount(loginBackoff(), maxLoginBody)(alwaysStatus(http.StatusBadRequest, &seen))

	body := `{"email": not-valid-json`
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:54321"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want the backend's 400", rec.Code)
	}
	if seen != body {
		t.Errorf("backend received %q, want the original body %q", seen, body)
	}
}

// Only a 401 counts as a failure. A backend that is down returns 502, and
// treating that as a failed password would throttle users out of a service
// that is already broken -- turning an outage into a lockout.
func TestNon401FailuresDoNotCountAgainstTheAccount(t *testing.T) {
	h := middleware.RateLimitLoginByAccount(loginBackoff(), maxLoginBody)(alwaysStatus(http.StatusBadGateway, nil))

	for i := 1; i <= 10; i++ {
		if rec := postLogin(t, h, "user@example.test"); rec.Code != http.StatusBadGateway {
			t.Fatalf("attempt %d got status %d, want 502 -- upstream errors must not accumulate as failed logins", i, rec.Code)
		}
	}
}

// A parser differential between the gateway and the service behind it is a
// bypass, not a nitpick.
//
// The auth service decodes with json.NewDecoder(...).Decode(), which parses
// the first JSON value and ignores trailing bytes. This middleware originally
// used json.Unmarshal, which rejects them. The gap was directly exploitable:
// appending one junk byte to a valid login body made the gateway fail to
// extract an account key -- skipping per-account backoff completely -- while
// auth parsed the same body and processed the login. Measured end to end, a
// threshold of 3 became 10 of 10 guesses reaching the backend.
//
// The invariant: the gateway must never be stricter than the service it
// protects. Anything auth will act on must be something this can key.
func TestTrailingDataDoesNotBypassAccountKeying(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"trailing garbage", `{"email":"victim@example.test","password":"guess"}x`},
		{"trailing object", `{"email":"victim@example.test","password":"guess"} {"junk":1}`},
		{"trailing whitespace", `{"email":"victim@example.test","password":"guess"}` + "\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := middleware.RateLimitLoginByAccount(loginBackoff(), maxLoginBody)(alwaysStatus(http.StatusUnauthorized, nil))

			var last *httptest.ResponseRecorder
			for i := 0; i < 6; i++ {
				req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(tc.body))
				req.RemoteAddr = "203.0.113.9:54321"
				last = httptest.NewRecorder()
				h.ServeHTTP(last, req)
			}

			if last.Code != http.StatusTooManyRequests {
				t.Errorf("got status %d after 6 failures, want %d -- trailing data let the caller skip per-account backoff entirely while auth still processed the login",
					last.Code, http.StatusTooManyRequests)
			}
		})
	}
}
