package service_test

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
	"github.com/kpeguero/quantsim/services/auth/internal/service/mock"
)

var testSecret = []byte("test-secret")

func TestRegister_Success(t *testing.T) {
	users := mock.NewUserStore()
	accounts := &mock.AccountStore{}
	svc := service.NewService(users, accounts, testSecret)

	req := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"}
	tokens, err := svc.Register(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatal("expected non-empty access and refresh tokens")
	}
	if tokens.ExpiresIn != int64(service.AccessTokenTTL.Seconds()) {
		t.Fatalf("expected expires_in %d, got %d", int64(service.AccessTokenTTL.Seconds()), tokens.ExpiresIn)
	}

	if len(accounts.Created) != 1 {
		t.Fatalf("expected 1 account created, got %d", len(accounts.Created))
	}
	if accounts.Created[0].Balance != service.StartingBalance {
		t.Fatalf("expected starting balance %v, got %v", service.StartingBalance, accounts.Created[0].Balance)
	}

	stored, err := users.GetUserByEmail(context.Background(), "a@b.com")
	if err != nil {
		t.Fatalf("expected user to exist: %v", err)
	}
	hash := users.PasswordHash(stored.ID)
	if string(hash) == req.Password {
		t.Fatal("password was stored in plaintext")
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(req.Password)); err != nil {
		t.Fatalf("stored hash does not match password: %v", err)
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	users := mock.NewUserStore()
	accounts := &mock.AccountStore{}
	svc := service.NewService(users, accounts, testSecret)

	ctx := context.Background()
	req := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"}
	if _, err := svc.Register(ctx, req); err != nil {
		t.Fatalf("unexpected error on first register: %v", err)
	}

	_, err := svc.Register(ctx, req)
	if !errors.Is(err, service.ErrDuplicateUser) {
		t.Fatalf("expected ErrDuplicateUser, got %v", err)
	}
	if len(accounts.Created) != 1 {
		t.Fatalf("expected no account created on duplicate register, still have %d", len(accounts.Created))
	}
}
