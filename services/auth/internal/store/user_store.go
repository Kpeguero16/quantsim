package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

// Compile-time proof that this implementation still satisfies the interface
// the service depends on. Without it, a signature drift is caught only when
// cmd/server is built -- which no test does -- so `go test ./...` would stay
// green against a store the service can no longer use.
var _ service.UserStore = (*PostgresUserStore)(nil)

type PostgresUserStore struct {
	pool *pgxpool.Pool
}

func NewPostgresUserStore(pool *pgxpool.Pool) *PostgresUserStore {
	return &PostgresUserStore{pool: pool}
}

// CreateUserWithAccount inserts the user and their starting account in a
// single transaction, so a failure partway through (e.g. the account insert
// failing after the user insert succeeds) leaves no orphaned user behind.
func (s *PostgresUserStore) CreateUserWithAccount(ctx context.Context, email, username string, passwordHash []byte, startingBalance float64) (uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
	INSERT INTO users (email, username, password_hash)
	VALUES ($1, $2, $3)
	RETURNING id
	`, email, username, string(passwordHash)).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return uuid.Nil, fmt.Errorf("%w", service.ErrDuplicateUser)
		}
		return uuid.Nil, err
	}

	_, err = tx.Exec(ctx, `
	INSERT INTO accounts (user_id, balance)
	VALUES ($1, $2)
	`, id, startingBalance)
	if err != nil {
		return uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// GetUserByEmail looks a user up case-insensitively, matching the unique
// index on lower(email) added in migration 004 rather than the stored form
// of the address.
//
// An exact match would work today -- every stored email is lowercase,
// because 004 rewrote the existing rows and service.Register normalises
// every new one. But that is a convention, not a constraint: the index stops
// a second row colliding with Foo@x.test, it does not stop Foo@x.test
// existing. Were one ever to appear, an exact match would never find it and
// the user would get a 401 indistinguishable from a wrong password.
//
// The caller still normalises the address before calling. That is not a
// duplicate of this rule but the other half of it: lower(email) = $1 only
// means anything if the parameter is lowercase too.
func (s *PostgresUserStore) GetUserByEmail(ctx context.Context, email string) (*service.User, error) {
	query := `
	SELECT id, email, username, password_hash, created_at, updated_at FROM users WHERE lower(email) = $1
	`
	var u service.User
	var hash string
	err := s.pool.QueryRow(ctx, query, email).Scan(&u.ID, &u.Email, &u.Username, &hash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, service.ErrUserNotFound
		}
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
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, service.ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}
