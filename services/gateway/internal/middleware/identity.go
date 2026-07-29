package middleware

import (
	"log"
	"net/http"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/gateway/internal/httperr"
)

// UserIDHeader carries the authenticated user's ID to backend services.
// pkg/auth.RequireAuth puts the token subject on the Go request context,
// which does not survive the process boundary -- a proxied backend can only
// see headers.
const UserIDHeader = "X-User-ID"

// StripUserID removes any client-supplied X-User-ID.
//
// This must be the outermost middleware, applying to every route including
// the public ones. Scoping it to authenticated routes would leave a hole:
// /auth/* is public and proxied, so the day anything downstream reads this
// header, an unauthenticated caller could set it to any user they like. A
// header the system treats as trusted identity gets scrubbed at the edge,
// unconditionally -- not at the point where it happens to be consumed.
func StripUserID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Del(UserIDHeader)
			next.ServeHTTP(w, r)
		})
	}
}

// InjectUserID sets X-User-ID from the validated JWT claims. It must run
// after pkg/auth.RequireAuth, which is what puts the ID on the context.
//
// Nothing consumes the header yet -- market-data is user-agnostic. It is here
// because the Phase 2 trading engine needs to know who is placing an order,
// and adding it now is cheaper than retrofitting the gateway and whatever
// assumed the header was absent.
func InjectUserID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := pkgauth.UserIDFromContext(r.Context())
			if !ok || userID == "" {
				// Reaching here means the chain is misordered -- RequireAuth
				// is not ahead of us -- so this request was never
				// authenticated. Forwarding it minus the header would let a
				// deployment mistake quietly serve unauthenticated traffic;
				// refusing surfaces the bug the first time it is exercised.
				//
				// The client gets a generic message: it is our bug, not
				// theirs, and the detail belongs in the log.
				log.Printf("gateway: InjectUserID reached with no authenticated user on %s %s -- middleware chain is misordered",
					r.Method, r.URL.Path)
				httperr.Write(w, http.StatusInternalServerError, "internal_error", "internal server error")
				return
			}
			r.Header.Set(UserIDHeader, userID)
			next.ServeHTTP(w, r)
		})
	}
}
