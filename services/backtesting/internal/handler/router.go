package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
)

// NewRouter builds the backtesting service's routing table.
//
// /healthz stays outside authentication so a load balancer can reach it
// without a token.
//
// Everything under /backtests revalidates the caller's JWT here, even though
// the gateway already validated the same token and injected X-User-ID --
// the same precedent trading-engine set in Step 14 (its own router.go:
// "each service checks for itself rather than trusting a proxy header").
func NewRouter(backtest *BacktestHandler, jwtSecret []byte) *chi.Mux {
	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Not r.Route("/backtests", ...) with sub-patterns of "/" -- that leaves
	// the collection routes (POST and GET with no id) dependent on chi's
	// trailing-slash matching. A plain Group with full paths has no such
	// ambiguity: "/backtests" and "/backtests/{id}" are unrelated patterns,
	// not a mount point and its root.
	r.Group(func(r chi.Router) {
		r.Use(pkgauth.RequireAuth(jwtSecret))

		r.Post("/backtests", backtest.RunBacktest)
		r.Get("/backtests", backtest.ListBacktests)
		r.Get("/backtests/{id}", backtest.GetBacktest)
	})

	return r
}
