// Package mock provides in-memory UserStore and AccountStore doubles for
// service- and handler-layer tests, so neither needs a live Postgres.
package mock

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

// ErrNotFound is returned by GetUserByEmail/GetUserByID when no user matches,
// mirroring the "no rows" outcome a real Postgres store would produce.
var ErrNotFound = errors.New("user not found")

type UserStore struct {
	byEmail map[string]*service.User
	byID    map[uuid.UUID]*service.User
	hashes  map[uuid.UUID][]byte
}

func NewUserStore() *UserStore {
	return &UserStore{
		byEmail: make(map[string]*service.User),
		byID:    make(map[uuid.UUID]*service.User),
		hashes:  make(map[uuid.UUID][]byte),
	}
}

func (m *UserStore) CreateUser(ctx context.Context, email, username string, passwordHash []byte) (uuid.UUID, error) {
	if _, exists := m.byEmail[email]; exists {
		return uuid.Nil, service.ErrDuplicateUser
	}
	id := uuid.New()
	u := &service.User{ID: id, Email: email, Username: username}
	m.byEmail[email] = u
	m.byID[id] = u
	hashCopy := make([]byte, len(passwordHash))
	copy(hashCopy, passwordHash)
	m.hashes[id] = hashCopy
	return id, nil
}

func (m *UserStore) GetUserByEmail(ctx context.Context, email string) (*service.User, error) {
	u, ok := m.byEmail[email]
	if !ok {
		return nil, ErrNotFound
	}
	return u, nil
}

// PasswordHash exposes the hash stored for a user so tests can assert it was
// actually hashed (not stored in plaintext) without leaking this through the
// UserStore interface itself.
func (m *UserStore) PasswordHash(id uuid.UUID) []byte {
	return m.hashes[id]
}

type AccountCreation struct {
	UserID  uuid.UUID
	Balance float64
}

type AccountStore struct {
	Created []AccountCreation
}

func (m *AccountStore) CreateAccount(ctx context.Context, userID uuid.UUID, balance float64) (uuid.UUID, error) {
	m.Created = append(m.Created, AccountCreation{UserID: userID, Balance: balance})
	return uuid.New(), nil
}
