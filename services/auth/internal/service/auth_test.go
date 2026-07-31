package service_test

import (
	"context"
	"errors"
	"strings"
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

	req := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: validPassword}
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
	req := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: validPassword}
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
	req := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: validPassword}
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
	registerReq := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: validPassword}
	if _, err := svc.Register(ctx, registerReq); err != nil {
		t.Fatalf("unexpected error on register: %v", err)
	}

	tokens, err := svc.Login(ctx, service.LoginRequest{Email: "a@b.com", Password: validPassword})
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
	registerReq := service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: validPassword}
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
	registerTokens, err := svc.Register(ctx, service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: validPassword})
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
	tokens, err := svc.Register(ctx, service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: validPassword})
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
	tokens, err := svc.Register(ctx, service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: validPassword})
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
	if _, err := svc.Register(ctx, service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: validPassword}); err != nil {
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
	if _, err := svc.Register(ctx, service.RegisterRequest{Email: "a@b.com", Username: "alice", Password: validPassword}); err != nil {
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

// --- Step 9: registration validation (SPEC.md 2.1-2.9, 2.12) ---

// TestRegister_RejectsInvalidInputBeforeTouchingStore is the assertion that
// proves validation is a real choke point rather than the database happening
// to reject bad input: nothing is written on the way to the rejection.
func TestRegister_RejectsInvalidInputBeforeTouchingStore(t *testing.T) {
	cases := []struct {
		name, email, username, password string
	}{
		{"password one under the minimum", "new@b.test", "bob", "fourteen-chars"},
		{"password over 72 bytes", "new@b.test", "bob", strings.Repeat("z", 80)},
		{"password on the blocklist", "new@b.test", "bob", "aaaaaaaaaaaaaaaa"},
		{"password contains the username", "new@b.test", "bobbie", "bobbie-bobbie-bobbie"},
		{"malformed email", "x", "bob", validPassword},
		{"username over the maximum", "new@b.test", strings.Repeat("u", 31), validPassword},
		{"username with a disallowed character", "new@b.test", "bo b", validPassword},
		{"everything empty", "", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			users := mock.NewUserStore()
			svc := service.NewService(users, testSecret)

			_, err := svc.Register(context.Background(), service.RegisterRequest{
				Email: tc.email, Username: tc.username, Password: tc.password,
			})

			if !errors.Is(err, service.ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
			if len(users.Accounts) != 0 {
				t.Fatalf("a rejected registration still wrote %d account(s)", len(users.Accounts))
			}
		})
	}
}

func TestRegister_StoresNormalizedIdentity(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)
	ctx := context.Background()

	if _, err := svc.Register(ctx, service.RegisterRequest{
		Email: "  Alice@Example.TEST  ", Username: "  ALICE  ", Password: validPassword,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, err := users.GetUserByEmail(ctx, "alice@example.test")
	if err != nil {
		t.Fatalf("user was not stored under the normalized email: %v", err)
	}
	if stored.Username != "alice" {
		t.Fatalf("username stored as %q, want %q", stored.Username, "alice")
	}
}

// TestLogin_FindsUserRegisteredInADifferentCase covers the second of the two
// bugs in SPEC.md section 1: registering as alice@example.test and then
// logging in as Alice@Example.TEST returned 401 before this step.
func TestLogin_FindsUserRegisteredInADifferentCase(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)
	ctx := context.Background()

	if _, err := svc.Register(ctx, service.RegisterRequest{
		Email: "alice@example.test", Username: "alice", Password: validPassword,
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := svc.Login(ctx, service.LoginRequest{
		Email: "Alice@Example.TEST", Password: validPassword,
	}); err != nil {
		t.Fatalf("login with different capitalization failed: %v", err)
	}
}

// TestLogin_ExistingShortPasswordStillAuthenticates is the single most
// important assertion in Step 9. Raising the minimum to 15 makes every
// existing password in the database non-compliant; SPEC.md 2.12 keeps Login
// untightened precisely so that locks nobody out. That property is load
// bearing, so it is asserted rather than assumed.
func TestLogin_ExistingShortPasswordStillAuthenticates(t *testing.T) {
	const legacyPassword = "pw12345678" // 10 characters -- below the new minimum

	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)
	ctx := context.Background()

	// Seeded directly, bypassing Register: this stands in for an account that
	// predates the policy, which Register would now refuse to create.
	hash, err := bcrypt.GenerateFromPassword([]byte(legacyPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := users.CreateUserWithAccount(ctx, "legacy@b.test", "legacy", hash, service.StartingBalance); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Guard the guard: if this password were still registerable, the test
	// below would pass for the wrong reason and prove nothing.
	if _, err := svc.Register(ctx, service.RegisterRequest{
		Email: "new@b.test", Username: "newuser", Password: legacyPassword,
	}); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected the legacy password to be unregisterable now, got %v", err)
	}

	if _, err := svc.Login(ctx, service.LoginRequest{
		Email: "legacy@b.test", Password: legacyPassword,
	}); err != nil {
		t.Fatalf("an existing %d-character-password account can no longer log in: %v", len(legacyPassword), err)
	}
}

// TestLogin_IsNotSubjectToRegistrationRules pins SPEC.md 2.12 directly: a
// login whose password would fail every registration rule must still come
// back as ErrInvalidCredentials, never ErrInvalidInput. A policy-specific
// error here would be distinguishable from a wrong password and turn login
// into a user-enumeration oracle.
func TestLogin_IsNotSubjectToRegistrationRules(t *testing.T) {
	users := mock.NewUserStore()
	svc := service.NewService(users, testSecret)

	for _, password := range []string{"a", "", "aaaaaaaaaaaaaaaa", strings.Repeat("z", 80)} {
		_, err := svc.Login(context.Background(), service.LoginRequest{
			Email: "nobody@b.test", Password: password,
		})
		if errors.Is(err, service.ErrInvalidInput) {
			t.Fatalf("login applied a registration rule to password of length %d", len(password))
		}
		if !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	}
}
