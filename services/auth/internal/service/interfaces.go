package service
import "context"
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
