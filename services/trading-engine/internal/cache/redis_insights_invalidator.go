package cache

import (
	"context"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/kpeguero/quantsim/pkg/cachekeys"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
)

var _ service.InsightsInvalidator = (*RedisInsightsInvalidator)(nil)

// RedisInsightsInvalidator deletes a user's cached portfolio report when a
// fill makes it stale (SPEC.md Step 24 §2.1).
//
// This is the one place trading-engine touches a key it does not own. The
// format comes from pkg/cachekeys rather than a literal here, so a change to
// it breaks both services' builds together instead of quietly leaving this
// service deleting a key nobody reads.
type RedisInsightsInvalidator struct {
	client *redis.Client
}

func NewRedisInsightsInvalidator(client *redis.Client) *RedisInsightsInvalidator {
	return &RedisInsightsInvalidator{client: client}
}

// InvalidateInsights deletes the report cached for userID.
//
// Deleting a key that is not there is a success, not a miss: most fills happen
// with no cached report, and DEL reports 0 without erroring. The caller has no
// use for the count, so it is dropped.
func (i *RedisInsightsInvalidator) InvalidateInsights(ctx context.Context, userID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

	return i.client.Del(ctx, cachekeys.Insights(userID)).Err()
}
