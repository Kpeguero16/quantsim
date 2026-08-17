// Package handler wires the gateway's routes: which prefixes are public,
// which are gated, and which backend each one reaches.
package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/gateway/internal/httperr"
	"github.com/kpeguero/quantsim/services/gateway/internal/limiter"
	"github.com/kpeguero/quantsim/services/gateway/internal/middleware"
)

// RateLimitConfig carries the authentication rate-limiting policy. Grouped
// into a struct rather than spread across NewRouter's parameters so adding a
// knob does not churn every call site.
type RateLimitConfig struct {
	// Enabled is an escape hatch. A bug in a control that sits in front of
	// login must not be an outage with no way out, so it can be switched off
	// without a code change or a rollback.
	Enabled bool

	Store    limiter.Store
	IPLimit  int
	IPWindow time.Duration

	// Backoff throttles repeated failed logins per account. Nil disables the
	// per-account dimension while leaving per-IP in place, which is what the
	// routing and auth tests use.
	Backoff *limiter.Backoff

	// MaxLoginBody caps how much of a login body is buffered to find the
	// email. Anything larger is forwarded without an account key rather than
	// rejected -- the gateway does not own login's validation rules.
	MaxLoginBody int64
}

// NewRouter builds the gateway's routing table.
//
// Middleware order is load-bearing, not stylistic:
//
//	StripUserID -> CORS -> LimitBody -> [/auth/*: RateLimitByIP] -> proxy
//	StripUserID -> CORS -> LimitBody -> [group: RequireAuth -> InjectUserID] -> proxy
//
// StripUserID is outermost so no route -- public ones included -- can receive
// a client-set identity header. CORS sits outside RequireAuth so that a 401
// still carries CORS headers; without that a browser reports an opaque
// network error instead of the real status, and you debug the wrong layer.
// RateLimitByIP is inside CORS for exactly the same reason, and scoped to
// /auth/* because that is the surface worth protecting -- /healthz must stay
// answerable to a load balancer no matter how busy the box is.
func NewRouter(authProxy, marketDataProxy, tradingProxy http.Handler, jwtSecret []byte, allowedOrigin string, rateLimit RateLimitConfig) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.StripUserID())
	r.Use(middleware.CORS(allowedOrigin))
	// Inside CORS, for the same reason RequireAuth and RateLimitByIP are: a
	// 413 without CORS headers reaches a browser as an opaque network error,
	// and you go looking for the bug in the wrong layer.
	r.Use(middleware.LimitBody(middleware.MaxBodyBytes))

	// chi's defaults answer these in plain text, which breaks the JSON error
	// contract every other QuantSim endpoint honours -- a frontend that calls
	// response.json() on an error path would throw on a typo'd URL rather
	// than showing the error.
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		httperr.Write(w, http.StatusNotFound, "not_found", "no route matches this path")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		httperr.Write(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed for this path")
	})

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Public: you cannot present a token before you have one. Which is
	// precisely why it is rate limited -- an unauthenticated endpoint that
	// checks credentials is the one an attacker can hammer for free.
	r.Group(func(r chi.Router) {
		if rateLimit.Enabled {
			r.Use(middleware.RateLimitByIP(rateLimit.Store, rateLimit.IPLimit, rateLimit.IPWindow))
		}

		// /auth/login carries the per-account backoff as well. It is mounted
		// as its own route rather than applied to the whole prefix because
		// only login has an account to key on and a 401 that means "wrong
		// password" -- /auth/refresh returns 401 for an expired token, which
		// is not a credential guess and must not accumulate as one.
		//
		// chi matches this static path ahead of the /auth/* wildcard below.
		if rateLimit.Enabled && rateLimit.Backoff != nil {
			r.With(middleware.RateLimitLoginByAccount(rateLimit.Backoff, rateLimit.MaxLoginBody)).
				Handle("/auth/login", authProxy)
		}

		r.Handle("/auth/*", authProxy)
	})

	r.Group(func(r chi.Router) {
		r.Use(pkgauth.RequireAuth(jwtSecret))
		r.Use(middleware.InjectUserID())

		r.Handle("/market-data/*", marketDataProxy)

		// The trading engine, live since Step 14 -- the placeholder that stood
		// here returned 501 from inside this same group, so this is the
		// one-line swap it was left as. trading-engine revalidates the token
		// itself as well (SPEC.md §2.11); the X-User-ID injected here is a
		// convenience for the backend, never the thing it trusts.
		r.Handle("/trading/*", tradingProxy)
	})

	return r
}
