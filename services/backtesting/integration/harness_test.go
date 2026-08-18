//go:build integration

// Package integration holds the backtesting service's store-layer tests,
// which run against a real Postgres rather than the in-memory doubles every
// other suite in this service uses.
//
// They are behind the `integration` build tag, so a plain `go test ./...`
// does not compile them at all. Run them with `make test-integration`.
//
// # Why this file is a copy, and the trigger that fires here
//
// Nearly all of it is duplicated from services/auth/integration and
// services/trading-engine/integration, which is where Steps 12 and 14 built
// it. This is the THIRD copy -- docs/TESTING_STRUCTURE.md §6a names "a third
// service needing it" as the trigger to extract this to pkg/testutil/, and
// had predicted market-data would be the third rather than backtesting.
// Extraction is deliberately NOT done in this step: it is cross-cutting
// (touches already-shipped test files in two other services) and belongs in
// its own reviewed change, recorded in docs/deferred-tuning.md.
//
// The guards below are copied INTACT. If any of this is ever simplified, do
// not start there: see assertTestDB.
//
// # No t.Parallel() in this package, ever
//
// Every test shares one database and truncates it between cases. Adding
// t.Parallel() is the reflexive change that would make this suite flake in
// ways that look like real bugs. If parallelism is ever wanted here, it needs
// a database per test, not a flag.
package integration

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testDBName is the ONLY database this harness may ever write to.
//
// This constant is load-bearing in a way most are not. The dev database holds
// real user rows and this harness runs TRUNCATE, so "which database am I
// connected to" is not a detail -- it is the single assumption whose
// violation cannot be undone.
//
// The environment makes that sharper than it looks. POSTGRES_DB is `quantsim`,
// which is EMPTY -- a long-standing decoy -- while DATABASE_URL points at
// `postgres`, which is where the real rows actually live. Both of the names a
// careless harness would reach for are wrong, and one of them is wrong
// destructively. The target is neither.
const testDBName = "quantsim_test"

// ErrUnsafeTarget marks a guard violation, as distinct from an environmental
// problem like Postgres being down.
//
// The distinction decides whether the suite skips or fails, and it matters:
// an unreachable server is expected (Docker is off) and skips, but a DSN
// aimed at the wrong database is a misconfiguration that must fail loudly.
// Letting that skip would make "I pointed the harness at the dev database"
// produce the same green `ok` as "Docker is not running" -- which is the
// silently-skipping suite this design is otherwise built to avoid.
var ErrUnsafeTarget = errors.New("unsafe target database")

// ErrPostgresUnavailable marks the ONE condition that may skip the suite:
// there is no server to talk to, which is the ordinary state of a laptop with
// Docker stopped.
//
// Everything else -- a failed migration, a guard violation, a bad DSN -- is a
// real problem and fails the run. Getting this backwards is not hypothetical:
// the first version of this harness skipped on any setup error, and when
// migrations turned out not to be idempotent, the whole suite reported a
// green `ok` while executing no tests at all. A harness that cannot tell
// "nothing to test against" from "the harness is broken" protects nothing.
var ErrPostgresUnavailable = errors.New("postgres unavailable")

// protectedDatabases may never be written to by this suite, whatever
// testDBName happens to say.
//
// This list exists because of a hole found reviewing the first version: every
// guard compared the target against testDBName, so all of them defended
// against a wrong *DSN* and none against a wrong *constant*. Editing
// testDBName to "postgres" would have satisfied every check while the harness
// dropped and truncated the database holding real users -- and the call sites
// that looked most protective were the emptiest, since assertTestDB(testDBName)
// reduces to comparing a constant with itself.
//
// Checking an absolute list first makes the constant itself subject to the
// guard rather than the yardstick for it.
var protectedDatabases = []string{
	"postgres",  // the dev database -- where the real rows actually live
	"quantsim",  // empty decoy, and the value of POSTGRES_DB
	"template0", // system
	"template1", // system
}

