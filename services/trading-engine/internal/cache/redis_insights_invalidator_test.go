package cache

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Driven against a real Redis rather than a mock, because a mock that
// reimplements the delete it stands in for cannot test the delete -- Step 21's
// finding, where cap-boundary tests drove a mock counter carrying its own copy
// of the comparison and left the real implementation exercised by nothing.
func miniInvalidator(t *testing.T) (*RedisInsightsInvalidator, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	client := NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisInsightsInvalidator(client), srv
}

// The key is written out here rather than built with cachekeys.Insights, and
// that is the point of the test: it asserts that this service deletes the
// string ai-insights actually reads. Building the expectation with the shared
// function would agree with any format change, including one that leaves the
// two services deleting and reading different keys.
func TestInvalidateInsights_DeletesTheKeyAIInsightsReads(t *testing.T) {
	inv, srv := miniInvalidator(t)
	id := uuid.MustParse("2735b945-6107-4aa5-9553-b5fbb4e85b27")
	const key = "insights:2735b945-6107-4aa5-9553-b5fbb4e85b27"

	if err := srv.Set(key, `{"as_of_date":"2026-07-28"}`); err != nil {
		t.Fatalf("seeding %s: %v", key, err)
	}

	if err := inv.InvalidateInsights(context.Background(), id); err != nil {
		t.Fatalf("InvalidateInsights: %v", err)
	}
	if srv.Exists(key) {
		t.Errorf("%s survived the invalidation", key)
	}
}

// Most fills happen with nothing cached, so this is the common case and not an
// edge one. DEL reports 0 without erroring, and the count is of no use to the
// caller -- an implementation that turned "nothing to delete" into an error
// would log on nearly every order.
func TestInvalidateInsights_DeletingAnAbsentKeyIsNotAnError(t *testing.T) {
	inv, _ := miniInvalidator(t)

	if err := inv.InvalidateInsights(context.Background(), uuid.New()); err != nil {
		t.Errorf("InvalidateInsights on a cold cache: %v", err)
	}
}

// One user's fill must not clear another user's report. The key is per-user,
// so this is really a test that the id reaches the key at all: an
// implementation that deleted a constant would pass every other test here.
func TestInvalidateInsights_LeavesOtherUsersReportsAlone(t *testing.T) {
	inv, srv := miniInvalidator(t)
	const other = "insights:6f1e0e5a-1b2c-4d3e-8f90-abcdef012345"

	if err := srv.Set(other, "{}"); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	if err := inv.InvalidateInsights(context.Background(), uuid.New()); err != nil {
		t.Fatalf("InvalidateInsights: %v", err)
	}
	if !srv.Exists(other) {
		t.Error("another user's cached report was deleted")
	}
}

// A Redis that is not there is an error the caller swallows, but it has to BE
// an error -- PlaceOrder logs on a failure and says nothing on a success, so
// an implementation that returned nil here would report a healthy cache while
// every report went stale.
func TestInvalidateInsights_ReportsAnUnreachableRedis(t *testing.T) {
	inv, srv := miniInvalidator(t)
	srv.Close()

	if err := inv.InvalidateInsights(context.Background(), uuid.New()); err == nil {
		t.Error("InvalidateInsights on a dead Redis returned nil")
	}
}
