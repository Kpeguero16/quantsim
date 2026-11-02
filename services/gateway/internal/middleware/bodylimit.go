package middleware

import (
	"bytes"
	"errors"
	"io"
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

// BufferBody reads the whole request body before the request reaches the proxy.
//
// The proxy's transport takes a backend connection first and copies the body
// second, and it caps connections per backend (proxy.maxConnsPerHost). Streaming
// would let a client hold one of those slots for as long as it cared to withhold
// its body, because ResponseHeaderTimeout only starts once the body has been
// sent. Measured: 128 connections that each sent one byte of a declared 100 froze
// that backend for every other caller, past the 30s header timeout and until the
// attacker hung up. Waiting here costs the client's own goroutine and nothing that
// anyone else needs.
//
// It must sit inside LimitBody, whose cap is what bounds this read, and as close
// to the proxy as possible, so that a request about to be refused is refused
// without waiting for a body it does not need.
func BufferBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}

		buf, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				httperr.Write(w, http.StatusRequestEntityTooLarge, "payload_too_large",
					"request body is too large")
				return
			}
			httperr.Write(w, http.StatusBadRequest, "invalid_request", "request body could not be read")
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(buf))
		// A chunked body now has a known length. Forwarding it as chunked would
		// work too, but this way the backend sees one framing for every request.
		r.ContentLength = int64(len(buf))
		r.TransferEncoding = nil

		next.ServeHTTP(w, r)
	})
}
