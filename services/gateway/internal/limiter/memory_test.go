package limiter_test

import (
	"sync"
	"testing"
	"time"

	"github.com/kpeguero/quantsim/services/gateway/internal/limiter"
)

// fakeClock lets every test drive time explicitly. No test in this package
// sleeps -- a limiter suite that waits on real windows is slow and flaky, and
// the whole point of injecting the clock is that it never has to.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const (
	testLimit  = 3
	testWindow = 15 * time.Minute
)

// Test #1 -- the limit binds. Requests up to the limit pass; the next is
// refused. This is the core contract everything else builds on.
func TestUnderLimitPassesAndNextIsRefused(t *testing.T) {
	clock := newFakeClock()
	store := limiter.NewMemoryStore(clock.Now)

	for i := 1; i <= testLimit; i++ {
		if res := store.Allow("1.2.3.4", testLimit, testWindow); !res.Allowed {
			t.Fatalf("request %d of %d was refused; requests up to the limit must pass", i, testLimit)
		}
	}

	res := store.Allow("1.2.3.4", testLimit, testWindow)
	if res.Allowed {
		t.Error("request 4 was allowed under a limit of 3; the limit does not bind")
	}
	if res.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v on a refusal; a refused caller must be told when to come back", res.RetryAfter)
	}
	if res.RetryAfter > testWindow {
		t.Errorf("RetryAfter = %v, longer than the %v window; a caller would wait longer than necessary", res.RetryAfter, testWindow)
	}
}

// Test #2 -- windows decay on their own. This is what keeps a limiter from
// becoming permanent state: nothing has to reset a counter for a legitimate
// caller to get their budget back.
func TestWindowExpiryRestoresBudget(t *testing.T) {
	clock := newFakeClock()
	store := limiter.NewMemoryStore(clock.Now)

	for i := 0; i < testLimit; i++ {
		store.Allow("1.2.3.4", testLimit, testWindow)
	}
	if store.Allow("1.2.3.4", testLimit, testWindow).Allowed {
		t.Fatal("setup: the limit should be exhausted before advancing the clock")
	}

	clock.Advance(testWindow + time.Second)

	if res := store.Allow("1.2.3.4", testLimit, testWindow); !res.Allowed {
		t.Error("still refused after the window elapsed; budgets must decay without intervention")
	}
}

// Test #3 -- keys are isolated. Exhausting one IP's budget must not spend
// another's, or a single noisy client would lock out everyone.
func TestKeysHaveIndependentBudgets(t *testing.T) {
	clock := newFakeClock()
	store := limiter.NewMemoryStore(clock.Now)

	for i := 0; i < testLimit; i++ {
		store.Allow("1.2.3.4", testLimit, testWindow)
	}
	if store.Allow("1.2.3.4", testLimit, testWindow).Allowed {
		t.Fatal("setup: the first key should be exhausted")
	}

	if res := store.Allow("5.6.7.8", testLimit, testWindow); !res.Allowed {
		t.Error("a second key was refused after the first exhausted its budget; keys must not share counters")
	}
}

// Test #12 -- eviction reclaims entries whose window has passed. Without it a
// distributed attack across many source IPs grows the map without bound,
// turning a defence into a memory-exhaustion vector.
func TestEvictionReclaimsStaleEntries(t *testing.T) {
	clock := newFakeClock()
	store := limiter.NewMemoryStore(clock.Now)

	store.Allow("1.2.3.4", testLimit, testWindow)
	store.Allow("5.6.7.8", testLimit, testWindow)
	if got := store.Len(); got != 2 {
		t.Fatalf("setup: Len() = %d, want 2 tracked keys", got)
	}

	// Still inside the window: nothing is reclaimable yet.
	store.EvictStale()
	if got := store.Len(); got != 2 {
		t.Errorf("Len() = %d after evicting inside the window, want 2; live counters must survive eviction", got)
	}

	clock.Advance(testWindow + time.Second)
	store.EvictStale()
	if got := store.Len(); got != 0 {
		t.Errorf("Len() = %d after the window elapsed, want 0; stale entries must be reclaimed", got)
	}
}

// Eviction must not cost a caller their budget. A key evicted mid-window
// would silently hand an attacker a fresh allowance, which is the same
// failure as having no limiter at all.
func TestEvictionDoesNotResetLiveCounters(t *testing.T) {
	clock := newFakeClock()
	store := limiter.NewMemoryStore(clock.Now)

	for i := 0; i < testLimit; i++ {
		store.Allow("1.2.3.4", testLimit, testWindow)
	}

	store.EvictStale()

	if store.Allow("1.2.3.4", testLimit, testWindow).Allowed {
		t.Error("eviction inside the window restored the budget; an attacker could refresh their allowance by waiting for a sweep")
	}
}

// The store is shared by every in-flight request, so concurrent use is the
// normal case rather than an edge case. Run with -race.
func TestConcurrentAllowIsSafe(t *testing.T) {
	clock := newFakeClock()
	store := limiter.NewMemoryStore(clock.Now)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			store.Allow("1.2.3.4", goroutines, testWindow)
		}()
	}
	wg.Wait()

	// Exactly `goroutines` calls were made against a limit of the same size,
	// so the budget must be spent precisely -- no lost updates.
	if store.Allow("1.2.3.4", goroutines, testWindow).Allowed {
		t.Error("the limit did not bind after exactly as many concurrent calls as the limit; counter updates are being lost")
	}
}
