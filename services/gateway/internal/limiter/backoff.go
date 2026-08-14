package limiter

import (
	"context"
	"sync"
	"time"
)

// BackoffConfig describes the schedule applied to consecutive authentication
// failures for one account.
type BackoffConfig struct {
	// FreeFailures is how many consecutive failures cost nothing. Typing a
	// password wrong a few times is ordinary, and throttling on the first
	// miss would punish users far more often than attackers.
	FreeFailures int

	// BaseDelay is the window opened by the first failure past the free
	// allowance. Each subsequent failure doubles it.
	BaseDelay time.Duration

	// MaxDelay caps the doubling. It doubles as the idle TTL: a key with no
	// failure for this long has its count cleared entirely, which is what
	// keeps backoff from accumulating across unrelated sessions.
	MaxDelay time.Duration
}

// Delay returns how long a key with this many consecutive failures must wait.
// It is a pure function -- no clock, no state, no key -- so the schedule can
// be pinned by a table test on its own.
func Delay(failures int, cfg BackoffConfig) time.Duration {
	excess := failures - cfg.FreeFailures
	if excess <= 0 {
		return 0
	}

	// Double once per failure past the allowance, stopping the moment the cap
	// is reached. Looping rather than shifting by (excess-1) is deliberate:
	// a large failure count would overflow the shift and wrap to a nonsense
	// duration -- potentially a negative one, which would read as "not
	// blocked" and silently disable the control at exactly the point the key
	// is most obviously under attack.
	d := cfg.BaseDelay
	for i := 1; i < excess; i++ {
		if d >= cfg.MaxDelay {
			break
		}
		d *= 2
	}
	if d > cfg.MaxDelay {
		return cfg.MaxDelay
	}
	return d
}

// attempt is one key's consecutive-failure state.
type attempt struct {
	failures    int
	lastFailure time.Time
}

// Backoff tracks consecutive authentication failures per key and reports
// whether a key is currently throttled.
//
// It is a distinct type from MemoryStore rather than a reuse of it because
// the semantics genuinely differ: a Store counts requests inside a fixed
// window, while this is a state machine over *consecutive* failures that a
// single success resets. Expressing one in terms of the other would obscure
// both.
//
// SPEC.md §2.3 records why this is backoff and not a lockout: a hard lockout
// hands anyone who knows an email address a denial-of-service primitive
// against its owner, and needs an unlock path that does not exist. Every
// window here decays on its own.
//
// Safe for concurrent use.
type Backoff struct {
	now func() time.Time
	cfg BackoffConfig

	mu       sync.Mutex
	attempts map[string]*attempt
}

func NewBackoff(now func() time.Time, cfg BackoffConfig) *Backoff {
	return &Backoff{
		now:      now,
		cfg:      cfg,
		attempts: make(map[string]*attempt),
	}
}

// stale reports whether an entry has aged out. Callers must hold b.mu.
func (b *Backoff) stale(a *attempt, now time.Time) bool {
	return now.Sub(a.lastFailure) >= b.cfg.MaxDelay
}

// Attempt asks whether key may try to authenticate, and — when the answer is
// yes — counts the attempt as a failure before returning.
//
// Checking and counting happen under **one** lock acquisition, and the count
// is optimistic: it assumes the attempt will fail and is rolled back by
// Succeed or Undo once the real outcome is known. Both details exist to close
// the same hole.
//
// Splitting this into a read-only check followed by a separate count after
// the backend replies leaves the counter untouched for the whole duration of
// that round-trip, so every request arriving in the meantime sees a clean
// slate. Measured before this changed: 60 concurrent guesses against one
// account, with a threshold of 5, all 60 reached the backend and none were
// refused. Backoff bounded sequential guessing and did nothing about
// concurrent guessing — which is the easy case for the distributed attack it
// exists to stop.
//
// Counting first inverts that. The Nth concurrent request finds the count
// already at N-1, so a burst is bounded by the threshold rather than by how
// fast the backend answers.
func (b *Backoff) Attempt(key string) Result {
	now := b.now()

	b.mu.Lock()
	defer b.mu.Unlock()

	a, ok := b.attempts[key]
	if !ok || b.stale(a, now) {
		// No history, or it has aged out: this attempt starts a fresh count.
		b.attempts[key] = &attempt{failures: 1, lastFailure: now}
		return Result{Allowed: true}
	}

	if delay := Delay(a.failures, b.cfg); delay > 0 {
		if elapsed := now.Sub(a.lastFailure); elapsed < delay {
			// Still inside the window. Deliberately does not extend it --
			// counting refused attempts would let an attacker keep their own
			// victim throttled indefinitely just by continuing to knock.
			return Result{Allowed: false, RetryAfter: delay - elapsed}
		}
	}

	a.failures++
	a.lastFailure = now
	return Result{Allowed: true}
}

// Succeed clears key's failure count after a successful authentication.
func (b *Backoff) Succeed(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.attempts, key)
}

// Undo rolls back the failure Attempt counted optimistically, for outcomes
// that are not a verdict on the credentials.
//
// An upstream 502 says the auth service is unreachable, not that the password
// was wrong. Letting those accumulate would turn a backend outage into an
// account lockout — users throttled out of a service that is already broken.
func (b *Backoff) Undo(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	a, ok := b.attempts[key]
	if !ok {
		return
	}
	a.failures--
	if a.failures <= 0 {
		delete(b.attempts, key)
	}
}

// EvictStale drops entries that have aged out. Entries still inside their
// window are kept: dropping one would clear an active throttle.
func (b *Backoff) EvictStale() {
	now := b.now()

	b.mu.Lock()
	defer b.mu.Unlock()

	for key, a := range b.attempts {
		if b.stale(a, now) {
			delete(b.attempts, key)
		}
	}
}

// Run sweeps aged-out entries every interval until ctx is cancelled. Without
// it the map grows by one entry per distinct email ever submitted, which an
// attacker controls directly.
//
// Intended to be run in its own goroutine.
func (b *Backoff) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.EvictStale()
		}
	}
}

// Len reports how many keys are tracked. For tests and operational logging.
func (b *Backoff) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.attempts)
}
