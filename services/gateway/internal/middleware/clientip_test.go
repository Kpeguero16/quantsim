package middleware_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/gateway/internal/limiter"
	"github.com/kpeguero/quantsim/services/gateway/internal/middleware"
)

// The proxy every test here puts in front of the gateway, and the CIDR that
// names it.
const (
	proxyAddr = "172.18.0.5"
	proxyCIDR = "172.18.0.0/16"
)

func trustedProxies(t *testing.T, raw string) middleware.TrustedProxies {
	t.Helper()
	trusted, err := middleware.ParseTrustedProxies(raw)
	if err != nil {
		t.Fatalf("ParseTrustedProxies(%q): %v", raw, err)
	}
	return trusted
}

// newProxiedLimiter is the deployed shape: a limiter that trusts exactly one
// proxy, receiving every request from that proxy's address.
func newProxiedLimiter(t *testing.T, trustRaw string) http.Handler {
	t.Helper()
	store := limiter.NewMemoryStore(time.Now)
	var called bool
	return middleware.RateLimitByIP(store, ipLimit, ipWindow, trustedProxies(t, trustRaw))(recorder(&called))
}

// proxiedRequest is a login arriving from the proxy, carrying whatever
// X-Forwarded-For the proxy would have produced.
func proxiedRequest(forwarded string) *http.Request {
	req := loginRequest(proxyAddr + ":40000")
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	return req
}

func spend(t *testing.T, h http.Handler, req func() *http.Request, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req())
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d got status %d, want 200; the budget should not be spent yet", i, rec.Code)
		}
	}
}

// THE test for this feature. Without it every client behind the proxy shares
// one budget -- 100 requests per 15 minutes for the entire internet, arriving
// as "login is broken for everyone" rather than as anything resembling a rate
// limit. SPEC.md §2.10.
func TestTwoClientsBehindOneTrustedProxyGetSeparateBudgets(t *testing.T) {
	h := newProxiedLimiter(t, proxyCIDR)

	spend(t, h, func() *http.Request { return proxiedRequest("203.0.113.9") }, ipLimit)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proxiedRequest("203.0.113.9"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the first client got %d after spending its budget, want %d", rec.Code, http.StatusTooManyRequests)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, proxiedRequest("198.51.100.7"))
	if rec.Code != http.StatusOK {
		t.Errorf("a second client behind the same proxy got %d, want 200 -- budgets are per client, not per proxy", rec.Code)
	}
}

// The rightmost entry is the one the trusted hop wrote; every entry to its
// left was supplied by the client. Taking the leftmost -- the reflex, since
// "the original client IP" is what X-Forwarded-For sounds like it means --
// hands out one fresh budget per forged prefix, which is the exact bypass
// keying on RemoteAddr was protecting against.
func TestForgedForwardedPrefixDoesNotBuyAFreshBudgetBehindAProxy(t *testing.T) {
	h := newProxiedLimiter(t, proxyCIDR)

	real := "203.0.113.9"
	spend(t, h, func() *http.Request { return proxiedRequest(real) }, ipLimit)

	for i, forged := range []string{"9.9.9.9", "8.8.8.8", "1.1.1.1", "9.9.9.9, 8.8.8.8"} {
		rec := httptest.NewRecorder()
		// What the proxy actually produces: the client's header, then the
		// address it really saw, appended on the right.
		h.ServeHTTP(rec, proxiedRequest(forged+", "+real))
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("forged prefix %d (%q) got status %d, want %d -- a client must not be able to reset its own budget",
				i+1, forged, rec.Code, http.StatusTooManyRequests)
		}
	}
}

// The pre-Step-26 guarantee, unchanged and now conditional on nothing being
// trusted. This is the case every deployment without a proxy is in.
func TestForwardedHeaderIsIgnoredWhenNothingIsTrusted(t *testing.T) {
	h := newProxiedLimiter(t, "")

	spend(t, h, func() *http.Request { return proxiedRequest("203.0.113.9") }, ipLimit)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proxiedRequest("198.51.100.7"))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("got status %d, want %d -- with no trusted proxies the header must not be read at all",
			rec.Code, http.StatusTooManyRequests)
	}
}

// An untrusted peer is untrusted no matter what it claims, even while some
// OTHER address is trusted. This is the case that separates "we trust this
// header from this hop" from "we trust this header".
func TestForwardedHeaderIsIgnoredFromAnUntrustedPeer(t *testing.T) {
	store := limiter.NewMemoryStore(time.Now)
	var called bool
	h := middleware.RateLimitByIP(store, ipLimit, ipWindow, trustedProxies(t, proxyCIDR))(recorder(&called))

	// 198.51.100.7 is not inside 172.18.0.0/16.
	attacker := func() *http.Request {
		req := loginRequest("198.51.100.7:44444")
		req.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(int(time.Now().UnixNano()%250)))
		return req
	}
	spend(t, h, attacker, ipLimit)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, attacker())
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("got status %d, want %d -- only a trusted peer's header may be believed",
			rec.Code, http.StatusTooManyRequests)
	}
}

