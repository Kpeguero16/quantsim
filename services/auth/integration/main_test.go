//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kpeguero/quantsim/services/auth/internal/store"
)

// setupTimeout bounds the whole one-time setup. Kept short so that "Docker is
// off" -- the expected case, not an exceptional one -- costs a few seconds
// rather than a stalled test run.
const setupTimeout = 10 * time.Second

// testTimeout bounds each individual test's database work.
const testTimeout = 15 * time.Second

var (
	testPool *pgxpool.Pool

	// skipReason is non-empty when setup could not reach Postgres. Every test
	// consults it, because t.Skip cannot be called from TestMain.
	skipReason string
)

func TestMain(m *testing.M) {
	if err := setup(); err != nil {
		// A guard violation is a misconfiguration, not an environment
		// problem, and it is the one thing here that must never be shrugged
		// off. Skipping would make "I aimed the harness at the dev database"
		// produce the same green `ok` as "Docker is not running".
		if errors.Is(err, ErrUnsafeTarget) {
			fmt.Fprintf(os.Stderr, "\nFATAL: %v\n\n", err)
			os.Exit(1)
		}

		// Postgres being unreachable, by contrast, is the expected case with
		// Docker stopped and must not fail the run. But it must not be silent
		// either: a suite that skips forever looks exactly like a suite that
		// passes, which is how integration tests quietly stop protecting
		// anything. So the reason goes to stderr, naming the fix.
		skipReason = fmt.Sprintf("auth store integration tests unavailable: %v", err)
		fmt.Fprintf(os.Stderr,
			"\n%s\n  start Postgres with `make docker-up`, or set TEST_DATABASE_URL\n\n",
			skipReason)
	}

	code := m.Run()

	if testPool != nil {
		testPool.Close()
	}
	os.Exit(code)
}

func setup() error {
	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()

	root, err := repoRoot()
	if err != nil {
		return err
	}

	adminDSN, testDSN, err := resolveDSNs() // guard 1
	if err != nil {
		return err
	}
	if err := ensureTestDatabase(ctx, adminDSN); err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		return err
	}

	// Guard 2: the DSN said quantsim_test, but ask the server what it
	// actually connected to before handing this pool to anything that writes.
	var current string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&current); err != nil {
		pool.Close()
		return fmt.Errorf("checking connected database: %w", err)
	}
	if err := assertTestDB(current); err != nil {
		pool.Close()
		return err
	}

	if err := applyMigrations(ctx, pool, root); err != nil {
		pool.Close()
		return err
	}

	testPool = pool
	return nil
}

// newStore is how every test starts: it skips when the database is
// unavailable, gives the test a clean schema, and returns the real store.
func newStore(t *testing.T) (*store.PostgresUserStore, *pgxpool.Pool, context.Context) {
	t.Helper()

	if skipReason != "" {
		t.Skip(skipReason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)

	truncateAll(t, ctx, testPool) // guard 3

	return store.NewPostgresUserStore(testPool), testPool, ctx
}
