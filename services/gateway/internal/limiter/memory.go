package limiter

import (
	"context"
	"hash/fnv"
	"sync"
	"time"
)

// shardCount is the number of independently locked maps the store is split
// across. Sharding matters here because eviction walks entire maps: with a
// single lock, a sweep would block every in-flight request for its duration.
// Sixteen is enough that a sweep contends with roughly a sixteenth of
// traffic, and small enough to stay cheap to construct.
const shardCount = 16

// counter is one key's fixed window. window is stored per entry, not per
// store, so a single store can back both the per-IP and per-account policies
// and eviction can still tell when any given entry went stale.
type counter struct {
	count       int
	windowStart time.Time
	window      time.Duration
}

type shard struct {
	mu      sync.Mutex
	entries map[string]*counter
}

// MemoryStore is an in-process Store. See SPEC.md §2.1 for why the gateway
// counts in memory rather than in Redis: it adds no module dependency, costs
// no network round-trip on the authentication path, and -- unlike a shared
// store -- cannot be unavailable, so there is no fail-open/fail-closed
// question to get wrong.
//
// The trade is that counters are per-process. Two gateway instances would
// each hold their own, doubling the effective limit. That is correct today
// (one instance, and one under Phase 4's single-EC2 plan) and is recorded in
// docs/deferred-tuning.md with its trigger.
type MemoryStore struct {
	now    func() time.Time
	shards [shardCount]*shard
}

// NewMemoryStore returns a store that reads time through now. Production
// passes time.Now; tests pass a fake clock, which is what keeps this
// package's suite free of sleeps.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	s := &MemoryStore{now: now}
	for i := range s.shards {
		s.shards[i] = &shard{entries: make(map[string]*counter)}
	}
	return s
}

func (s *MemoryStore) shardFor(key string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key)) // hash.Hash.Write never returns an error
	return s.shards[h.Sum32()%shardCount]
}

// Allow implements Store.
//
// The count is checked *before* it is incremented, so a key that is already
// over its limit does not keep climbing. Under a sustained attack the counter
// therefore rests at the limit rather than growing with every rejected
// attempt, which keeps the number meaningful and the arithmetic bounded.
func (s *MemoryStore) Allow(key string, limit int, window time.Duration) Result {
	now := s.now()
	sh := s.shardFor(key)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	c, ok := sh.entries[key]
	if !ok || now.Sub(c.windowStart) >= window {
		// No entry, or the previous window has elapsed. Either way this
		// attempt opens a fresh one.
		sh.entries[key] = &counter{count: 1, windowStart: now, window: window}
		return Result{Allowed: true}
	}

	if c.count >= limit {
		return Result{
			Allowed:    false,
			RetryAfter: window - now.Sub(c.windowStart),
		}
	}

	c.count++
	return Result{Allowed: true}
}

// EvictStale drops every entry whose window has elapsed.
//
// Entries inside their window are left alone deliberately: evicting one would
// hand its key a fresh budget mid-window, which is indistinguishable from
// having no limiter at all for anyone who learns the sweep interval.
//
// Exported rather than hidden behind Run so the suite can drive it directly
// instead of waiting on a ticker.
func (s *MemoryStore) EvictStale() {
	now := s.now()
	for _, sh := range s.shards {
		sh.mu.Lock()
		for key, c := range sh.entries {
			if now.Sub(c.windowStart) >= c.window {
				delete(sh.entries, key)
			}
		}
		sh.mu.Unlock()
	}
}

// Len reports how many keys are currently tracked, across all shards. It is
// for tests and operational logging -- nothing in the request path uses it.
func (s *MemoryStore) Len() int {
	n := 0
	for _, sh := range s.shards {
		sh.mu.Lock()
		n += len(sh.entries)
		sh.mu.Unlock()
	}
	return n
}

// Run sweeps stale entries every interval until ctx is cancelled. Without it
// the map grows with every distinct key seen -- and under a distributed
// attack that is one entry per source IP, turning a defence into a
// memory-exhaustion vector.
//
// Intended to be run in its own goroutine.
func (s *MemoryStore) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.EvictStale()
		}
	}
}
