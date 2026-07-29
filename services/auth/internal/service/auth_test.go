package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/auth/internal/service"
	"github.com/kpeguero/quantsim/services/auth/internal/service/mock"
)

var testSecret = []byte("test-secret")

func TestRegister_Success(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

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

	if len(users.Accounts) != 1 {
		t.Fatalf("expected 1 account created, got %d", len(users.Accounts))
	}
	if users.Accounts[0].Balance != service.StartingBalance {
		t.Fatalf("expected starting balance %v, got %v", service.StartingBalance, users.Accounts[0].Balance)
	}

	stored, err := users.GetUserByEmail(context.Background(), "a@b.com")
	if err != nil {
		t.Fatalf("expected user to exist: %v", err)
	}
	if string(stored.PasswordHash) == req.Password {
		t.Fatal("password was stored in plaintext")
	}
	if err := bcrypt.CompareHashAndPassword(stored.PasswordHash, []byte(req.Password)); err != nil {
		t.Fatalf("stored hash does not match password: %v", err)
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	ctx := context.Background()
	req := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"}
	if _, err := svc.Register(ctx, req); err != nil {
		t.Fatalf("unexpected error on first register: %v", err)
	}

	_, err := svc.Register(ctx, req)
	if !errors.Is(err, service.ErrDuplicateUser) {
		t.Fatalf("expected ErrDuplicateUser, got %v", err)
	}
	if len(users.Accounts) != 1 {
		t.Fatalf("expected no account created on duplicate register, still have %d", len(users.Accounts))
	}
}

// TestRegister_AtomicOnAccountFailure guards against a user being created
// with no funded account: if the account half of CreateUserWithAccount
// fails, no user should be left behind either (matching what a rolled-back
// Postgres transaction would do).
func TestRegister_AtomicOnAccountFailure(t *testing.T) {
	users := mock.NewUserStore()
	users.CreateAccountErr = errors.New("simulated account insert failure")
	svc := service.NewService(users, testSecret)

	ctx := context.Background()
	req := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"}
	if _, err := svc.Register(ctx, req); err == nil {
		t.Fatal("expected register to fail")
	}

	if _, err := users.GetUserByEmail(ctx, "a@b.com"); !errors.Is(err, service.ErrUserNotFound) {
		t.Fatalf("expected no user to be persisted after a failed register, got err=%v", err)
	}
	if len(users.Accounts) != 0 {
		t.Fatalf("expected no account to be persisted after a failed register, got %d", len(users.Accounts))
	}

	// Clearing the simulated failure should let the same email register
	// cleanly -- proves the failed attempt didn't leave a partial user
	// behind that would trip the duplicate-email check.
	users.CreateAccountErr = nil
	if _, err := svc.Register(ctx, req); err != nil {
		t.Fatalf("expected register to succeed after clearing the simulated failure, got %v", err)
	}
}

func TestLogin_Success(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	ctx := context.Background()
	registerReq := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"}
	if _, err := svc.Register(ctx, registerReq); err != nil {
		t.Fatalf("unexpected error on register: %v", err)
	}

	tokens, err := svc.Login(ctx, service.LoginRequest{Email: "a@b.com", Password: "pw12345678"})
	if err != nil {
		t.Fatalf("unexpected error on login: %v", err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatal("expected non-empty access and refresh tokens")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	ctx := context.Background()
	registerReq := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"}
	if _, err := svc.Register(ctx, registerReq); err != nil {
		t.Fatalf("unexpected error on register: %v", err)
	}

	_, err := svc.Login(ctx, service.LoginRequest{Email: "a@b.com", Password: "wrong-password"})
	if !errors.Is(err, service.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLogin_UnknownEmail(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	_, err := svc.Login(context.Background(), service.LoginRequest{Email: "nouser@x.com", Password: "whatever123"})
	if !errors.Is(err, service.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

// TestLogin_PropagatesNonNotFoundError guards against a store error that
// ISN'T "no such user" (a DB outage, a timeout) being silently folded into
// ErrInvalidCredentials -- that would surface a real infra failure as a
// misleading "wrong password" to every user trying to log in.
func TestLogin_PropagatesNonNotFoundError(t *testing.T) {
	users := mock.NewUserStore()
	dbErr := errors.New("simulated database connection failure")
	users.GetByEmailErr = dbErr
	svc := service.NewService(users, testSecret)

	_, err := svc.Login(context.Background(), service.LoginRequest{Email: "a@b.com", Password: "whatever123"})
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected the underlying db error to propagate, got %v", err)
	}
	if errors.Is(err, service.ErrInvalidCredentials) {
		t.Fatal("a non-not-found store error should not be reported as invalid credentials")
	}
}

func TestRefresh_Success(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	ctx := context.Background()
	registerTokens, err := svc.Register(ctx, service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"})
	if err != nil {
		t.Fatalf("unexpected error on register: %v", err)
	}

	newTokens, err := svc.Refresh(ctx, service.RefreshTokenRequest{RefreshToken: registerTokens.RefreshToken})
	if err != nil {
		t.Fatalf("unexpected error on refresh: %v", err)
	}
	if newTokens.AccessToken == "" || newTokens.RefreshToken == "" {
		t.Fatal("expected non-empty access and refresh tokens")
	}
}

func TestRefresh_ExpiredToken(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	expired := fabricateToken(t, testSecret, "some-user-id", pkgauth.TokenTypeRefresh, -1*time.Hour)

	_, err := svc.Refresh(context.Background(), service.RefreshTokenRequest{RefreshToken: expired})
	if !errors.Is(err, service.ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestRefresh_GarbageToken(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	_, err := svc.Refresh(context.Background(), service.RefreshTokenRequest{RefreshToken: "not-a-real-token"})
	if !errors.Is(err, service.ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestRefresh_AccessTokenRejected(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	ctx := context.Background()
	tokens, err := svc.Register(ctx, service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"})
	if err != nil {
		t.Fatalf("unexpected error on register: %v", err)
	}

	_, err = svc.Refresh(ctx, service.RefreshTokenRequest{RefreshToken: tokens.AccessToken})
	if !errors.Is(err, service.ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid for access token used as refresh, got %v", err)
	}
}

// TestRefresh_UserNotFound guards against a deleted user's refresh token
// continuing to mint valid access tokens: Refresh must check the user still
// exists, the same invariant Me already enforces.
func TestRefresh_UserNotFound(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	ctx := context.Background()
	tokens, err := svc.Register(ctx, service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"})
	if err != nil {
		t.Fatalf("unexpected error on register: %v", err)
	}
	stored, err := users.GetUserByEmail(ctx, "a@b.com")
	if err != nil {
		t.Fatalf("expected user to exist: %v", err)
	}

	users.DeleteUser(stored.ID)

	_, err = svc.Refresh(ctx, service.RefreshTokenRequest{RefreshToken: tokens.RefreshToken})
	if !errors.Is(err, service.ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid for a deleted user's refresh token, got %v", err)
	}
}

// fabricateToken signs a token directly (bypassing GenerateToken's TTL
// choices) so tests can construct expired or wrongly-typed tokens.
func fabricateToken(t *testing.T, secret []byte, userID, tokenType string, ttl time.Duration) string {
	t.Helper()
	now := time.Now()
	claims := pkgauth.Claims{
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		t.Fatalf("failed to fabricate token: %v", err)
	}
	return token
}

func TestMe_Success(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	ctx := context.Background()
	if _, err := svc.Register(ctx, service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"}); err != nil {
		t.Fatalf("unexpected error on register: %v", err)
	}
	stored, err := users.GetUserByEmail(ctx, "a@b.com")
	if err != nil {
		t.Fatalf("expected user to exist: %v", err)
	}

	profile, err := svc.Me(ctx, stored.ID)
	if err != nil {
		t.Fatalf("unexpected error on Me: %v", err)
	}
	if profile.Email != "a@b.com" || profile.Username != "alice" {
		t.Fatalf("expected profile for a@b.com/alice, got %+v", profile)
	}
}

func TestMe_UserNotFound(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	ctx := context.Background()
	if _, err := svc.Register(ctx, service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: "pw12345678"}); err != nil {
		t.Fatalf("unexpected error on register: %v", err)
	}
	stored, err := users.GetUserByEmail(ctx, "a@b.com")
	if err != nil {
		t.Fatalf("expected user to exist: %v", err)
	}

	// Simulate the user row vanishing between token issuance and use.
	users.DeleteUser(stored.ID)

	_, err = svc.Me(ctx, stored.ID)
	if !errors.Is(err, service.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

// TestMe_PropagatesNonNotFoundError mirrors TestLogin_PropagatesNonNotFoundError:
// a store error that isn't "no such user" must not be reported as an
// invalid-token 401.
func TestMe_PropagatesNonNotFoundError(t *testing.T) {
	users := mock.NewUserStore()
	dbErr := errors.New("simulated database connection failure")
	users.GetByIDErr = dbErr
	svc := service.NewService(users, testSecret)

	_, err := svc.Me(context.Background(), uuid.New())
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected the underlying db error to propagate, got %v", err)
	}
}
