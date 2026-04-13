package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresAccountStore struct {
	pool *pgxpool.Pool
}

func NewPostgresAccountStore(pool *pgxpool.Pool) *PostgresAccountStore {
	return &PostgresAccountStore{pool: pool}
}

// CreateAccount inserts a row into accounts with the given starting balance (currency defaults to USD in the DB).
func (s *PostgresAccountStore) CreateAccount(ctx context.Context, userID uuid.UUID, balance float64) (uuid.UUID, error) {
	query := `
	INSERT INTO accounts (user_id, balance)
	VALUES ($1, $2)
	RETURNING id
	`
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, query, userID, balance).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}