// A trusted proxy that sends nothing usable must fall back to the peer, not to
// an empty key. An empty key would merge every such request into one bucket --
// the bug this whole mechanism exists to prevent, reintroduced by its own
// error path.
func TestTrustedProxyWithNoUsableHeaderFallsBackToThePeer(t *testing.T) {
	for _, forwarded := range []string{"", "not-an-ip", "  ", "example.com"} {
		t.Run(strconv.Quote(forwarded), func(t *testing.T) {
			h := newProxiedLimiter(t, proxyCIDR)
			spend(t, h, func() *http.Request { return proxiedRequest(forwarded) }, ipLimit)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, proxiedRequest(forwarded))
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("got status %d, want %d -- the proxy's own address is the fallback key",
					rec.Code, http.StatusTooManyRequests)
			}
		})
	}
}

// The peer fallback has to be the PEER, not a constant. Keying the
// no-usable-header case on "" is invisible with one proxy -- every request
// still lands in one bucket either way -- and merges two proxies into a single
// budget the moment there are two. Which is the bug this mechanism exists to
// prevent, reintroduced by its own error path.
func TestTrustedProxiesWithNoHeaderDoNotShareABudget(t *testing.T) {
	h := newProxiedLimiter(t, proxyCIDR)

	first := func() *http.Request { return loginRequest("172.18.0.5:40000") }
	spend(t, h, first, ipLimit)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, first())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the first proxy got %d after spending its budget, want %d", rec.Code, http.StatusTooManyRequests)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loginRequest("172.18.0.6:40000"))
	if rec.Code != http.StatusOK {
		t.Errorf("a second trusted proxy got %d, want 200 -- the fallback key must be the peer, not a constant", rec.Code)
	}
}

// Some proxies write "ip:port" into X-Forwarded-For even though the spec does
// not ask for it. The port must not reach the key, for the same reason it is
// stripped from RemoteAddr: it changes per connection.
func TestForwardedPortIsStrippedFromTheKey(t *testing.T) {
	h := newProxiedLimiter(t, proxyCIDR)

	spend(t, h, func() *http.Request { return proxiedRequest("203.0.113.9:11111") }, ipLimit)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proxiedRequest("203.0.113.9:22222"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got status %d from the same forwarded IP on a new port, want %d",
			rec.Code, http.StatusTooManyRequests)
	}

	// The half that the check above cannot see. If the "ip:port" form were
	// rejected outright instead of parsed, both requests would fall back to
	// the proxy's own address, share a bucket, and produce exactly the 429
	// above -- a passing test for a feature that does not exist. A DIFFERENT
	// client in the same form has to get its own budget.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, proxiedRequest("198.51.100.7:33333"))
	if rec.Code != http.StatusOK {
		t.Errorf("a different forwarded IP got %d, want 200 -- the address, not the whole field, is the key", rec.Code)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		peer    string
		trusted bool
	}{
		{"empty trusts nothing", "", "172.18.0.5", false},
		{"cidr contains", "172.18.0.0/16", "172.18.0.5", true},
		{"cidr excludes", "172.18.0.0/16", "10.0.0.1", false},
		{"bare address is a host", "172.18.0.5", "172.18.0.5", true},
		{"bare address is only that host", "172.18.0.5", "172.18.0.6", false},
		{"list", "10.0.0.1, 172.18.0.0/16", "172.18.0.9", true},
		{"ipv6 cidr", "fd00::/8", "fd00::1", true},
		// A v4 address arriving as ::ffff:a.b.c.d is the same host. Comparing
		// without unmapping silently fails to trust the proxy, and the
		// symptom is one shared budget again.
		{"ipv4-mapped ipv6 peer", "172.18.0.0/16", "::ffff:172.18.0.5", true},
		{"a v6 prefix does not swallow v4", "::/0", "172.18.0.5", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := limiter.NewMemoryStore(time.Now)
			var called bool
			h := middleware.RateLimitByIP(store, ipLimit, ipWindow, trustedProxies(t, tc.raw))(recorder(&called))

			// net.JoinHostPort, not concatenation. An IPv6 RemoteAddr has
			// to be bracketed or SplitHostPort refuses it, peerIP falls back
			// to the whole string INCLUDING the port, and every request gets
			// its own key. The first version of this table concatenated, and
			// the ipv4-mapped case passed because of that rather than because
			// the address was trusted -- a green test asserting nothing.
			req := func() *http.Request {
				r := loginRequest(net.JoinHostPort(tc.peer, "40000"))
				r.Header.Set("X-Forwarded-For", "203.0.113.9")
				return r
			}
			spend(t, h, req, ipLimit)

			// If the peer is trusted the key is 203.0.113.9, so changing the
			// forwarded address gets a fresh budget. If it is not, the key is
			// the peer and the budget is already gone.
			rec := httptest.NewRecorder()
			r := loginRequest(net.JoinHostPort(tc.peer, "40001"))
			r.Header.Set("X-Forwarded-For", "198.51.100.7")
			h.ServeHTTP(rec, r)

			got := rec.Code == http.StatusOK
			if got != tc.trusted {
				t.Errorf("peer %q against %q: header believed = %v, want %v", tc.peer, tc.raw, got, tc.trusted)
			}
		})
	}
}

func TestParseTrustedProxiesRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"nonsense", "172.18.0.0/99", "172.18.0.0/16, oops", "1.2.3.4.5"} {
		if _, err := middleware.ParseTrustedProxies(raw); err == nil {
			t.Errorf("ParseTrustedProxies(%q) = nil error, want a failure -- an unparseable value must not "+
				"silently become 'trust nothing', which looks identical to a working limiter", raw)
		}
	}
}
