package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
)

func NewRouter(auth *AuthHandler, jwtSecret []byte) *chi.Mux {
	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/auth", func(r chi.Router) {
		r.Post("/register", auth.Register)
		r.Post("/login", auth.Login)
		r.Post("/refresh", auth.Refresh)

		r.Group(func(r chi.Router) {
			r.Use(pkgauth.RequireAuth(jwtSecret))
			r.Get("/me", auth.Me)
		})
	})

	return r
}
