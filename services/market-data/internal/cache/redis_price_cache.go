package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kpeguero/quantsim/services/market-data/internal/service"
)

// RedisPriceCache implements service.PriceCache over Redis. Keys/channels
// assume symbol is already uppercased/trimmed by the caller (service.LatestPrice
// and the poller both normalize before reaching here).
type RedisPriceCache struct {
	client *redis.Client
}

func NewRedisPriceCache(client *redis.Client) *RedisPriceCache {
	return &RedisPriceCache{client: client}
}

func priceKey(symbol string) string {
	return "price:" + symbol
}

func priceChannel(symbol string) string {
	return "prices:" + symbol
}

func (c *RedisPriceCache) SetPrice(ctx context.Context, symbol string, price service.Price, ttl time.Duration) error {
	data, err := json.Marshal(price)
	if err != nil {
		return fmt.Errorf("marshal price: %w", err)
	}
	return c.client.Set(ctx, priceKey(symbol), data, ttl).Err()
}

func (c *RedisPriceCache) GetPrice(ctx context.Context, symbol string) (service.Price, error) {
	data, err := c.client.Get(ctx, priceKey(symbol)).Result()
	if errors.Is(err, redis.Nil) {
		return service.Price{}, ErrNotFound
	}
	if err != nil {
		return service.Price{}, err
	}

	var price service.Price
	if err := json.Unmarshal([]byte(data), &price); err != nil {
		return service.Price{}, fmt.Errorf("unmarshal price: %w", err)
	}
	return price, nil
}

func (c *RedisPriceCache) PublishPrice(ctx context.Context, symbol string, price service.Price) error {
	data, err := json.Marshal(price)
	if err != nil {
		return fmt.Errorf("marshal price: %w", err)
	}
	return c.client.Publish(ctx, priceChannel(symbol), data).Err()
}
