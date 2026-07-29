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
}

func NewUserStore() *UserStore {
	return &UserStore{
		byEmail: make(map[string]*service.User),
		byID:    make(map[uuid.UUID]*service.User),
	}
}

func (m *UserStore) CreateUser(ctx context.Context, email, username string, passwordHash []byte) (uuid.UUID, error) {
	if _, exists := m.byEmail[email]; exists {
		return uuid.Nil, service.ErrDuplicateUser
	}
	id := uuid.New()
	hashCopy := make([]byte, len(passwordHash))
	copy(hashCopy, passwordHash)
	u := &service.User{ID: id, Email: email, Username: username, PasswordHash: hashCopy}
	m.byEmail[email] = u
	m.byID[id] = u
	return id, nil
}

func (m *UserStore) GetUserByEmail(ctx context.Context, email string) (*service.User, error) {
	u, ok := m.byEmail[email]
	if !ok {
		return nil, ErrNotFound
	}
	return u, nil
}

func (m *UserStore) GetUserByID(ctx context.Context, id uuid.UUID) (*service.User, error) {
	u, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	return u, nil
}

// DeleteUser removes a user by ID, letting tests simulate a user vanishing
// between token issuance and use (e.g. a valid token, but no matching row).
func (m *UserStore) DeleteUser(id uuid.UUID) {
	if u, ok := m.byID[id]; ok {
		delete(m.byEmail, u.Email)
		delete(m.byID, id)
	}
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
