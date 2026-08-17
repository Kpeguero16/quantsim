//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/redis/go-redis/v9"
)

// testRedisDB is the ONLY logical Redis database this suite may ever touch.
//
// Nothing else in this app selects DB 15 -- REDIS_URL for both auth and
// market-data defaults to /0 -- so isolation from the dev cache holds by
// construction. That is a materially lower risk than the Postgres harness
// guards against (a wrong Redis DB loses nothing that TTL-expiring keys
// wouldn't have cleaned up anyway), which is why this is one assertion at
// setup rather than the three-guard chain in harness_test.go (SPEC.md Step
// 13, 2.6).
const testRedisDB = 15

// ErrRedisUnavailable marks the one condition that may skip the Redis half
// of this suite: there is no server to talk to. Mirrors ErrPostgresUnavailable
// in harness_test.go, and the same reasoning applies -- everything else about
// Redis setup is a real problem and must fail rather than skip.
var ErrRedisUnavailable = errors.New("redis unavailable")

var (
	testRedisClient *redis.Client

	// redisSkipReason is non-empty when setup could not reach Redis.
	// Independent of skipReason (Postgres) so a Redis-only test can skip
	// without Postgres needing to be up, and vice versa.
	redisSkipReason string
)

// resolveRedisURL mirrors resolveDSNs' precedence for Postgres: an explicit
// override, else the normal env var, else .env read directly (the same
// reason applies -- make migrate-up and make test-integration only work
// because the Makefile does `-include .env` + `export`; a plain `go test`
// invocation has no such export).
func resolveRedisURL() (string, error) {
	explicit := os.Getenv("TEST_REDIS_URL")
	if explicit != "" {
		return explicit, nil
	}

	raw := os.Getenv("REDIS_URL")
	if raw == "" {
		root, err := repoRoot()
		if err != nil {
			return "", err
		}
		raw = dotenv(filepath.Join(root, ".env"))["REDIS_URL"]
	}
	if raw == "" {
		return "", fmt.Errorf("no TEST_REDIS_URL or REDIS_URL set, and none found in .env")
	}

	// Derived, never inherited: REDIS_URL points at DB 0, so the DB index is
	// replaced rather than trusted -- the same "derived, not inherited"
	// reasoning resolveDSNs uses for the database name.
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parsing redis URL: %w", err)
	}
	u.Path = fmt.Sprintf("/%d", testRedisDB)
	return u.String(), nil
}

func setupRedis(ctx context.Context) error {
	redisURL, err := resolveRedisURL()
	if err != nil {
		return err
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("parsing redis URL: %w", err)
	}
	if opts.DB != testRedisDB {
		return fmt.Errorf("refusing to run Redis integration tests against DB %d; only %d is permitted", opts.DB, testRedisDB)
	}

	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return fmt.Errorf("%w: %v", ErrRedisUnavailable, err)
	}

	testRedisClient = client
	return nil
}

// newRedisClient is how every Redis test starts: skip when Redis is
// unavailable, flush DB 15 for a clean slate, return the shared client.
func newRedisClient(t *testing.T) *redis.Client {
	t.Helper()

	if redisSkipReason != "" {
		t.Skip(redisSkipReason)
	}

	ctx := context.Background()
	if err := testRedisClient.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flushing test redis db: %v", err)
	}

	return testRedisClient
}
