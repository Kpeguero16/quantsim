package limiter

import (
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

// Check reports whether key may attempt authentication right now. It does not
// record anything -- Fail and Reset do that, once the outcome is known.
func (b *Backoff) Check(key string) Result {
	now := b.now()

	b.mu.Lock()
	defer b.mu.Unlock()

	a, ok := b.attempts[key]
	if !ok || b.stale(a, now) {
		return Result{Allowed: true}
	}

	delay := Delay(a.failures, b.cfg)
	if delay == 0 {
		return Result{Allowed: true}
	}

	elapsed := now.Sub(a.lastFailure)
	if elapsed >= delay {
		return Result{Allowed: true}
	}
	return Result{Allowed: false, RetryAfter: delay - elapsed}
}

// Fail records one failed attempt for key.
//
// The count resumes from zero if the previous failure has aged out, so
// failures separated by a long idle gap do not compound.
func (b *Backoff) Fail(key string) {
	now := b.now()

	b.mu.Lock()
	defer b.mu.Unlock()

	a, ok := b.attempts[key]
	if !ok || b.stale(a, now) {
		b.attempts[key] = &attempt{failures: 1, lastFailure: now}
		return
	}
	a.failures++
	a.lastFailure = now
}

// Reset clears key's failure count after a successful authentication.
func (b *Backoff) Reset(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.attempts, key)
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

// Len reports how many keys are tracked. For tests and operational logging.
func (b *Backoff) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.attempts)
}
