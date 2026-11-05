// Package redisclient builds the Redis client every QuantSim service uses.
package redisclient

import (
	"time"

	"github.com/redis/go-redis/v9"
)

// Timeout bounds one Redis round trip when the caller sets no tighter
// deadline. Every Redis call in this repo is a single-key GET, SET, DEL, INCR
// or PUBLISH against a local instance, so anything past this is an outage, not
// a slow reply. Nothing here uses a blocking command (BLPOP, XREAD, SUBSCRIBE);
// one that does needs its own client, because this bound would cut it off.
const Timeout = 500 * time.Millisecond

// New builds a Redis client that honours context deadlines.
//
// It exists for one non-obvious default: go-redis v9's ContextTimeoutEnabled
// is FALSE unless set, and while it is false the client ignores context
// deadlines entirely and waits its own ReadTimeout instead, 3s by default. A
// context.WithTimeout around a Redis call therefore does nothing. The code
// reads as bounded, compiles, passes review, and waits the full default
// anyway. That is how GET /insights/portfolio came to take 6.05s against a
// hung Redis in Step 21.
//
// The read, write and dial timeouts are a second, independent bound. The
// context deadline is the one that matters per call; these cap a call that
// arrives with no deadline at all, which is every call auth and market-data
// make. Values already set on opts, for example from a REDIS_URL query
// parameter, are kept.
func New(opts *redis.Options) *redis.Client {
	opts.ContextTimeoutEnabled = true

	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = Timeout
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = Timeout
	}
	if opts.DialTimeout == 0 {
		opts.DialTimeout = Timeout
	}
	return redis.NewClient(opts)
}
