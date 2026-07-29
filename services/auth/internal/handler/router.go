package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func NewRouter(auth *AuthHandler) *chi.Mux {
	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/auth", func(r chi.Router) {
		r.Post("/register", auth.Register)
	})

	return r
}
