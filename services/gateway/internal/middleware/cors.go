// Package middleware holds the gateway's cross-cutting request handling:
// CORS, and the identity header the backends will eventually trust.
package middleware

import "net/http"

const (
	allowedMethods = "GET, POST, OPTIONS"
	// Authorization carries the JWT; Content-Type is needed for the JSON
	// request bodies auth's register/login endpoints take.
	allowedHeaders = "Authorization, Content-Type"
	// 300s of preflight caching, mostly to keep the dev console quiet.
	maxAge = "300"
)

// CORS allows browser requests from exactly one origin.
//
// The origin is compared by exact string equality against allowedOrigin and
// echoed only on a match. That single rule is what keeps the four standard
// CORS failure modes out:
//
//   - reflecting an attacker-supplied Origin -- never happens, the request's
//     Origin is compared and never echoed unvalidated
//   - "*" together with Allow-Credentials -- neither is ever sent; QuantSim's
//     JWT rides in the Authorization header, not a cookie, so credentialed
//     CORS is unnecessary and would only add risk
//   - a missing Vary: Origin, which lets a shared cache serve one origin's
//     response to another -- set unconditionally below, match or not
//   - accepting the literal "null" origin (sandboxed iframes, file://) --
//     falls out of exact matching, "null" simply is not the constant
//
// Preflight requests are answered here and never proxied to a backend.
func CORS(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Unconditional: the response varies by Origin whether or not
			// this particular request carried a matching one, and a cache
			// needs to know that either way.
			w.Header().Add("Vary", "Origin")

			origin := r.Header.Get("Origin")
			allowed := origin != "" && origin == allowedOrigin

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}

			// A preflight is an OPTIONS carrying Access-Control-Request-Method.
			// A bare OPTIONS is not, and is left to the routes below.
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if allowed {
					w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
					w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
					w.Header().Set("Access-Control-Max-Age", maxAge)
				}
				// 204 regardless: a disallowed origin gets a response with no
				// CORS headers, which the browser rejects on its own. Either
				// way the preflight stops here rather than reaching a backend.
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
