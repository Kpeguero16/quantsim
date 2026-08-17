// Package handler wires the trading engine's routes and translates between
// HTTP and the service layer.
package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
)

// NewRouter builds the trading engine's routing table.
//
// /healthz stays outside authentication so a load balancer can reach it
// without a token.
//
// Everything under /trading revalidates the caller's JWT here, even though
// the gateway already validated the same token and injected X-User-ID. That
// is the precedent auth set for /me: each service checks for itself rather
// than trusting a proxy header (SPEC.md §2.11). Trusting the header would
// make this -- the one surface in the project that moves money -- the only
// place in the codebase that takes an identity on faith, and would mean
// anything that ever reached this port directly could trade as anyone.
func NewRouter(trading *TradingHandler, jwtSecret []byte) *chi.Mux {
	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/trading", func(r chi.Router) {
		r.Use(pkgauth.RequireAuth(jwtSecret))

		r.Post("/orders", trading.PlaceOrder)
		r.Get("/orders", trading.ListOrders)
		r.Get("/positions", trading.ListPositions)
	})

	return r
}
