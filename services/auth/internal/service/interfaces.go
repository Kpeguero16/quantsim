package service
import "context"
import "time"
import "github.com/google/uuid"

type UserStore interface {
	// CreateUserWithAccount creates the user and its starting account
	// atomically -- in this domain a user never exists without a funded
	// account, so the store is responsible for making that a single
	// all-or-nothing operation (a transaction in the Postgres implementation).
	CreateUserWithAccount(ctx context.Context, email, username string, passwordHash []byte, startingBalance float64) (uuid.UUID, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*User, error)
}

// RevocationStore records refresh tokens that must no longer be honoured,
// keyed by their jti claim (SPEC.md Step 13, 2.1). A key's own TTL is what
// bounds storage growth -- there is no cleanup path because there is nothing
// to clean up.
type RevocationStore interface {
	// Revoke marks jti as revoked for ttl, after which it may be forgotten:
	// the token itself will have expired naturally by then.
	Revoke(ctx context.Context, jti string, ttl time.Duration) error
	// IsRevoked reports whether jti has been revoked and not yet expired.
	IsRevoked(ctx context.Context, jti string) (bool, error)
}
