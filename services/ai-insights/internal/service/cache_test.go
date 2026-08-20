package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service/mock"
)

// cachedFixture is one traded account plus the doubles it runs against, so a
// test can call twice and inspect what each dependency saw.
func cachedFixture(cache service.InsightsCache) (*service.Service, *mock.TradingClient, *mock.HistoryClient) {
	history := &mock.HistoryClient{Bars: withBars(benchmarkBars(1, 2), map[string][]service.Bar{
		"AAPL": bars(1, 2),
	})}
	trading := &mock.TradingClient{
		TradesResult:    []service.Trade{buy("AAPL", 10, 100, 1)},
		PortfolioResult: live(99000, pos("AAPL", 10)),
	}
	return service.NewService(trading, history, cache), trading, history
}

func callTwice(t *testing.T, svc *service.Service) (service.PortfolioInsights, service.PortfolioInsights) {
	t.Helper()
	first, err := svc.PortfolioInsights(context.Background(), testUser, "Bearer tok")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := svc.PortfolioInsights(context.Background(), testUser, "Bearer tok")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	return first, second
}

// THE ACCEPTANCE CRITERION: a second request inside the TTL makes no upstream
// call and returns the SAME computed_at.
//
// Asserted on the mocks' recorded calls rather than on elapsed time -- a
// timing assertion would pass on a machine fast enough to recompute in under a
// millisecond, which is every machine, and would therefore be testing nothing.
func TestCache_ASecondRequestIsServedFromTheCache(t *testing.T) {
	cache := &mock.InsightsCache{}
	svc, trading, history := cachedFixture(cache)

	first, second := callTwice(t, svc)

	if !first.ComputedAt.Equal(second.ComputedAt) {
		t.Errorf("computed_at differs: %v then %v -- the second call recomputed",
			first.ComputedAt, second.ComputedAt)
	}
	if n := len(trading.Authorizations()); n != 2 {
		t.Errorf("made %d trading-engine calls across two requests, want 2 (the first request's)", n)
	}
	if n := len(history.Calls()); n != 3 {
		t.Errorf("fetched %d symbol histories across two requests, want 3 (the first request's)", n)
	}
	if cache.Sets() != 1 {
		t.Errorf("wrote to the cache %d times, want 1 -- a hit must not rewrite", cache.Sets())
	}
}

// The key is the user, so one user's report is never served to another.
func TestCache_IsKeyedByUser(t *testing.T) {
	cache := &mock.InsightsCache{}
	svc, trading, _ := cachedFixture(cache)

	if _, err := svc.PortfolioInsights(context.Background(), testUser, "Bearer tok"); err != nil {
		t.Fatalf("first user: %v", err)
	}
	const otherUser = "11111111-2222-3333-4444-555555555555"
	if _, err := svc.PortfolioInsights(context.Background(), otherUser, "Bearer other"); err != nil {
		t.Fatalf("second user: %v", err)
	}

	if n := len(trading.Authorizations()); n != 4 {
		t.Errorf("made %d trading-engine calls, want 4 -- the second user was served "+
			"the first user's cached report", n)
	}
	keys := cache.Keys()
	if len(keys) != 2 {
		t.Fatalf("cache holds %v, want one entry per user", keys)
	}
}

// FAIL-OPEN, READ SIDE: a cache error computes and returns 200-worthy data.
func TestCache_AReadFailureComputesRatherThanFailing(t *testing.T) {
	cache := &mock.InsightsCache{GetErr: errors.New("redis is down")}
	svc, trading, _ := cachedFixture(cache)

	first, second := callTwice(t, svc)

	if first.Risk.State != service.StateOK || second.Risk.State != service.StateOK {
		t.Fatalf("a cache outage degraded the report: %q then %q",
			first.Risk.State, second.Risk.State)
	}
	// Both requests computed, because neither read succeeded.
	if n := len(trading.Authorizations()); n != 4 {
		t.Errorf("made %d trading-engine calls, want 4 (both requests computed)", n)
	}
	if first.ComputedAt.Equal(second.ComputedAt) {
		t.Error("computed_at is identical; the second call was served from a cache that errors")
	}
}

