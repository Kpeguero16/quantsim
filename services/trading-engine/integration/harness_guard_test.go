//go:build integration

package integration

import (
	"strings"
	"testing"
)

// The guard is the only thing standing between this suite and the dev
// database, which holds real user rows. It gets its own test, and the cases
// that matter most are the two names that actually exist on this server:
// `postgres` (where the real rows live) and `quantsim` (the empty decoy).
//
// Both are plausible typos. Neither may ever be accepted.
func TestAssertTestDBRefusesEverythingButTheTestDatabase(t *testing.T) {
	for _, name := range []string{
		"postgres",  // the dev database -- where the real rows live
		"quantsim",  // the empty decoy, and the value of POSTGRES_DB
		"",          // an unparsed or pathless URL
		"template1", // a system database
		"quantsim_tes",
		"quantsim_test2",
		"QUANTSIM_TEST", // Postgres identifiers are case-sensitive when quoted
	} {
		t.Run(name, func(t *testing.T) {
			err := assertTestDB(name)
			if err == nil {
				t.Fatalf("assertTestDB(%q) returned nil -- this guard is what stops the suite truncating a database holding real users", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not name the rejected database %q; the message is what tells you which one was wrong", err, name)
			}
		})
	}

	if err := assertTestDB(testDBName); err != nil {
		t.Errorf("assertTestDB(%q) = %v, want nil -- the harness cannot run at all if its own target is refused", testDBName, err)
	}
}

// The guard must catch a poisoned *constant*, not just a wrong DSN.
//
// The first version of this harness compared every target against testDBName,
// which meant all three guards defended against a bad connection string and
// none against a bad constant: editing testDBName to "postgres" would have
// satisfied every check while the suite dropped and truncated the database
// holding real users. The call that looked most protective --
// assertTestDB(testDBName), immediately before the DROP -- was the emptiest,
// being a constant compared with itself.
//
// This is deliberately verified by asserting the denylist rather than by
// actually repointing the harness: the failure mode being guarded against is
// unrecoverable, so it must never be reproduced to be tested.
func TestProtectedDatabasesAreRefusedRegardlessOfTheConstant(t *testing.T) {
	for _, protected := range protectedDatabases {
		if err := assertTestDB(protected); err == nil {
			t.Errorf("assertTestDB(%q) returned nil; protected databases must be refused even if testDBName were edited to name one", protected)
		}
	}

	// And the constant itself must not be one of them -- which is what makes
	// the pre-DROP check meaningful rather than tautological.
	for _, protected := range protectedDatabases {
		if testDBName == protected {
			t.Fatalf("testDBName is %q, which is a protected database; the harness must never target it", testDBName)
		}
	}
}

// Derivation must replace the database name in DATABASE_URL rather than
// inherit it. DATABASE_URL points at the dev database, so a harness that used
// it as-is would truncate real data on its first run.
func TestResolveDSNsReplacesTheDatabaseName(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "postgres://quantsim:pw@localhost:5432/postgres?sslmode=disable")

	adminDSN, testDSN, err := resolveDSNs()
	if err != nil {
		t.Fatalf("resolveDSNs: %v", err)
	}

	if !strings.Contains(testDSN, "/"+testDBName) {
		t.Errorf("test DSN = %q, want it pointing at %q", testDSN, testDBName)
	}
	if strings.Contains(testDSN, "/postgres?") {
		t.Error("test DSN still points at the dev database; the path must be replaced, not inherited")
	}
	// The admin DSN targets the maintenance database on purpose: CREATE
	// DATABASE cannot run from inside the database being created.
	if !strings.Contains(adminDSN, "/postgres") {
		t.Errorf("admin DSN = %q, want it pointing at the postgres maintenance database", adminDSN)
	}
	// Query parameters must survive, or sslmode=disable is lost and the
	// connection fails against a server that has no TLS configured.
	if !strings.Contains(testDSN, "sslmode=disable") {
		t.Errorf("test DSN = %q, want the original query parameters preserved", testDSN)
	}
}

// A DATABASE_URL that already points somewhere unexpected must not be
// silently rewritten into something acceptable -- but it also must not be
// followed. Deriving from it yields quantsim_test either way, which is the
// safe outcome; this pins that the derived name is what gets checked.
func TestResolveDSNsGuardsTheDerivedName(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	// An explicit override may change host or user, but not the database name.
	t.Setenv("TEST_DATABASE_URL", "postgres://quantsim:pw@localhost:5432/postgres?sslmode=disable")

	if _, _, err := resolveDSNs(); err == nil {
		t.Fatal("resolveDSNs accepted a TEST_DATABASE_URL pointing at the dev database; the override exists to change host or user, never the database name")
	}
}

// The smoke test: the pool is live and connected to the right place. Runs
// before any test that writes.
func TestHarnessConnectsToTheTestDatabase(t *testing.T) {
	if skipReason != "" {
		t.Skip(skipReason)
	}

	var one int
	if err := testPool.QueryRow(t.Context(), `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 returned %d", one)
	}

	var current string
	if err := testPool.QueryRow(t.Context(), `SELECT current_database()`).Scan(&current); err != nil {
		t.Fatalf("current_database: %v", err)
	}
	if current != testDBName {
		t.Fatalf("connected to %q, want %q", current, testDBName)
	}
}

// Migrations must actually have been applied, from zero. If this fails, every
// other test in the suite is running against a schema that arrived by some
// other route -- which is exactly the "green against a stale database"
// failure the harness exists to avoid.
func TestMigrationsAppliedToTestDatabase(t *testing.T) {
	if skipReason != "" {
		t.Skip(skipReason)
	}

	for _, table := range []string{"users", "accounts", "positions", "orders", "trades", "historical_prices"} {
		var exists bool
		err := testPool.QueryRow(t.Context(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)`,
			table).Scan(&exists)
		if err != nil {
			t.Fatalf("checking table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %q missing; migrations did not apply cleanly to an empty database", table)
		}
	}

	// Migration 006's columns carry every number this service exists to get
	// right. Without them the store's statements fail outright, so checking
	// them here turns "006 did not apply" into one legible failure instead of
	// a column-does-not-exist error inside every other test.
	for _, col := range []struct{ table, column string }{
		{"positions", "avg_cost"},
		{"orders", "filled_price"},
		{"orders", "rejection_reason"},
		{"trades", "realized_pl"},
	} {
		var exists bool
		err := testPool.QueryRow(t.Context(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			 WHERE table_schema='public' AND table_name=$1 AND column_name=$2)`,
			col.table, col.column).Scan(&exists)
		if err != nil {
			t.Fatalf("checking %s.%s: %v", col.table, col.column, err)
		}
		if !exists {
			t.Errorf("column %s.%s missing; migration 006 did not apply", col.table, col.column)
		}
	}

	// avg_cost must be NOT NULL: the store writes it on every buy and reads it
	// on every sell without a nil check, because the schema promises it is
	// always there.
	var nullable string
	err := testPool.QueryRow(t.Context(),
		`SELECT is_nullable FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='positions' AND column_name='avg_cost'`).
		Scan(&nullable)
	if err != nil {
		t.Fatalf("checking positions.avg_cost nullability: %v", err)
	}
	if nullable != "NO" {
		t.Errorf("positions.avg_cost is nullable; the store reads it as a plain float64")
	}
}
