package store

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

// RedisTokenStore implements service.RevocationStore over Redis. Keys carry
// a revoked: prefix so they can never collide with services/market-data's
// price:/prices: keys (internal/cache/redis_price_cache.go), even on the
// same Redis instance and logical DB.
type RedisTokenStore struct {
	client *redis.Client
}

func NewRedisTokenStore(client *redis.Client) *RedisTokenStore {
	return &RedisTokenStore{client: client}
}

var _ service.RevocationStore = (*RedisTokenStore)(nil)

func revokedKey(jti string) string {
	return "revoked:" + jti
}

// Revoke writes a key that expires on its own after ttl -- the same moment
// the token itself would have expired naturally, so there is nothing to
// clean up and no unbounded growth.
func (s *RedisTokenStore) Revoke(ctx context.Context, jti string, ttl time.Duration) error {
	return s.client.Set(ctx, revokedKey(jti), "1", ttl).Err()
}

func (s *RedisTokenStore) IsRevoked(ctx context.Context, jti string) (bool, error) {
	_, err := s.client.Get(ctx, revokedKey(jti)).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
