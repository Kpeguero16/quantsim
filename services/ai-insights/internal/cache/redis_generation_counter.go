package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
)

var _ narrative.Counter = (*RedisGenerationCounter)(nil)

// RedisGenerationCounter counts one caller's paid generations per day
// (SPEC.md §2.7).
//
// The key carries the date, so the count resets without anything having to
// sweep it, and the TTL is what actually removes the key.
type RedisGenerationCounter struct {
	client *redis.Client

	// now is injectable so the day-boundary behaviour is testable without
	// waiting for midnight.
	now func() time.Time
}

func NewRedisGenerationCounter(client *redis.Client) *RedisGenerationCounter {
	return &RedisGenerationCounter{client: client, now: func() time.Time { return time.Now().UTC() }}
}

func counterKey(userID string, now time.Time) string {
	return "narrative:count:" + userID + ":" + now.Format("2006-01-02")
}

// Allow increments the day's count and reports whether it stayed within the
// cap.
//
// It increments first and compares afterwards, which is what makes it a
// reservation rather than a check: two concurrent requests cannot both read
// "49" and both proceed. INCR returns the post-increment value, so the
// comparison is against a number no other request can also see.
func (c *RedisGenerationCounter) Allow(ctx context.Context, userID string, cap int) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

	key := counterKey(userID, c.now())

	count, err := c.client.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}

	// Set on every call rather than only on the first. A key that gained a
	// count but never gained an expiry would cap that user forever, and the
	// window where that can happen is exactly a crash between INCR and
	// EXPIRE. Re-setting an existing TTL to the same horizon is harmless.
	//
	// 48 hours rather than 24: the key is already date-scoped, so the TTL is
	// only a sweeper, and one that expires too eagerly would hand a caller a
	// fresh allowance partway through their own day.
	if err := c.client.Expire(ctx, key, 48*time.Hour).Err(); err != nil {
		return false, err
	}

	return count <= int64(cap), nil
}