// assertTestDB is the guard, and it fails closed twice over: an absolute
// denylist first, then an exact match against testDBName.
//
// It is called on every path that could write: when the DSN is derived, before
// the DROP, after the pool connects, and again immediately before every
// TRUNCATE. See truncateAll for why the last one exists.
func assertTestDB(name string) error {
	for _, protected := range protectedDatabases {
		if name == protected {
			return fmt.Errorf(
				"%w: %q is a protected database and must never be written to by tests",
				ErrUnsafeTarget, name)
		}
	}
	if name != testDBName {
		return fmt.Errorf(
			"%w: refusing to run integration tests against database %q; only %q is permitted",
			ErrUnsafeTarget, name, testDBName)
	}
	return nil
}

// repoRoot walks up from the working directory to the directory holding
// go.work. Migrations and .env are then found by structure rather than by a
// relative path, which would silently break if this package ever moves.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.work not found in any parent of the working directory")
		}
		dir = parent
	}
}

// dotenv reads KEY=VALUE pairs from the repo-root .env WITHOUT exporting them
// into the process environment. Exporting would mean other tests in this
// binary observe a different environment depending on whether .env happens to
// exist, which is the kind of action at a distance that costs an afternoon.
func dotenv(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

// resolveDSNs returns (adminDSN, testDSN). The admin DSN points at the
// `postgres` maintenance database, because CREATE DATABASE cannot run from
// inside the database it is creating.
//
// Reading .env as a last resort is not belt-and-braces, it is load-bearing:
// `make migrate-up` only works because the Makefile does `-include .env` and
// `export`. Running `go test -tags=integration ./integration/...` by hand from
// a plain shell would otherwise always skip -- and a harness that silently
// skips forever is worse than no harness, because it looks like it passes.
func resolveDSNs() (adminDSN, testDSN string, err error) {
	explicit := os.Getenv("TEST_DATABASE_URL")

	raw := explicit
	if raw == "" {
		raw = os.Getenv("DATABASE_URL")
	}
	if raw == "" {
		root, rootErr := repoRoot()
		if rootErr != nil {
			return "", "", rootErr
		}
		raw = dotenv(filepath.Join(root, ".env"))["DATABASE_URL"]
	}
	if raw == "" {
		return "", "", fmt.Errorf("no TEST_DATABASE_URL or DATABASE_URL set, and none found in .env")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parsing database URL: %w", err)
	}

	testU := *u
	if explicit == "" {
		// Derived, never inherited. DATABASE_URL points at the dev database,
		// so the path component is replaced rather than trusted.
		testU.Path = "/" + testDBName
	}
	// Guard 1. Note this also applies to TEST_DATABASE_URL: that override
	// exists to point at a different host or user, not a different database
	// name. If a second name is ever wanted, relax this to a suffix check --
	// do not remove it.
	if err := assertTestDB(strings.TrimPrefix(testU.Path, "/")); err != nil {
		return "", "", err
	}

	adminU := *u
	adminU.Path = "/postgres"
	return adminU.String(), testU.String(), nil
}

// ensureTestDatabase drops and recreates testDBName, so every run starts from
// an empty database.
//
// Recreating rather than reusing is what makes migrations idempotent without
// any version tracking: they always run exactly once, against nothing. The
// first version of this harness merely created the database when absent, and
// the second run then failed on `CREATE TABLE users` with 42P07 -- which,
// combined with skipping on any setup error, produced a green `ok` that ran
// no tests.
//
// It also preserves the property this approach is worth having for: the
// schema is rebuilt from 001 every single run, so the suite continuously
// proves the migration chain applies cleanly to an empty cluster. The dev
// database, migrated incrementally over months, has never demonstrated that.
//
// DROP DATABASE is obviously destructive, which is why it sits behind
// assertTestDB and why that guard fails closed.
func ensureTestDatabase(ctx context.Context, adminDSN string) error {
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer admin.Close()

	// Ping rather than waiting for the first query, so "Docker is off" is
	// reported as an unreachable server -- the one skippable condition --
	// rather than as a confusing query error.
	if err := admin.Ping(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrPostgresUnavailable, err)
	}

	// Checked again before the single most destructive statement in the repo,
	// rather than trusting that a caller checked earlier.
	//
	// This call is only meaningful because assertTestDB consults an absolute
	// denylist before comparing against testDBName. Without that it would be
	// `testDBName != testDBName` -- a check that reads as protective and can
	// never fire, which is worse than no check because it gets believed.
	if err := assertTestDB(testDBName); err != nil {
		return err
	}

	// DROP/CREATE DATABASE take no bind parameters, hence the interpolation.
	// testDBName is a constant, never user input. If that ever changes, use
	// pgx.Identifier to quote it.
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+testDBName+` WITH (FORCE)`); err != nil {
		return fmt.Errorf("dropping %s: %w", testDBName, err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+testDBName); err != nil {
		return fmt.Errorf("creating %s: %w", testDBName, err)
	}
	return nil
}

