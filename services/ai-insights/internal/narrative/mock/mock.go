// Package mock provides a Generator double so the narrative suite never
// reaches the network.
//
// The one thing it models carefully is the call count. A retry loop that
// silently runs a third time still returns the right answer and is still a
// bug, so every test here asserts on how many drafts were asked for, not only
// on what came back.
package mock

import (
	"context"
	"sync"
	"time"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
)

var _ narrative.Generator = (*Generator)(nil)

// Generator answers from a queue of drafts, one per call, and records the
// prompts it was given.
type Generator struct {
	// Drafts are returned in order. Running past the end is itself a failure
	// worth seeing, so it returns ErrNoMoreDrafts rather than an empty string.
	Drafts []string

	// Err, when set, is returned for every call regardless of Drafts. This
	// drives the "a generator failure is not a rejected draft" path.
	Err error

	mu      sync.Mutex
	Prompts []narrative.Prompt
}

func (g *Generator) Draft(_ context.Context, p narrative.Prompt) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.Prompts = append(g.Prompts, p)
	if g.Err != nil {
		return "", g.Err
	}
	i := len(g.Prompts) - 1
	if i >= len(g.Drafts) {
		return "", ErrNoMoreDrafts
	}
	return g.Drafts[i], nil
}

// Calls is how many drafts were asked for.
func (g *Generator) Calls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.Prompts)
}

type noMoreDrafts struct{}

func (noMoreDrafts) Error() string { return "mock: asked for more drafts than were queued" }

// ErrNoMoreDrafts marks a test that asked for more drafts than it queued,
// which almost always means the code under test looped further than intended.
var ErrNoMoreDrafts error = noMoreDrafts{}

var _ narrative.Cache = (*Cache)(nil)

// Cache is an in-memory narrative cache that records what it was asked for,
// so a test can tell a hit from a miss from a write that never happened.
type Cache struct {
	mu      sync.Mutex
	entries map[string]map[string]string

	// GetErr and SetErr drive the fail-open paths: a cache that is down must
	// cost a recomputation and nothing else.
	GetErr error
	SetErr error

	Gets []string
	Sets []string
}

func (c *Cache) Get(_ context.Context, userID, reportHash string) (map[string]string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Gets = append(c.Gets, userID+":"+reportHash)
	if c.GetErr != nil {
		return nil, false, c.GetErr
	}
	sections, ok := c.entries[userID+":"+reportHash]
	return sections, ok, nil
}

func (c *Cache) Set(_ context.Context, userID, reportHash string, sections map[string]string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Sets = append(c.Sets, userID+":"+reportHash)
	if c.SetErr != nil {
		return c.SetErr
	}
	if c.entries == nil {
		c.entries = map[string]map[string]string{}
	}
	c.entries[userID+":"+reportHash] = sections
	return nil
}

// Seed stores an entry directly, as a prior generation would have.
func (c *Cache) Seed(userID, reportHash string, sections map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]map[string]string{}
	}
	c.entries[userID+":"+reportHash] = sections
}

// SetCount is how many writes were attempted.
func (c *Cache) SetCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.Sets)
}

var _ narrative.Counter = (*Counter)(nil)

// Counter is an in-memory generation counter.
type Counter struct {
	mu sync.Mutex

	// Count is how many generations have already been reserved.
	Count int

	// Err, when set, is what a Redis outage looks like here.
	Err error

	Calls int
}

func (c *Counter) Allow(_ context.Context, _ string, cap int) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Calls++
	if c.Err != nil {
		return false, c.Err
	}
	c.Count++
	return c.Count <= cap, nil
}
