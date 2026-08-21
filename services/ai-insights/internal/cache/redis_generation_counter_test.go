package cache

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestCounterKey_IsNamespacedAndDateScoped(t *testing.T) {
	const userID = "6f1e0e5a-1b2c-4d3e-8f90-abcdef012345"
	day := time.Date(2026, 8, 20, 23, 59, 0, 0, time.UTC)

	got := counterKey(userID, day)
	if got != "narrative:count:"+userID+":2026-08-20" {
		t.Fatalf("got %q", got)
	}
	// The date in the key is what resets the allowance without anything
	// having to sweep it.
	next := counterKey(userID, day.Add(time.Minute))
	if next == got {
		t.Error("the key does not roll over at midnight; the cap would be cumulative forever")
	}
	// It must not collide with the narrative entries themselves, which share
	// the "narrative:" prefix.
	if !strings.HasPrefix(got, "narrative:count:") {
		t.Errorf("key %q could collide with a stored narrative", got)
	}
}

// An unreachable Redis must REFUSE, not allow. This is the fail-closed rule,
// and it is the opposite of the caches in this same package.
func TestRedisGenerationCounter_AnUnreachableServerRefuses(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 100 * time.Millisecond,
		MaxRetries:  -1,
	})
	t.Cleanup(func() { _ = client.Close() })

	allowed, err := NewRedisGenerationCounter(client).Allow(context.Background(), "user", 50)

	if err == nil {
		t.Fatal("an unreachable Redis reported success")
	}
	if allowed {
		t.Error("an unreachable Redis allowed a paid generation -- this is the fail-open " +
			"bug that would remove the cost ceiling exactly when the cache is also gone")
	}
}

// The tests below run against a real Redis implementation rather than the
// handler's mock counter.
//
// They exist because mutation testing found the gap: the mock reimplements the
// cap comparison, so the handler's boundary tests passed while this file's
// `count <= cap` and its INCR-as-reservation were never exercised at all. A
// double that reimplements the logic it stands in for cannot test it.
func miniCounter(t *testing.T, now time.Time) (*RedisGenerationCounter, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	client := NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	c := NewRedisGenerationCounter(client)
	c.now = func() time.Time { return now }
	return c, srv
}

func TestRedisGenerationCounter_TheCapBoundaryHoldsOnBothSides(t *testing.T) {
	const cap = 3
	c, _ := miniCounter(t, time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))

	for i := 1; i <= cap; i++ {
		allowed, err := c.Allow(context.Background(), "user", cap)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("call %d of %d was refused; the cap is one lower than it says", i, cap)
		}
	}

	allowed, err := c.Allow(context.Background(), "user", cap)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if allowed {
		t.Errorf("call %d was allowed with a cap of %d; the cap means one more than it says", cap+1, cap)
	}
}

// INCR, not GET-then-INCR. The count must be reserved before the model is
// called, and two concurrent requests must not be able to read the same
// number and both proceed.
func TestRedisGenerationCounter_ReservesRatherThanChecks(t *testing.T) {
	c, srv := miniCounter(t, time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))

	if _, err := c.Allow(context.Background(), "user", 50); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	got, err := srv.Get(counterKey("user", time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("the counter stored nothing; a check that does not increment reserves nothing: %v", err)
	}
	if got != "1" {
		t.Errorf("counter = %q after one call, want 1", got)
	}
}

// A key that gained a count but never gained an expiry would cap that user
// forever.
func TestRedisGenerationCounter_TheKeyExpires(t *testing.T) {
	day := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	c, srv := miniCounter(t, day)

	if _, err := c.Allow(context.Background(), "user", 50); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if ttl := srv.TTL(counterKey("user", day)); ttl <= 0 {
		t.Fatalf("the counter key has no TTL (%v); the allowance would never reset", ttl)
	}
}

// A new day is a new allowance, without anything having to sweep the old key.
func TestRedisGenerationCounter_TheAllowanceResetsTheNextDay(t *testing.T) {
	const cap = 2
	day := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	c, _ := miniCounter(t, day)

	for i := 0; i < cap; i++ {
		if _, err := c.Allow(context.Background(), "user", cap); err != nil {
			t.Fatalf("Allow: %v", err)
		}
	}
	if allowed, _ := c.Allow(context.Background(), "user", cap); allowed {
		t.Fatal("fixture is wrong: the user is not at the cap")
	}

	c.now = func() time.Time { return day.Add(24 * time.Hour) }
	allowed, err := c.Allow(context.Background(), "user", cap)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !allowed {
		t.Error("the allowance did not reset on the next day; the cap is cumulative forever")
	}
}

// Each caller has their own allowance.
func TestRedisGenerationCounter_CountsPerUser(t *testing.T) {
	const cap = 1
	c, _ := miniCounter(t, time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))

	if allowed, _ := c.Allow(context.Background(), "user-a", cap); !allowed {
		t.Fatal("the first call for user-a was refused")
	}
	if allowed, _ := c.Allow(context.Background(), "user-b", cap); !allowed {
		t.Error("user-a's usage consumed user-b's allowance")
	}
}
