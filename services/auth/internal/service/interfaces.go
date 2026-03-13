package service
import "context"
import "github.com/google/uuid"

type UserStore interface {
	CreateUser(ctx context.Context, email, username string, passwordHash []byte) (uuid.UUID, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
}

type AccountStore interface {
	CreateAccount(ctx context.Context, userID uuid.UUID, balance float64) (uuid.UUID, error)
}
