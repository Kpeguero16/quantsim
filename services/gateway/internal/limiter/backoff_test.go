package limiter_test

import (
	"sync"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/gateway/internal/limiter"
)

func testBackoffConfig() limiter.BackoffConfig {
	return limiter.BackoffConfig{
		FreeFailures: 4,
		BaseDelay:    time.Minute,
		MaxDelay:     15 * time.Minute,
	}
}

// Test #6 -- the schedule itself, as a pure table. Kept separate from the
// tracker so the arithmetic can be pinned without any state, clock, or key.
func TestDelaySchedule(t *testing.T) {
	cfg := testBackoffConfig()

	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 0},
		{1, 0},
		{4, 0}, // the last free failure
		{5, 1 * time.Minute},
		{6, 2 * time.Minute},
		{7, 4 * time.Minute},
		{8, 8 * time.Minute},
		{9, 15 * time.Minute},  // 16m would exceed the ceiling
		{10, 15 * time.Minute}, // and it stays there
		{40, 15 * time.Minute}, // a shift this large would overflow if computed naively
	}

	for _, tc := range cases {
		if got := limiter.Delay(tc.failures, cfg); got != tc.want {
			t.Errorf("Delay(%d) = %v, want %v", tc.failures, got, tc.want)
		}
	}
}

// Test #5 -- the free allowance, then the throttle.
//
// Read SPEC.md §2.2 precisely: "failures 1-4 pass freely; the 5th failure
// opens a 1-minute window." The window opens once the 5th failure has been
// *recorded*, so all five attempts are allowed to proceed and the sixth is
// the first one refused. The other reading -- refusing the 5th attempt --
// is self-contradictory, because then a 5th failure could never occur.
func TestFifthFailureOpensTheFirstWindow(t *testing.T) {
	clock := newFakeClock()
	b := limiter.NewBackoff(clock.Now, testBackoffConfig())

	for i := 1; i <= 5; i++ {
		if res := b.Attempt("user@example.test"); !res.Allowed {
			t.Fatalf("blocked before failure %d was recorded; the first 4 failures cost nothing", i)
		}
	}

	res := b.Attempt("user@example.test")
	if res.Allowed {
		t.Error("still allowed after 5 failures; the 5th must open a window")
	}
	if res.RetryAfter != time.Minute {
		t.Errorf("RetryAfter = %v after 5 failures, want 1m", res.RetryAfter)
	}
}

// Test #7 -- a success wipes the slate. Someone who mistypes twice and then
// gets it right must not be carrying those failures around.
func TestSuccessResetsFailureCount(t *testing.T) {
	clock := newFakeClock()
	b := limiter.NewBackoff(clock.Now, testBackoffConfig())

	for i := 0; i < 4; i++ {
		b.Attempt("user@example.test")
	}
	b.Succeed("user@example.test")

	// The slate is clean, so the full allowance must be available again.
	for i := 1; i <= 5; i++ {
		if res := b.Attempt("user@example.test"); !res.Allowed {
			t.Fatalf("blocked before failure %d following a success; Succeed must clear the count", i)
		}
	}
	if b.Attempt("user@example.test").Allowed {
		t.Error("the 5th failure after a reset should open a window again")
	}
}

// The block must lift on its own. This is the property that makes backoff
// acceptable where a hard lockout was rejected (SPEC.md §2.3): an attacker
// can slow a known victim down, but cannot keep them out.
func TestBlockExpiresWithoutIntervention(t *testing.T) {
	clock := newFakeClock()
	b := limiter.NewBackoff(clock.Now, testBackoffConfig())

	for i := 0; i < 5; i++ {
		b.Attempt("user@example.test")
	}
	if b.Attempt("user@example.test").Allowed {
		t.Fatal("setup: 5 failures should be blocking")
	}

	clock.Advance(time.Minute + time.Second)

	if !b.Attempt("user@example.test").Allowed {
		t.Error("still blocked after the delay elapsed; backoff must decay without any admin action")
	}
}

