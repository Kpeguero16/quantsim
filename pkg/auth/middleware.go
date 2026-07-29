package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const userIDContextKey contextKey = "userID"

// RequireAuth validates the Bearer access token on every request. On
// success it puts the token's subject (the user ID string) on the request
// context via UserIDFromContext; on failure it writes 401 and stops the
// chain. It has no dependency on any service's internal packages, so it can
// be reused by any service that needs to gate a route (the gateway later).
func RequireAuth(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || token == "" {
				writeUnauthorized(w)
				return
			}

			claims, err := ValidateToken(secret, token)
			if err != nil || claims.TokenType != TokenTypeAccess {
				writeUnauthorized(w)
				return
			}

			ctx := context.WithValue(r.Context(), userIDContextKey, claims.Subject)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserIDFromContext returns the user ID string RequireAuth placed on the
// request context, and whether one was present.
func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDContextKey).(string)
	return id, ok
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"code":"invalid_token","message":"invalid or expired token"}`))
}
