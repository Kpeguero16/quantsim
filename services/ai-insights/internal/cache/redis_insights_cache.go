// Package cache holds the AI insights service's Redis-backed report cache.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

var _ service.InsightsCache = (*RedisInsightsCache)(nil)

// RedisInsightsCache stores rendered reports at insights:{user_id}
// (SPEC.md §2.8).
//
// The "insights:" prefix continues the namespacing convention .env.example
// documents and every other user of this Redis follows -- market-data's
// "price:" and "prices:", auth's "revoked:". Sharing one Redis is only safe
// because of that convention, and a bare user id as a key would collide with
// anything else that ever keys on one.
type RedisInsightsCache struct {
	client *redis.Client
}

func NewRedisInsightsCache(client *redis.Client) *RedisInsightsCache {
	return &RedisInsightsCache{client: client}
}

func insightsKey(userID string) string {
	return "insights:" + userID
}

// Get returns the stored report, and whether there was one.
//
// A miss is (zero, false, nil) and an error is (zero, false, err). The caller
// treats both as "compute", but they are not the same event: a miss is the
// cache working, and an error is worth a log line.
func (c *RedisInsightsCache) Get(ctx context.Context, userID string) (service.PortfolioInsights, bool, error) {
	data, err := c.client.Get(ctx, insightsKey(userID)).Result()
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
	data, err := json.Marshal(insights)
	if err != nil {
		return fmt.Errorf("marshal insights: %w", err)
	}
	return c.client.Set(ctx, insightsKey(userID), data, ttl).Err()
}
