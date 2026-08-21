package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
)

var _ narrative.Cache = (*RedisNarrativeCache)(nil)

// NarrativeTTL is how long a narrative outlives its generation (SPEC.md
// §2.10). It is long because the key already carries the report's content
// hash: an entry can only be served while every figure it describes is
// unchanged, so age is not what makes it stale.
const NarrativeTTL = 24 * time.Hour

// redisTimeout bounds one Redis round trip.
//
// It exists because a *stopped* Redis and a *hung* Redis fail completely
// differently, and only the first one is fast. A stopped container refuses the
// connection in microseconds; a paused or overloaded one accepts it and never
// answers, and the client then waits on its own defaults. Checkpoint 0
// measured GET /insights/portfolio at 6.05s against a hung Redis, on the path
// that does not even reach trading-engine -- the cache was failing open
// correctly and taking six seconds to do it. Fail-open is only useful if it is
// also fast.
const redisTimeout = 500 * time.Millisecond

// RedisNarrativeCache stores rendered narratives at
// narrative:{user_id}:{report_hash} (SPEC.md §2.10).
//
// Keying on the report's content rather than on time is what makes the entry
// correct rather than merely fresh: it can only be served while every figure
// it describes is unchanged. The consequence, accepted deliberately, is that
// an unchanged portfolio returns the same three paragraphs word for word --
// which is right, since the same facts should not be described differently on
// a refresh, and will still read as staleness to someone expecting a new take.
type RedisNarrativeCache struct {
	client *redis.Client
}

func NewRedisNarrativeCache(client *redis.Client) *RedisNarrativeCache {
	return &RedisNarrativeCache{client: client}
}

func narrativeKey(userID, reportHash string) string {
	return "narrative:" + userID + ":" + reportHash
}

// Get returns the stored narrative, and whether there was one.
func (c *RedisNarrativeCache) Get(ctx context.Context, userID, reportHash string) (map[string]string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

	data, err := c.client.Get(ctx, narrativeKey(userID, reportHash)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	var sections map[string]string
	if err := json.Unmarshal([]byte(data), &sections); err != nil {
		return nil, false, fmt.Errorf("unmarshal narrative: %w", err)
	}
	return sections, true, nil
}

func (c *RedisNarrativeCache) Set(ctx context.Context, userID, reportHash string, sections map[string]string, ttl time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

	data, err := json.Marshal(sections)
	if err != nil {
		return fmt.Errorf("marshal narrative: %w", err)
	}
	return c.client.Set(ctx, narrativeKey(userID, reportHash), data, ttl).Err()
}
