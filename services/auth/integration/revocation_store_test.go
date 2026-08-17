//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/auth/internal/store"
)

// TestRedisTokenStore_RevokeThenIsRevoked proves the actual SET/GET round
// trip against a real Redis -- the mock only proves the calling code reacts
// correctly to a bool, not that Revoke and IsRevoked agree on a real server.
func TestRedisTokenStore_RevokeThenIsRevoked(t *testing.T) {
	client := newRedisClient(t)
	tokenStore := store.NewRedisTokenStore(client)
	ctx := context.Background()

	jti := uuid.NewString()

	revoked, err := tokenStore.IsRevoked(ctx, jti)
	if err != nil {
		t.Fatalf("IsRevoked before Revoke: %v", err)
	}
	if revoked {
		t.Fatal("expected an unrevoked jti to report false before Revoke is ever called")
	}

	if err := tokenStore.Revoke(ctx, jti, time.Minute); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	revoked, err = tokenStore.IsRevoked(ctx, jti)
	if err != nil {
		t.Fatalf("IsRevoked after Revoke: %v", err)
	}
	if !revoked {
		t.Fatal("expected the jti to report revoked after Revoke")
	}
}

// TestRedisTokenStore_TTLExpires proves Redis's own key expiry actually
// fires. Nothing about this is exercised by mock.RevocationStore, which
// does not model TTL at all -- this is the entire reason this test lives
// here instead of only at the unit level (SPEC.md Step 13, 2.1/2.6).
func TestRedisTokenStore_TTLExpires(t *testing.T) {
	client := newRedisClient(t)
	tokenStore := store.NewRedisTokenStore(client)
	ctx := context.Background()

	jti := uuid.NewString()
	if err := tokenStore.Revoke(ctx, jti, time.Second); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	revoked, err := tokenStore.IsRevoked(ctx, jti)
	if err != nil {
		t.Fatalf("IsRevoked immediately after Revoke: %v", err)
	}
	if !revoked {
		t.Fatal("expected the jti to report revoked immediately after Revoke")
	}

	time.Sleep(2 * time.Second)

	revoked, err = tokenStore.IsRevoked(ctx, jti)
	if err != nil {
		t.Fatalf("IsRevoked after TTL expiry: %v", err)
	}
	if revoked {
		t.Fatal("expected the jti to no longer report revoked once its TTL has passed")
	}
}

// TestRedisTokenStore_DifferentJTIsDontCollide pins the revoked: key prefix
// and construction: two distinct tokens must never be confused for one
// another.
func TestRedisTokenStore_DifferentJTIsDontCollide(t *testing.T) {
	client := newRedisClient(t)
	tokenStore := store.NewRedisTokenStore(client)
	ctx := context.Background()

	jtiA := uuid.NewString()
	jtiB := uuid.NewString()

	if err := tokenStore.Revoke(ctx, jtiA, time.Minute); err != nil {
		t.Fatalf("Revoke jtiA: %v", err)
	}

	revokedA, err := tokenStore.IsRevoked(ctx, jtiA)
	if err != nil {
		t.Fatalf("IsRevoked jtiA: %v", err)
	}
	revokedB, err := tokenStore.IsRevoked(ctx, jtiB)
	if err != nil {
		t.Fatalf("IsRevoked jtiB: %v", err)
	}

	if !revokedA {
		t.Fatal("expected jtiA to report revoked")
	}
	if revokedB {
		t.Fatal("expected jtiB to report unrevoked -- revoking jtiA must not affect it")
	}
}
