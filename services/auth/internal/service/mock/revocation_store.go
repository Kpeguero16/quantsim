package mock

import (
	"context"
	"time"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

// Compile-time proof this stays in step with the real Redis-backed store.
var _ service.RevocationStore = (*RevocationStore)(nil)

// RevocationStore is an in-memory RevocationStore double for service- and
// handler-layer tests. It does not model TTL expiry -- proving that a key
// actually expires needs a real Redis, which is what the integration suite
// is for (SPEC.md Step 13, 2.1/2.6). This double only proves the calling
// code reacts correctly to "revoked" vs "not revoked" vs "the store errored".
type RevocationStore struct {
	revoked map[string]bool

	// RevokeErr / IsRevokedErr, when set, are returned as-is instead of the
	// normal outcome -- simulating a Redis error so callers can prove they
	// fail open (SPEC.md Step 13, 2.3) rather than only exercising the happy
	// path.
	RevokeErr    error
	IsRevokedErr error

	// LastRevokeTTL is the ttl argument from the most recent Revoke call.
	// TTL expiry itself is not modeled here (see the type doc) -- this only
	// lets a test assert the *caller* computed a sane ttl before handing it
	// to the store.
	LastRevokeTTL time.Duration
}

func NewRevocationStore() *RevocationStore {
	return &RevocationStore{revoked: make(map[string]bool)}
}

func (m *RevocationStore) Revoke(ctx context.Context, jti string, ttl time.Duration) error {
	m.LastRevokeTTL = ttl
	if m.RevokeErr != nil {
		return m.RevokeErr
	}
	m.revoked[jti] = true
	return nil
}

func (m *RevocationStore) IsRevoked(ctx context.Context, jti string) (bool, error) {
	if m.IsRevokedErr != nil {
		return false, m.IsRevokedErr
	}
	return m.revoked[jti], nil
}
