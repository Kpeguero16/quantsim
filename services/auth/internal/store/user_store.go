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
			return uuid.Nil, fmt.Errorf("%w", service.ErrDuplicateUser)
		}
		return uuid.Nil, err
	}
	return id, nil
}

func (s *PostgresUserStore) GetUserByEmail(ctx context.Context, email string) (*service.User, error) {
	query := `
	SELECT id, email, username, password_hash, created_at, updated_at FROM users WHERE email = $1
	`
	var u service.User
	var hash string
	err := s.pool.QueryRow(ctx, query, email).Scan(&u.ID, &u.Email, &u.Username, &hash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	u.PasswordHash = []byte(hash)
	return &u, nil
}

func (s *PostgresUserStore) GetUserByID(ctx context.Context, id uuid.UUID) (*service.User, error) {
	query := `
	SELECT id, email, username, created_at, updated_at FROM users WHERE id = $1
	`
	var u service.User
	err := s.pool.QueryRow(ctx, query, id).Scan(&u.ID, &u.Email, &u.Username, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}