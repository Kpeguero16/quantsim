package cache

import (
	"github.com/redis/go-redis/v9"
)

// NewClient builds the Redis client both caches in this package expect.
//
// It exists because of one non-obvious default: go-redis v9's
// ContextTimeoutEnabled is FALSE unless set, and while it is false the client
// ignores context deadlines entirely and uses its own ReadTimeout instead. A
// context.WithTimeout wrapped around a Redis call therefore does nothing at
// all -- the code reads as bounded, compiles, passes review, and waits the
// full default anyway. That is exactly how GET /insights/portfolio came to
// take 6.05s against a hung Redis while failing open perfectly correctly.
//
// Configuring it here rather than at each call site means a caller cannot get
// this wrong by constructing a client the ordinary way.
func NewClient(opts *redis.Options) *redis.Client {
	opts.ContextTimeoutEnabled = true

	// A second, independent bound. The context deadline is the one that
	// matters per call; these cap anything that reaches the client without
	// one, so a future caller that forgets is still bounded.
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
