// Package cache holds the AI insights service's Redis-backed report cache.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/kpeguero/quantsim/pkg/cachekeys"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

var _ service.InsightsCache = (*RedisInsightsCache)(nil)

// RedisInsightsCache stores rendered reports at insights:{user_id}
// (SPEC.md §2.8).
//
// The key format lives in pkg/cachekeys because trading-engine deletes this
// entry when a fill makes it stale (Step 24 §2.4). It is the one key in this
// service another service names, so it cannot be a literal in either of them.
type RedisInsightsCache struct {
	client *redis.Client
}

func NewRedisInsightsCache(client *redis.Client) *RedisInsightsCache {
	return &RedisInsightsCache{client: client}
}

// key parses the caller's user id and formats the shared cache key.
//
// The id arrives as a string because that is what service.InsightsCache
// passes, and cachekeys.Insights takes a uuid.UUID so that no caller can turn
// an arbitrary string into an arbitrary Redis key. Parsing here is where those
// two meet.
//
// A parse failure cannot happen through the handlers, which parse the JWT
// subject to a UUID before it ever reaches this package. If it ever does, an
// error is the right answer and a harmless one: this interface documents that
// every method may fail and that no failure is a request failure, so the
// service logs it and computes. That is strictly better than the alternative
// this replaces, where an unparseable id quietly became a key.
func (c *RedisInsightsCache) key(userID string) (string, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("insights cache: user id %q is not a uuid: %w", userID, err)
	}
	return cachekeys.Insights(id), nil
}

// Get returns the stored report, and whether there was one.
//
// A miss is (zero, false, nil) and an error is (zero, false, err). The caller
// treats both as "compute", but they are not the same event: a miss is the
// cache working, and an error is worth a log line.
func (c *RedisInsightsCache) Get(ctx context.Context, userID string) (service.PortfolioInsights, bool, error) {
	// Bounded for the reason redisTimeout documents: this read is where a
	// hung Redis cost the report endpoint 6.05s while failing open perfectly
	// correctly. Fail-open is only useful if it is also fast.
	key, err := c.key(userID)
	if err != nil {
		return service.PortfolioInsights{}, false, err
	}

	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

	data, err := c.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return service.PortfolioInsights{}, false, nil
	}
	if err != nil {
		return service.PortfolioInsights{}, false, err
	}

	var insights service.PortfolioInsights
	if err := json.Unmarshal([]byte(data), &insights); err != nil {
		// Treated as an error rather than a miss so it is logged: an entry
		// that will not decode is a shape change between deploys, and it will
		// keep happening for every user until the old entries expire.
		return service.PortfolioInsights{}, false, fmt.Errorf("unmarshal insights: %w", err)
	}
	return insights, true, nil
}

func (c *RedisInsightsCache) Set(ctx context.Context, userID string, insights service.PortfolioInsights, ttl time.Duration) error {
	key, err := c.key(userID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

	data, err := json.Marshal(insights)
	if err != nil {
		return fmt.Errorf("marshal insights: %w", err)
	}
	return c.client.Set(ctx, key, data, ttl).Err()
}
