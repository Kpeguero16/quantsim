// Package limiter holds the gateway's rate-limiting primitives: a Store of
// fixed-window counters and an exponential backoff schedule for repeated
// authentication failures.
//
// The package deliberately knows nothing about HTTP. Middleware decides what
// a key means -- a client IP, an email address -- and this package only
// counts. That split is what lets the whole thing be tested against an
// injected clock with no server, no sleeping, and no network.
package limiter

import "time"

// Result is the outcome of one Allow call.
//
// RetryAfter is meaningful only when Allowed is false, and is the time
// remaining until the caller's window resets. It exists so the middleware can
// set a Retry-After header without reaching back into the store's internals.
type Result struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Store counts requests against fixed windows, keyed by an opaque string.
//
// It is an interface for one concrete reason recorded in SPEC.md §2.1: the
// in-memory implementation is per-process, which is correct while exactly one
// gateway instance runs. When that stops being true, a Redis implementation
// satisfies this interface and nothing above it changes.
//
// Implementations must be safe for concurrent use -- the store is shared by
// every in-flight request.
type Store interface {
	// Allow records an attempt against key and reports whether it may
	// proceed. Callers pass limit and window on every call rather than
	// configuring them per store, so one store can back several policies
	// with different budgets.
	Allow(key string, limit int, window time.Duration) Result
}