// An idle gap clears accumulated failures entirely, so a user who fails a few
// times today is not one mistake away from a throttle next week.
func TestFailureCountDecaysWhenIdle(t *testing.T) {
	clock := newFakeClock()
	cfg := testBackoffConfig()
	b := limiter.NewBackoff(clock.Now, cfg)

	for i := 0; i < 4; i++ {
		b.Attempt("user@example.test")
	}

	clock.Advance(cfg.MaxDelay + time.Second)

	// Those 4 failures have aged out, so the next 4 must be free again.
	for i := 1; i <= 4; i++ {
		if res := b.Attempt("user@example.test"); !res.Allowed {
			t.Fatalf("blocked on fresh attempt %d; stale failures must not carry over", i)
		}
	}
}

// Keys are independent: throttling one account must not touch another.
func TestBackoffKeysAreIndependent(t *testing.T) {
	clock := newFakeClock()
	b := limiter.NewBackoff(clock.Now, testBackoffConfig())

	for i := 0; i < 5; i++ {
		b.Attempt("victim@example.test")
	}
	if b.Attempt("victim@example.test").Allowed {
		t.Fatal("setup: the first account should be blocked")
	}

	if !b.Attempt("bystander@example.test").Allowed {
		t.Error("a second account was blocked by the first's failures; accounts must not share counters")
	}
}

func TestBackoffEvictionReclaimsStaleEntries(t *testing.T) {
	clock := newFakeClock()
	cfg := testBackoffConfig()
	b := limiter.NewBackoff(clock.Now, cfg)

	b.Attempt("a@example.test")
	b.Attempt("b@example.test")
	if got := b.Len(); got != 2 {
		t.Fatalf("setup: Len() = %d, want 2", got)
	}

	b.EvictStale()
	if got := b.Len(); got != 2 {
		t.Errorf("Len() = %d after evicting fresh entries, want 2", got)
	}

	clock.Advance(cfg.MaxDelay + time.Second)
	b.EvictStale()
	if got := b.Len(); got != 0 {
		t.Errorf("Len() = %d after the entries aged out, want 0", got)
	}
}

// Undo rolls back the optimistic count for outcomes that say nothing about
// the credentials, so a downed auth service cannot throttle its own users.
func TestUndoRollsBackTheOptimisticCount(t *testing.T) {
	clock := newFakeClock()
	b := limiter.NewBackoff(clock.Now, testBackoffConfig())

	// Ten attempts that all end inconclusively, e.g. a 502 from upstream.
	for i := 1; i <= 10; i++ {
		if res := b.Attempt("user@example.test"); !res.Allowed {
			t.Fatalf("blocked on attempt %d; outcomes that were rolled back must not accumulate", i)
		}
		b.Undo("user@example.test")
	}

	if got := b.Len(); got != 0 {
		t.Errorf("Len() = %d after every attempt was undone, want 0", got)
	}
}

// THE concurrency test.
//
// Before Attempt existed, checking and counting were separate steps with the
// backend round-trip between them, so the counter stayed clean for the whole
// duration of that call and every request arriving meanwhile saw a free slate.
// Measured through the full gateway: 60 concurrent guesses against one
// account at a threshold of 5 -- all 60 reached the backend, none refused.
//
// Backoff bounded sequential guessing and did nothing about concurrent
// guessing, which is the easy case for the distributed attack it exists to
// stop. Counting under the same lock as the check is what fixes it.
func TestConcurrentAttemptsAreBoundedByTheThreshold(t *testing.T) {
	clock := newFakeClock()
	cfg := testBackoffConfig() // 4 free, so the 5th attempt is the last allowed
	b := limiter.NewBackoff(clock.Now, cfg)

	const burst = 200
	var wg sync.WaitGroup
	allowed := make([]bool, burst)
	wg.Add(burst)
	for i := 0; i < burst; i++ {
		go func(i int) {
			defer wg.Done()
			allowed[i] = b.Attempt("victim@example.test").Allowed
		}(i)
	}
	wg.Wait()

	n := 0
	for _, ok := range allowed {
		if ok {
			n++
		}
	}

	// Exactly the allowance gets through: 4 free failures plus the one that
	// opens the window. Anything more means the burst outran the counter.
	if want := cfg.FreeFailures + 1; n != want {
		t.Errorf("%d of %d concurrent attempts were allowed, want exactly %d -- a burst must not outrun the counter",
			n, burst, want)
	}
}
