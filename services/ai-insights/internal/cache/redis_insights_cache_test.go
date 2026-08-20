package cache

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// An in-package test, because the thing worth pinning here -- the key -- is
// unexported and is exactly the detail that must not drift.
//
// The repo shares ONE Redis across services and keeps them apart by prefix
// convention alone (.env.example). market-data owns "price:" and "prices:",
// auth owns "revoked:". A collision would not error; it would silently serve
// one service's value to another, which is the worst shape a bug can have.
func TestInsightsKey_IsNamespacedAndCollidesWithNothing(t *testing.T) {
	const userID = "6f1e0e5a-1b2c-4d3e-8f90-abcdef012345"
	got := insightsKey(userID)

	if got != "insights:"+userID {
		t.Fatalf("got %q, want insights:%s", got, userID)
	}
	// A bare user id as a key is the failure this guards: any other service
	// that ever keys on a user id would then read and write the same entry.
	if got == userID {
		t.Fatal("the key is a bare user id, in a Redis shared with three other services")
	}
	for _, taken := range []string{"price:", "prices:", "revoked:"} {
		if strings.HasPrefix(got, taken) {
			t.Errorf("key %q lands in %q, which another service owns", got, taken)
		}
	}
}

// unreachableCache points at a port nothing is listening on, which is the
// "Redis is down" case SPEC §2.8's fail-open rule exists for -- reproduced
// with a real client and a real failure rather than a fake.
func unreachableCache(t *testing.T) *RedisInsightsCache {
	t.Helper()
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1", // reserved; nothing listens here
		DialTimeout: 100 * time.Millisecond,
		MaxRetries:  -1,
	})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisInsightsCache(client)
}

// An unreachable Redis is an ERROR, not a miss. The distinction is the whole
// reason Get returns both: the service logs one and stays silent about the
// other, so reporting an outage as a miss would make a Redis that is down for
// hours completely invisible.
func TestRedisInsightsCache_AnUnreachableServerIsAnErrorNotAMiss(t *testing.T) {
	_, found, err := unreachableCache(t).Get(context.Background(), "user")

	if err == nil {
		t.Fatal("an unreachable Redis reported success")
	}
	if found {
		t.Error("an unreachable Redis reported a cache hit")
	}
}

// And a write to an unreachable Redis errors rather than blocking forever or
// pretending to have stored something.
func TestRedisInsightsCache_AWriteToAnUnreachableServerErrors(t *testing.T) {
	err := unreachableCache(t).Set(context.Background(), "user", service.PortfolioInsights{}, time.Minute)

	if err == nil {
		t.Fatal("a write to an unreachable Redis reported success")
	}
}
