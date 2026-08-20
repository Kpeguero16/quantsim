// Package handler wires the AI insights service's routes and translates
// between HTTP and the service layer.
package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
)

// NewRouter builds the AI insights service's routing table.
//
// /healthz stays outside authentication so a load balancer can reach it
// without a token.
//
// Everything under /insights revalidates the caller's JWT here rather than
// trusting the gateway's X-User-ID header, matching trading-engine's and
// backtesting's precedent -- and, since this service forwards that same token
// onward to trading-engine (SPEC.md §6.5), validating it here is also what
// stops an invalid token being relayed to a service that moves money.
func NewRouter(insights *InsightsHandler, jwtSecret []byte) *chi.Mux {
	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Group(func(r chi.Router) {
		r.Use(pkgauth.RequireAuth(jwtSecret))

		r.Get("/insights/portfolio", insights.PortfolioInsights)
	})

	return r
}
