package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

// ErrDuplicateUser is returned when CreateUser fails due to unique constraint
// (duplicate email or username). The service layer should map this to 409 Conflict.
var ErrDuplicateUser = errors.New("duplicate user: email or username already exists")

type PostgresUserStore struct {
	pool *pgxpool.Pool
}

func NewPostgresUserStore(pool *pgxpool.Pool) *PostgresUserStore {
	return &PostgresUserStore{pool: pool}
}

func (s *PostgresUserStore) CreateUser(ctx context.Context, email, username string, passwordHash []byte) (uuid.UUID, error) {
	query := `
	INSERT INTO users (email, username, password_hash)
	VALUES ($1, $2, $3)
	RETURNING id
	`
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, query, email, username, string(passwordHash)).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return uuid.Nil, fmt.Errorf("%w", ErrDuplicateUser)
		}
		return uuid.Nil, err
	}
	return id, nil
}

func (s *PostgresUserStore) GetUserByEmail(ctx context.Context, email string) (*service.User, error) {
	query := `
	SELECT id, email, username, created_at, updated_at FROM users WHERE email = $1
	`
	var u service.User
	err := s.pool.QueryRow(ctx, query, email).Scan(&u.ID, &u.Email, &u.Username, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}