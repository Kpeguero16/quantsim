package middleware

import (
	"net/http"

	"github.com/kpeguero/quantsim/services/gateway/internal/httperr"
)

// MaxBodyBytes is the gateway-wide request body cap (docs/security-backlog.md
// item 4).
//
// 64 KiB, matching what auth has applied per request via MaxBytesReader since
// Step 9. Deliberately not tighter: the gateway must not reject bodies the
// service behind it would have accepted, or it becomes a second, invisible
// validation layer that disagrees with the real one. An order payload is a
// couple of hundred bytes -- the point of this control is coverage, not
// tightness.
const MaxBodyBytes = 64 << 10

// LimitBody caps how large a request body any backend can be made to read.
//
// Two mechanisms, because neither is sufficient alone:
//
//   - The Content-Length check rejects the common case early, with a clean
//     413 and the standard JSON error shape, before a single byte is proxied.
//     A MaxBytesReader alone could not produce that: it surfaces mid-copy
//     inside ReverseProxy and comes back as the proxy's 502, which tells the
//     caller the backend is broken when the request was.
//   - MaxBytesReader is what actually enforces the cap on a chunked body,
//     where Content-Length is -1 and there is nothing to check up front. A
//     length check alone would leave exactly that case uncapped -- and it is
//     the case an attacker chooses.
//
// Applied to every route rather than per-prefix, so a service added later
// inherits the cap instead of needing to remember it. That inheritance is the
// shape the backlog item asks for.
func LimitBody(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// -1 means "unknown" (chunked), not "enormous", so the comparison
			// has to be against a declared length rather than a missing one.
			if r.ContentLength > limit {
				httperr.Write(w, http.StatusRequestEntityTooLarge, "payload_too_large",
					"request body is too large")
				return
			}

			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}

			next.ServeHTTP(w, r)
		})
	}
}
