package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// The key format itself now lives in pkg/cachekeys and is tested there, because
// trading-engine deletes this entry and so produces the same string (Step 24
// §2.4). What is left here is the join between them: this package takes a
// string from service.InsightsCache and cachekeys.Insights takes a uuid.UUID.
func TestKey_IsTheSharedKeyForAValidUUID(t *testing.T) {
	const userID = "6f1e0e5a-1b2c-4d3e-8f90-abcdef012345"
	c := &RedisInsightsCache{}

	got, err := c.key(userID)
	if err != nil {
		t.Fatalf("key(%q): %v", userID, err)
	}
	// Written out rather than built with cachekeys.Insights: this assertion
	// exists to catch that function changing under this service.
	if want := "insights:" + userID; got != want {
		t.Errorf("key = %q, want %q", got, want)
	}
}

// Unreachable through the handlers, which parse the subject to a UUID first.
// It is an error rather than a key because this interface documents that any
// method may fail and that no failure is a request failure -- so the service
// logs it and computes, which beats writing an attacker-shaped string into a
// Redis kept safe by prefix convention.
func TestKey_RefusesAUserIDThatIsNotAUUID(t *testing.T) {
	c := &RedisInsightsCache{}

	for _, id := range []string{"", "not-a-uuid", "revoked:someone-elses-key"} {
		if got, err := c.key(id); err == nil {
			t.Errorf("key(%q) = %q, want an error", id, got)
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
