package store
package service
import "context"
import "github.com/jackc/pgx/v5/pgxpool"

type PostgresUserStore struct { pool *pgxpool.Pool }

func NewPostgresUserStore(pool *pgxpool.Pool) *PostgresUserStore {
	return &PostgresUserStore{ pool: pool }
}