// Package handler wires the trading engine's routes and translates between
// HTTP and the service layer.
package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// NewRouter builds the trading engine's routing table.
//
// /healthz stays outside any authentication, so a load balancer can reach it
// without a token. The /trading/* routes arrive with RequireAuth in front of
// them -- this service revalidates the caller's JWT itself rather than
// trusting the gateway's X-User-ID header, matching what auth already does
// for /me (SPEC.md §2.11).
func NewRouter() *chi.Mux {
	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return r
}