// applyMigrations brings the test database up to the current schema by running
// every *.up.sql in filename order.
//
// This deliberately does not use golang-migrate as a library. Every migration
// in this repo is plain SQL with no migrate directives (no `-- no-transaction`,
// no CONCURRENTLY), and the test database is created once and truncated
// between tests -- so version tracking and dirty-state recovery would buy
// nothing, while the dependency would land in a production module's go.mod.
func applyMigrations(ctx context.Context, pool *pgxpool.Pool, root string) error {
	pattern := filepath.Join(root, "infra", "migrations", "*.up.sql")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no migrations found at %s", pattern)
	}
	sort.Strings(files)

	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		// Name the migration in the error. "syntax error at or near" with no
		// filename means reading all seven to find the culprit.
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("applying %s: %w", filepath.Base(f), err)
		}
	}
	return nil
}

// truncateAll empties the schema between tests.
//
// Guard 3 lives here. This is the statement that destroys data, so it asks the
// *server* which database it is about to empty rather than trusting the DSN
// parsed at startup or a boolean cached from it. The redundancy with guards 1
// and 2 is the point: they check different things at different times, and the
// cost of the check is one round trip against the cost of being wrong.
//
// Truncating BEFORE each test rather than after leaves a failing test's rows
// in place for inspection with psql.
func truncateAll(tb testing.TB, ctx context.Context, pool *pgxpool.Pool) {
	tb.Helper()

	var current string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&current); err != nil {
		tb.Fatalf("checking current database before truncate: %v", err)
	}
	if err := assertTestDB(current); err != nil {
		tb.Fatal(err)
	}

	// CASCADE follows the FK chain from users into backtests, and onward into
	// backtest_trades. schema_migrations is untouched.
	if _, err := pool.Exec(ctx, `TRUNCATE TABLE users CASCADE`); err != nil {
		tb.Fatalf("truncating: %v", err)
	}
}

// seedUser creates a user directly, bypassing the auth service entirely --
// this module does not import auth, and an integration suite that needed
// another service's registration path to run would be testing two things at
// once. backtests.user_id references users(id) directly (not accounts, which
// this service has no reason to touch).
func seedUser(tb testing.TB, ctx context.Context, pool *pgxpool.Pool) (userID uuid.UUID) {
	tb.Helper()

	suffix := uuid.NewString()
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, username, password_hash) VALUES ($1, $2, $3) RETURNING id`,
		"backtester-"+suffix+"@example.test", "backtester-"+suffix, "not-a-real-hash").Scan(&userID)
	if err != nil {
		tb.Fatalf("seeding user: %v", err)
	}
	return userID
}

// numeric reads a money column as text and parses it, rather than scanning
// into a float64.
//
// The columns are NUMERIC(20,4) and Postgres is the authority on what was
// actually stored. Scanning straight into a float64 would let a value that
// lost precision on the way in come back looking exactly like the number the
// test expected -- the assertion would then be checking Go's arithmetic
// against itself.
func numeric(tb testing.TB, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) float64 {
	tb.Helper()

	var raw string
	if err := pool.QueryRow(ctx, query, args...).Scan(&raw); err != nil {
		tb.Fatalf("reading numeric (%s): %v", query, err)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		tb.Fatalf("parsing numeric %q: %v", raw, err)
	}
	return v
}
