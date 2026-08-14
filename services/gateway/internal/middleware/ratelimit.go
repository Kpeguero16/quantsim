package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
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

// statusRecorder captures the status the next handler wrote, so the per-
// account limiter can tell a rejected password from an accepted one.
//
// It forwards Flush because ReverseProxy streams: without it, a wrapped
// response would buffer where the unwrapped one did not, changing behaviour
// for every response that passes through.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Write covers handlers that never call WriteHeader explicitly, where net/http
// implies 200.
func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// loginEmail extracts the email from a login body, and returns the bytes to
// hand onward so the proxy still sees the request the client sent.
//
// It reads at most maxBytes+1: enough to notice the body is oversized without
// buffering an unbounded one, which would be a memory-exhaustion vector in
// the middleware meant to prevent abuse. An oversized or unparseable body
// yields no key and is forwarded untouched -- the gateway does not own
// login's validation rules, and inventing a rejection here would mean two
// services disagreeing about what a valid request looks like.
func loginEmail(r *http.Request, maxBytes int64) (key string, restore io.Reader) {
	if r.Body == nil {
		return "", nil
	}

	buf, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return "", bytes.NewReader(buf)
	}

	// Over the cap: replay what was read followed by whatever remains, so the
	// backend still receives the whole body despite it never being buffered.
	if int64(len(buf)) > maxBytes {
		return "", io.MultiReader(bytes.NewReader(buf), r.Body)
	}

	var payload struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(buf, &payload); err != nil {
		return "", bytes.NewReader(buf)
	}

	// The same normalisation Steps 9 and 10 made structural, down to the SQL
	// lookup on lower(email). Without it an attacker cycles Foo@, foo@, FOO@
	// for a fresh allowance against one account.
	return strings.ToLower(strings.TrimSpace(payload.Email)), bytes.NewReader(buf)
}

// RateLimitLoginByAccount applies exponential backoff to consecutive failed
// logins for one account, independent of which address they come from. Per-IP
// limiting alone does not stop credential stuffing spread across a botnet,
// where every bot starts with a fresh budget.
//
// Two properties here are security invariants rather than implementation
// detail, and both have tests written before this code existed:
//
// Only a 401 counts. A success clears the count, so a legitimate user is
// never throttled however often they sign in, and an upstream 502 does not
// accumulate as failed passwords -- which would turn a backend outage into a
// lockout.
//
// The key is whatever email the client submitted, and nothing here consults a
// database. That is what keeps a 429 from becoming the user-enumeration
// oracle Step 9 §2.12 deliberately closed: an unknown address accumulates
// failures exactly like a real one, because this code cannot tell them apart.
// See SPEC.md §2.6.
func RateLimitLoginByAccount(b *limiter.Backoff, maxBodyBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, restore := loginEmail(r, maxBodyBytes)
			if restore != nil {
				r.Body = io.NopCloser(restore)
			}

			// No usable key -- oversized or malformed body. Forward it and
			// let the auth service answer; per-IP limiting still applies.
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			if res := b.Check(key); !res.Allowed {
				writeRateLimited(w, res.RetryAfter)
				return
			}

			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			switch rec.status {
			case http.StatusUnauthorized:
				b.Fail(key)
			case http.StatusOK:
				b.Reset(key)
			}
		})
	}
}
