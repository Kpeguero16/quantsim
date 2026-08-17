//go:build integration

package integration

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

// overflowBalance is larger than accounts.balance can hold.
//
// The column is NUMERIC(20,4) -- precision 20, scale 4 -- so it accepts at
// most 16 digits before the decimal point. Verified directly against the
// server:
//
//	SELECT 1e15::NUMERIC(20,4)  ->  1000000000000000.0000
//	SELECT 1e16::NUMERIC(20,4)  ->  ERROR: numeric field overflow
//
// This is how the rollback test forces the accounts insert to fail *after*
// the users insert has already succeeded, which is the only arrangement that
// actually exercises the transaction. It needs no schema mutation, so unlike
// a temporary CHECK constraint there is nothing that can leak into a later
// test if cleanup is skipped or fails.
const overflowBalance = 1e16

// The transaction in CreateUserWithAccount is promised by a doc comment and,
// until now, by nothing else. The mock's CreateAccountErr hook returns before
// inserting anything, so it fakes atomicity by construction -- it cannot fail
// halfway through, which is the only interesting case.
func TestCreateUserWithAccountRollsBackWhenTheAccountInsertFails(t *testing.T) {
	s, pool, ctx := newStore(t)

	_, err := s.CreateUserWithAccount(ctx, "orphan@example.test", "orphan", []byte(testHash), overflowBalance)
	if err == nil {
		t.Fatal("CreateUserWithAccount succeeded with a balance that cannot fit in NUMERIC(20,4); the failure injection is broken, so this test proves nothing")
	}

	// Assert the failure is the one intended. Without this the test would
	// also pass if the *users* insert failed, in which case there would be no
	// rollback to verify at all.
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("err = %v (%T), want a *pgconn.PgError", err, err)
	}
	if pgErr.Code != "22003" {
		t.Fatalf("SQLSTATE = %s, want 22003 (numeric_value_out_of_range); the accounts insert must be what failed", pgErr.Code)
	}

	// The 23505 mapping lives only in the users branch. If it ever widens to
	// cover the account insert, an unrelated failure would surface to the
	// service as "duplicate user" and the handler would answer 409 for a
	// server-side problem.
	if errors.Is(err, service.ErrDuplicateUser) {
		t.Error("err maps to ErrDuplicateUser; the 23505 mapping must not swallow unrelated failures")
	}

	// The actual point: no orphan.
	var users, accounts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatalf("counting users: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&accounts); err != nil {
		t.Fatalf("counting accounts: %v", err)
	}
	if users != 0 {
		t.Errorf("users = %d, want 0 -- the user insert succeeded and must have been rolled back with the account", users)
	}
	if accounts != 0 {
		t.Errorf("accounts = %d, want 0", accounts)
	}
}

// Duplicate detection, across both unique indexes migration 004 created and
// in both exact and case-differing form.
//
// The mock keys a Go map on the exact email string and models no username
// uniqueness whatsoever, so three of these four cases cannot be expressed
// against it at all -- every existing auth suite is blind to them.
func TestCreateUserWithAccountMapsUniqueViolationsToErrDuplicateUser(t *testing.T) {
	for _, tc := range []struct {
		name            string
		email, username string
	}{
		{"same email", "dup@example.test", "different"},
		{"email differing only in case", "DUP@Example.test", "different"},
		{"same username", "different@example.test", "dupuser"},
		{"username differing only in case", "different@example.test", "DupUser"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, pool, ctx := newStore(t)

			if _, err := s.CreateUserWithAccount(ctx, "dup@example.test", "dupuser", []byte(testHash), service.StartingBalance); err != nil {
				t.Fatalf("seeding the first user: %v", err)
			}

			_, err := s.CreateUserWithAccount(ctx, tc.email, tc.username, []byte(testHash), service.StartingBalance)
			if !errors.Is(err, service.ErrDuplicateUser) {
				t.Errorf("err = %v, want ErrDuplicateUser", err)
			}

			// The rejected insert must not have left anything behind.
			var users int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
				t.Fatalf("counting users: %v", err)
			}
			if users != 1 {
				t.Errorf("users = %d, want 1", users)
			}
		})
	}
}

// The account row is created by the same call, with a currency nothing in Go
// ever sets -- it comes from the column default, which no test has exercised.
func TestCreateUserWithAccountCreatesExactlyOneAccount(t *testing.T) {
	s, pool, ctx := newStore(t)

	id, err := s.CreateUserWithAccount(ctx, "acct@example.test", "acct", []byte(testHash), service.StartingBalance)
	if err != nil {
		t.Fatalf("CreateUserWithAccount: %v", err)
	}

	var count int
	var currency string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE user_id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("counting accounts: %v", err)
	}
	if count != 1 {
		t.Fatalf("accounts for user = %d, want exactly 1", count)
	}
	if err := pool.QueryRow(ctx, `SELECT currency FROM accounts WHERE user_id = $1`, id).Scan(&currency); err != nil {
		t.Fatalf("reading currency: %v", err)
	}
	if currency != "USD" {
		t.Errorf("currency = %q, want USD from the column default", currency)
	}
}

// What a float64 balance actually becomes in NUMERIC(20,4).
//
// The values below were OBSERVED first and then written down, not predicted:
// the point is to pin current behaviour so a future change (to decimal, or to
// pgtype.Numeric) shows up as a diff here rather than as money quietly
// changing shape. Read back as ::text, because scanning into a float64 would
// re-introduce the very imprecision under examination.
//
// StartingBalance is the one that matters today -- it is what every real
// account opens with.
func TestCreateUserWithAccountBalancePrecision(t *testing.T) {
	for _, tc := range []struct {
		name    string
		balance float64
		want    string
	}{
		{"starting balance", service.StartingBalance, "100000.0000"},
		{"whole dollars", 250.0, "250.0000"},
		{"two decimal places", 1234.56, "1234.5600"},
		{"exactly at the scale limit", 0.0001, "0.0001"},
		{"rounded up beyond the scale", 0.00005, "0.0001"},
		{"rounded away below the scale", 0.00004, "0.0000"},
		{"five decimal places", 1234.56785, "1234.5679"},
		{"largest value that fits", 1e15, "1000000000000000.0000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, pool, ctx := newStore(t)

			id, err := s.CreateUserWithAccount(ctx, "bal@example.test", "bal", []byte(testHash), tc.balance)
			if err != nil {
				t.Fatalf("CreateUserWithAccount(%v): %v", tc.balance, err)
			}

			var got string
			if err := pool.QueryRow(ctx, `SELECT balance::text FROM accounts WHERE user_id = $1`, id).Scan(&got); err != nil {
				t.Fatalf("reading balance: %v", err)
			}
			if got != tc.want {
				t.Errorf("balance stored as %q, want %q (float64 %v)", got, tc.want, tc.balance)
			}
		})
	}
}
