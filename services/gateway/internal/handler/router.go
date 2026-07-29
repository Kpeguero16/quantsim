// Package handler wires the gateway's routes: which prefixes are public,
// which are gated, and which backend each one reaches.
package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/gateway/internal/httperr"
	"github.com/kpeguero/quantsim/services/gateway/internal/middleware"
)

// NewRouter builds the gateway's routing table.
//
// Middleware order is load-bearing, not stylistic:
//
//	StripUserID -> CORS -> [route group: RequireAuth -> InjectUserID] -> proxy
//
// StripUserID is outermost so no route -- public ones included -- can receive
// a client-set identity header. CORS sits outside RequireAuth so that a 401
// still carries CORS headers; without that a browser reports an opaque
// network error instead of the real status, and you debug the wrong layer.
func NewRouter(authProxy, marketDataProxy http.Handler, jwtSecret []byte, allowedOrigin string) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.StripUserID())
	r.Use(middleware.CORS(allowedOrigin))

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

	// Public: you cannot present a token before you have one.
	r.Handle("/auth/*", authProxy)

	r.Group(func(r chi.Router) {
		r.Use(pkgauth.RequireAuth(jwtSecret))
		r.Use(middleware.InjectUserID())

		r.Handle("/market-data/*", marketDataProxy)

		// Placeholder for the Phase 2 trading engine. It sits inside the
		// authenticated group deliberately: the auth wiring is real and
		// tested today, so swapping in a proxy later is a one-line change.
		r.Handle("/trading/*", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			httperr.Write(w, http.StatusNotImplemented, "not_implemented",
				"trading engine is not available until Phase 2")
		}))
	})

	return r
}
