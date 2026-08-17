//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

const testHash = "$2a$10$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ012"

// THE test this whole step exists for.
//
// Step 10 changed GetUserByEmail from `WHERE email = $1` to
// `WHERE lower(email) = $1`. That change was verified by hand against the dev
// database and then protected by nothing: every auth suite runs against a Go
// map keyed on the exact string, where both queries behave identically.
//
// The row here is seeded with raw SQL rather than through the store, and that
// is not a shortcut -- it is the only way. service.Register lowercases every
// address before it reaches the store, so the store cannot produce a
// mixed-case stored email. Proving the lookup matches on lower(email)
// requires creating a row the store itself never could.
//
// Reverting the query makes this fail with ErrUserNotFound. Nothing else in
// the repo does.
func TestGetUserByEmailFindsAMixedCaseStoredRow(t *testing.T) {
	s, pool, ctx := newStore(t)

	const stored = "MiXeD@Example.test"
	id := insertUserRaw(t, ctx, pool, stored, "MixedUser", testHash)

	u, err := s.GetUserByEmail(ctx, "mixed@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail(lowercase) = %v; a row stored as %q must still be findable, or its owner gets a 401 indistinguishable from a wrong password", err, stored)
	}
	if u.ID != id {
		t.Errorf("got user %s, want %s", u.ID, id)
	}

	// The store returns the STORED form, not the argument it was given.
	// Nothing asserted this before, and it is the difference between
	// displaying a user's address as they typed it and as we normalised it.
	if u.Email != stored {
		t.Errorf("Email = %q, want the stored form %q", u.Email, stored)
	}
}

// The other half of Step 10's contract, and the one that is easy to erode.
//
// The store's doc comment is explicit that the caller normalises: the query
// is `lower(email) = $1`, not `lower(email) = lower($1)`. Someone later
// "fixing" it to lowercase its own argument would look like a harmless
// robustness improvement, silently widening what matches. This pins it so
// that change has to be deliberate.
func TestGetUserByEmailDoesNotLowercaseItsArgument(t *testing.T) {
	s, pool, ctx := newStore(t)

	insertUserRaw(t, ctx, pool, "plain@example.test", "plain", testHash)

	_, err := s.GetUserByEmail(ctx, "PLAIN@Example.test")
	if !errors.Is(err, service.ErrUserNotFound) {
		t.Errorf("GetUserByEmail(uppercase argument) = %v, want ErrUserNotFound -- the store matches lower(email) against the parameter as given; normalising is the caller's half of the contract", err)
	}
}

// A full round trip through the real driver: UUID, timestamptz, and a hash
// that crosses a TEXT column as a Go string and comes back as bytes. The mock
// hands back the same structs it was given, so none of this encoding is
// exercised anywhere else.
func TestCreateThenGetRoundTripsEveryColumn(t *testing.T) {
	s, _, ctx := newStore(t)

	before := time.Now().UTC()
	id, err := s.CreateUserWithAccount(ctx, "round@example.test", "roundtrip", []byte(testHash), service.StartingBalance)
	if err != nil {
		t.Fatalf("CreateUserWithAccount: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("returned the nil UUID; RETURNING id did not produce a value")
	}

	u, err := s.GetUserByEmail(ctx, "round@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	if u.ID != id {
		t.Errorf("ID = %s, want %s", u.ID, id)
	}
	if u.Email != "round@example.test" {
		t.Errorf("Email = %q", u.Email)
	}
	if u.Username != "roundtrip" {
		t.Errorf("Username = %q", u.Username)
	}
	if string(u.PasswordHash) != testHash {
		t.Errorf("PasswordHash did not survive the round trip:\n got  %q\n want %q", u.PasswordHash, testHash)
	}
	if u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Errorf("timestamps not populated: created=%v updated=%v", u.CreatedAt, u.UpdatedAt)
	}
	// Generous window: this asserts the column default fired and the value
	// decoded as a real time, not that any particular clock is accurate.
	if u.CreatedAt.Before(before.Add(-time.Minute)) || u.CreatedAt.After(time.Now().UTC().Add(time.Minute)) {
		t.Errorf("CreatedAt = %v, outside a sane window around %v", u.CreatedAt, before)
	}
}

// GetUserByID deliberately omits password_hash from its SELECT. The mock
// returns the same fully-populated pointer for both lookups, so this
// asymmetry is invisible to every existing test -- and it matters, because
// anything that assumed GetUserByID returned a usable hash would compare
// against nil and fail in a confusing way.
func TestGetUserByIDOmitsThePasswordHash(t *testing.T) {
	s, _, ctx := newStore(t)

	id, err := s.CreateUserWithAccount(ctx, "byid@example.test", "byid", []byte(testHash), service.StartingBalance)
	if err != nil {
		t.Fatalf("CreateUserWithAccount: %v", err)
	}

	u, err := s.GetUserByID(ctx, id)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if u.PasswordHash != nil {
		t.Errorf("PasswordHash = %q, want nil -- GetUserByID does not select the column, and callers must not rely on it", u.PasswordHash)
	}
	if u.Email != "byid@example.test" {
		t.Errorf("Email = %q", u.Email)
	}
}

// pgx.ErrNoRows must map to the service's sentinel, against the real driver
// rather than a map lookup returning a Go zero value.
func TestLookupMissesReturnErrUserNotFound(t *testing.T) {
	s, pool, ctx := newStore(t)

	t.Run("by email", func(t *testing.T) {
		u, err := s.GetUserByEmail(ctx, "nobody@example.test")
		if !errors.Is(err, service.ErrUserNotFound) {
			t.Errorf("err = %v, want ErrUserNotFound", err)
		}
		if u != nil {
			t.Errorf("user = %+v, want nil alongside the error", u)
		}
	})

	t.Run("by id", func(t *testing.T) {
		u, err := s.GetUserByID(ctx, uuid.New())
		if !errors.Is(err, service.ErrUserNotFound) {
			t.Errorf("err = %v, want ErrUserNotFound", err)
		}
		if u != nil {
			t.Errorf("user = %+v, want nil alongside the error", u)
		}
	})

	// An empty address must miss rather than match the first row or error.
	t.Run("empty email", func(t *testing.T) {
		insertUserRaw(t, ctx, pool, "someone@example.test", "someone", testHash)
		if _, err := s.GetUserByEmail(ctx, ""); !errors.Is(err, service.ErrUserNotFound) {
			t.Errorf("err = %v, want ErrUserNotFound", err)
		}
	})
}
