// Package cache holds trading-engine's Redis client and the one entry it
// invalidates: another service's report cache.
package cache

import (
	"time"

	"github.com/redis/go-redis/v9"
)

// redisTimeout bounds one Redis round trip. It sits on the order path, so it
// is the extra latency a fill can cost when Redis is slow, and the ceiling on
// how long a placed order waits for a cache delete it does not need.
const redisTimeout = 500 * time.Millisecond

// NewClient builds trading-engine's Redis client.
//
// It exists for one non-obvious default: go-redis v9's ContextTimeoutEnabled
// is FALSE unless set, and while it is false the client ignores context
// deadlines entirely and uses its own ReadTimeout instead. A context.WithTimeout
// around a Redis call therefore does nothing -- the code reads as bounded,
// compiles, passes review, and waits the full default anyway. See
// services/ai-insights/internal/cache/client.go for the full account; that is
// where it cost 6.05s on a live endpoint.
//
// This is a deliberate second copy rather than a shared helper. Hoisting it to
// pkg/ would put go-redis in a module whose only dependencies are jwt and
// uuid, and every service importing pkg/auth would inherit it -- including
// backtesting, which uses no Redis. Consolidating all four construction sites
// is its own step, and it has to fix auth and market-data too, neither of
// which sets this today (SPEC.md Step 24 §2.5, §6).
func NewClient(opts *redis.Options) *redis.Client {
	opts.ContextTimeoutEnabled = true

	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = redisTimeout
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = redisTimeout
	}
	if opts.DialTimeout == 0 {
		opts.DialTimeout = redisTimeout
	}
	return redis.NewClient(opts)
}
