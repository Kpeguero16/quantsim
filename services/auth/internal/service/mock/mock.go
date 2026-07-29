// Package mock provides an in-memory UserStore double for service- and
// handler-layer tests, so neither needs a live Postgres.
package mock

import (
	"context"

	"github.com/google/uuid"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

type AccountRecord struct {
	UserID  uuid.UUID
	Balance float64
}

type UserStore struct {
	byEmail  map[string]*service.User
	byID     map[uuid.UUID]*service.User
	Accounts []AccountRecord

	// CreateAccountErr, when set, makes CreateUserWithAccount fail after the
	// point where a real transaction would be inserting the account row --
	// and, like a rolled-back transaction, leaves no user behind either.
	// Lets tests verify Register's atomicity.
	CreateAccountErr error

	// GetByEmailErr / GetByIDErr, when set, are returned as-is instead of the
	// normal lookup outcome -- simulating a store error that ISN'T "no such
	// user" (e.g. a DB connection failure), so tests can verify callers don't
	// fold every error into an auth-failure response.
	GetByEmailErr error
	GetByIDErr    error
}

func NewUserStore() *UserStore {
	return &UserStore{
		byEmail: make(map[string]*service.User),
		byID:    make(map[uuid.UUID]*service.User),
	}
}

func (m *UserStore) CreateUserWithAccount(ctx context.Context, email, username string, passwordHash []byte, startingBalance float64) (uuid.UUID, error) {
	if _, exists := m.byEmail[email]; exists {
		return uuid.Nil, service.ErrDuplicateUser
	}
	if m.CreateAccountErr != nil {
		return uuid.Nil, m.CreateAccountErr
	}

	id := uuid.New()
	hashCopy := make([]byte, len(passwordHash))
	copy(hashCopy, passwordHash)
	u := &service.User{ID: id, Email: email, Username: username, PasswordHash: hashCopy}
	m.byEmail[email] = u
	m.byID[id] = u
	m.Accounts = append(m.Accounts, AccountRecord{UserID: id, Balance: startingBalance})
	return id, nil
}

func (m *UserStore) GetUserByEmail(ctx context.Context, email string) (*service.User, error) {
	if m.GetByEmailErr != nil {
		return nil, m.GetByEmailErr
	}
	u, ok := m.byEmail[email]
	if !ok {
		return nil, service.ErrUserNotFound
	}
	return u, nil
}

func (m *UserStore) GetUserByID(ctx context.Context, id uuid.UUID) (*service.User, error) {
	if m.GetByIDErr != nil {
		return nil, m.GetByIDErr
	}
	u, ok := m.byID[id]
	if !ok {
		return nil, service.ErrUserNotFound
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
