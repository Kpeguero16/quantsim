package middleware

import (
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/kpeguero/quantsim/services/gateway/internal/httperr"
	"github.com/kpeguero/quantsim/services/gateway/internal/limiter"
)

// rateLimitCode and rateLimitMessage are shared by every refusal this package
// emits, per-IP and per-account alike.
//
// The sameness is a security property, not tidiness. SPEC.md §2.6: if a
// per-account throttle were distinguishable from a per-IP one, a 429 would
// tell an attacker which of the two applied -- and a per-account throttle
// only exists for an email the system is tracking. One code and one message
// keeps a refusal from saying anything about the account behind it.
const (
	rateLimitCode    = "rate_limited"
	rateLimitMessage = "too many requests, please try again later"
)

// writeRateLimited emits the standard 429. Retry-After is rounded *up* to the
// next whole second: rounding down would advertise a moment at which the
// caller is still blocked, and a client obeying the header would retry into
// another refusal.
func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if retryAfter > 0 && retryAfter > time.Duration(seconds)*time.Second {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	httperr.Write(w, http.StatusTooManyRequests, rateLimitCode, rateLimitMessage)
}

// clientIP returns the address the request actually arrived from.
//
// It reads r.RemoteAddr and nothing else. Not X-Forwarded-For, not X-Real-IP
// -- both are client-authored at this hop and a limiter keying on either can
// be given a fresh budget per request by anyone who sets the header.
//
// docs/security-backlog.md asserted the opposite: that the gateway's
// SetXForwarded() call makes the inbound header trustworthy. It does not.
// That call runs on r.Out inside proxy.New's Rewrite (proxy.go:59), which
// executes after all middleware and builds only the request sent upstream.
// Nothing sanitises the inbound header, because until now nothing read it.
//
// The port is stripped because it differs on every TCP connection; leaving it
// in the key would hand a single client an unbounded number of budgets.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// No port to split (some test servers, some proxies). The whole value
		// is then the address -- better a coarse key than no limiting.
		return r.RemoteAddr
	}
	return host
}

// RateLimitByIP caps how many requests one client address may make within a
// fixed window. It runs before the proxy, so a flood is refused without ever
// reaching a backend.
//
// It must sit inside the CORS middleware. A 429 without CORS headers reaches
// a browser as an opaque network error rather than a status, and the next
// person to debug it will start at the wrong layer -- the same reasoning that
// put RequireAuth inside CORS in Step 7.
func RateLimitByIP(store limiter.Store, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res := store.Allow(clientIP(r), limit, window)
			if !res.Allowed {
				writeRateLimited(w, res.RetryAfter)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
