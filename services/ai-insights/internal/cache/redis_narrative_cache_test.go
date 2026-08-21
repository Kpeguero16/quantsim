package cache

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// In-package, like the report cache's key test, because the key is the detail
// that must not drift and it is unexported.
func TestNarrativeKey_IsNamespacedAndCarriesTheReportHash(t *testing.T) {
	const userID = "6f1e0e5a-1b2c-4d3e-8f90-abcdef012345"
	got := narrativeKey(userID, "a91f3c0011223344")

	if got != "narrative:"+userID+":a91f3c0011223344" {
		t.Fatalf("got %q", got)
	}
	// The report hash has to be IN the key. Without it the entry would be
	// keyed on the user alone and would outlive the figures it describes --
	// which is the whole difference between this cache and the report cache
	// sitting beside it in this package.
	if !strings.Contains(got, "a91f3c0011223344") {
		t.Error("the key does not carry the report hash")
	}
	for _, taken := range []string{"price:", "prices:", "revoked:", "insights:"} {
		if strings.HasPrefix(got, taken) {
			t.Errorf("key %q lands in %q, which another service owns", got, taken)
		}
	}
}

func unreachableNarrativeCache(t *testing.T) *RedisNarrativeCache {
	t.Helper()
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 100 * time.Millisecond,
		MaxRetries:  -1,
	})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisNarrativeCache(client)
}

func TestRedisNarrativeCache_AnUnreachableServerIsAnErrorNotAMiss(t *testing.T) {
	_, found, err := unreachableNarrativeCache(t).Get(context.Background(), "user", "hash")

	if err == nil {
		t.Fatal("an unreachable Redis reported success")
	}
	if found {
		t.Error("an unreachable Redis reported a cache hit")
	}
}

func TestRedisNarrativeCache_AWriteToAnUnreachableServerErrors(t *testing.T) {
	err := unreachableNarrativeCache(t).Set(context.Background(), "user", "hash", map[string]string{"risk": "x"}, time.Minute)

	if err == nil {
		t.Fatal("a write to an unreachable Redis reported success")
	}
}

// hungListener accepts connections and never answers, which is the outage a
// closed port does NOT reproduce: a stopped container refuses in
// microseconds, while a paused or overloaded one leaves the client waiting on
// its own defaults. Checkpoint 0 measured 6.05s of exactly this.
func hungListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Held open, never read from, never written to.
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return ln.Addr().String()
}

// Both caches in this package must bound their own round trip. Fail-open is
// only useful if it is also fast: without this the endpoint waits on the
// client's defaults and a Redis outage becomes a latency outage.
func TestRedisCaches_BoundTheirOwnRoundTripAgainstAHungServer(t *testing.T) {
	addr := hungListener(t)
	newClient := func() *redis.Client {
		// Built through the package's own constructor, which is where the
		// ContextTimeoutEnabled default is corrected -- a client made the
		// ordinary way would ignore the deadline and wait ReadTimeout.
		c := NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}

	// Comfortably above redisTimeout and far below any client default.
	const limit = 3 * time.Second

	t.Run("narrative Get", func(t *testing.T) {
		start := time.Now()
		_, _, err := NewRedisNarrativeCache(newClient()).Get(context.Background(), "user", "hash")
		if elapsed := time.Since(start); elapsed > limit {
			t.Errorf("took %v against a hung Redis, want under %v -- the read is unbounded", elapsed, limit)
		}
		if err == nil {
			t.Error("a hung Redis reported success")
		}
	})

	t.Run("insights Get", func(t *testing.T) {
		start := time.Now()
		_, _, err := NewRedisInsightsCache(newClient()).Get(context.Background(), "user")
		if elapsed := time.Since(start); elapsed > limit {
			t.Errorf("took %v against a hung Redis, want under %v -- this is the 6.05s "+
				"Checkpoint 0 measured on the report endpoint", elapsed, limit)
		}
		if err == nil {
			t.Error("a hung Redis reported success")
		}
	})
}