// FAIL-OPEN, WRITE SIDE: the report already computed is still returned.
func TestCache_AWriteFailureStillReturnsTheReport(t *testing.T) {
	cache := &mock.InsightsCache{SetErr: errors.New("redis is down")}
	svc, _, _ := cachedFixture(cache)

	got, err := svc.PortfolioInsights(context.Background(), testUser, "Bearer tok")
	if err != nil {
		t.Fatalf("a failed cache WRITE lost a report that was already computed: %v", err)
	}
	if got.Risk.State != service.StateOK {
		t.Errorf("risk state: got %q, want ok", got.Risk.State)
	}
	if cache.Sets() != 1 {
		t.Errorf("attempted %d writes, want 1", cache.Sets())
	}
}

// An error is never cached. A momentary upstream blip must not be served for
// the whole TTL after the upstream recovers.
func TestCache_ErrorsAreNotCached(t *testing.T) {
	cache := &mock.InsightsCache{}
	history := &mock.HistoryClient{Bars: withBars(benchmarkBars(1, 2), map[string][]service.Bar{
		"AAPL": bars(1, 2),
	})}
	trading := &mock.TradingClient{
		TradesResult: []service.Trade{buy("AAPL", 10, 100, 1)},
		// The account the trade log describes -- so that once the injected
		// outage clears, the reconciliation guard has something coherent to
		// agree with. Without it the recovered request refuses, and the test
		// would blame the cache for the guard doing its job.
		PortfolioResult: live(99000, pos("AAPL", 10)),
		PortfolioErr:    service.ErrUpstreamUnavailable,
	}
	svc := service.NewService(trading, history, cache)

	if _, err := svc.PortfolioInsights(context.Background(), testUser, "Bearer tok"); err == nil {
		t.Fatal("expected the upstream failure to surface")
	}
	if cache.Sets() != 0 {
		t.Errorf("cached a failed request %d time(s)", cache.Sets())
	}

	// The upstream recovers; the next request must see that immediately.
	trading.PortfolioErr = nil
	got, err := svc.PortfolioInsights(context.Background(), testUser, "Bearer tok")
	if err != nil {
		t.Fatalf("after recovery: %v", err)
	}
	if got.Risk.State != service.StateOK {
		t.Errorf("risk state: got %q -- a cached failure outlived the outage", got.Risk.State)
	}
}

// An insufficient_data report is cached like any other. A brand-new user
// polling a dashboard is exactly the case where the answer is cheap to reuse
// and does not change between polls.
func TestCache_InsufficientDataReportsAreCachedToo(t *testing.T) {
	cache := &mock.InsightsCache{}
	trading := &mock.TradingClient{TradesResult: []service.Trade{}}
	svc := service.NewService(trading, &mock.HistoryClient{}, cache)

	first, second := callTwice(t, svc)

	if first.Risk.State != service.StateInsufficientData {
		t.Fatalf("fixture: got %q", first.Risk.State)
	}
	if !first.ComputedAt.Equal(second.ComputedAt) {
		t.Error("the second call recomputed an answer that cannot have changed")
	}
	if n := len(trading.Authorizations()); n != 1 {
		t.Errorf("made %d trading-engine calls, want 1", n)
	}
}

// A nil cache is a supported configuration, not a crash: this service's only
// stateful dependency is one it can run without (SPEC.md §2.9).
func TestCache_ANilCacheComputesEveryTime(t *testing.T) {
	svc, trading, _ := cachedFixture(nil)

	first, second := callTwice(t, svc)

	if first.Risk.State != service.StateOK || second.Risk.State != service.StateOK {
		t.Fatal("a nil cache broke the report")
	}
	if n := len(trading.Authorizations()); n != 4 {
		t.Errorf("made %d trading-engine calls, want 4 (both requests computed)", n)
	}
}

// The Redis key namespace, asserted at the only level a unit test can reach
// it: the service hands the cache a bare user id, and the Redis
// implementation is what prefixes it. This pins the half the service owns --
// that the id is passed through unmangled and unprefixed, so the prefix lives
// in exactly one place.
func TestCache_TheServicePassesABareUserID(t *testing.T) {
	cache := &mock.InsightsCache{}
	svc, _, _ := cachedFixture(cache)

	if _, err := svc.PortfolioInsights(context.Background(), testUser, "Bearer tok"); err != nil {
		t.Fatalf("PortfolioInsights: %v", err)
	}

	keys := cache.Keys()
	if len(keys) != 1 || keys[0] != testUser {
		t.Fatalf("cache saw %v, want exactly [%s]", keys, testUser)
	}
	if strings.Contains(keys[0], ":") {
		t.Errorf("the service is prefixing the key itself: %q", keys[0])
	}
}
