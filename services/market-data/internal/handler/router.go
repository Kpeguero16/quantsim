package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func NewRouter(h *MarketDataHandler) *chi.Mux {
	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/market-data", func(r chi.Router) {
		r.Get("/symbols", SymbolsHandler)
		r.Post("/ingest", h.Ingest)
	})

	return r
}
