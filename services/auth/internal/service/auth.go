package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
)

const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 7 * 24 * time.Hour

	bcryptCost = 10

	// StartingBalance is the simulated USD balance every new account opens with.
	StartingBalance = 100000.00
)

type Service struct {
	users     UserStore
	jwtSecret []byte
}

func NewService(users UserStore, jwtSecret []byte) *Service {
	return &Service{users: users, jwtSecret: jwtSecret}
}

// Register validates and normalizes the submitted identity, hashes the
// password, and creates the user and its $100k starting account atomically
// (see UserStore.CreateUserWithAccount), then returns a fresh token pair.
//
// Validation runs before hashing and before any store call, so a rejected
// registration never reaches the database and never spends a bcrypt round.
// This is the single choke point the rules live behind -- see validate.go.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (*TokenPair, error) {
	email, username, err := ValidateRegistration(req.Email, req.Username, req.Password)
	if err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		// Unreachable: ValidateRegistration already rejects anything over
		// MaxPasswordBytes. Mapped anyway as defence in depth -- if a future
		// path ever reaches this without validating, the caller should see a
		// 400 describing the problem, not a 500. Never truncate to fit:
		// that would make two distinct passwords hash identically.
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			return nil, fmt.Errorf("%w: password must be at most %d bytes -- try a shorter one", ErrInvalidInput, MaxPasswordBytes)
		}
		return nil, err
	}

	userID, err := s.users.CreateUserWithAccount(ctx, email, username, hash, StartingBalance)
	if err != nil {
		return nil, err
	}

	return s.issueTokenPair(userID)
}

// dummyHash lets Login run a bcrypt comparison even when the email doesn't
// exist, so response timing doesn't reveal whether an email is registered.
var dummyHash = mustHash("not-a-real-password-used-only-for-constant-time-compare")

func mustHash(password string) []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		panic(err)
	}
	return hash
}

// Login verifies credentials and returns a fresh token pair. Unknown email
// and wrong password return the identical error so the API doesn't leak
// whether an email is registered. A store error other than "no such user"
// (a DB outage, a timeout) is not an auth failure and propagates as-is, so
// it surfaces as a 500 rather than a misleading "invalid credentials".
//
// The email is normalized so a user who registered as Khalil@x.test can log
// in as khalil@x.test. Nothing else is validated, deliberately: applying the
// registration rules here would lock out every account whose password
// predates them, and a policy-specific error would be distinguishable from
// the uniform failure below -- which, with the dummy-hash timing defence, is
// what keeps this endpoint from being a user-enumeration oracle. Registration
// enforces policy; Login authenticates whoever already exists.
func (s *Service) Login(ctx context.Context, req LoginRequest) (*TokenPair, error) {
	user, err := s.users.GetUserByEmail(ctx, NormalizeEmail(req.Email))
	if err != nil {
		if !errors.Is(err, ErrUserNotFound) {
			return nil, err
		}
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(req.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	return s.issueTokenPair(user.ID)
}

// Refresh validates a refresh token, confirms the user it was issued for
// still exists, and issues a brand-new token pair. The old refresh token is
// not revoked and remains valid until its natural expiry -- refresh tokens
// are stateless by design (see SPEC.md); no revocation list exists. The
// user-existence check is a separate concern from revocation: it's the same
// invariant Me enforces, so a deleted user's refresh token can't keep
// minting valid access tokens.
func (s *Service) Refresh(ctx context.Context, req RefreshTokenRequest) (*TokenPair, error) {
	claims, err := pkgauth.ValidateToken(s.jwtSecret, req.RefreshToken)
	if err != nil {
		return nil, ErrTokenInvalid
	}
	if claims.TokenType != pkgauth.TokenTypeRefresh {
		return nil, ErrTokenInvalid
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, ErrTokenInvalid
	}

	if _, err := s.users.GetUserByID(ctx, userID); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrTokenInvalid
		}
		return nil, err
	}

	return s.issueTokenPair(userID)
}

// Me returns the profile for an already-authenticated user. The caller
// (pkg/auth's middleware, via the handler) is the sole JWT gatekeeper for
// this path -- Me does no token parsing, just a store lookup. A store error
// other than "no such user" propagates as-is rather than being folded into
// ErrUserNotFound, so it surfaces as a 500 rather than a misleading 401.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (*MeResponse, error) {
	user, err := s.users.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &MeResponse{
		ID:        user.ID,
		Email:     user.Email,
		Username:  user.Username,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}, nil
}

func (s *Service) issueTokenPair(userID uuid.UUID) (*TokenPair, error) {
	access, err := pkgauth.GenerateToken(s.jwtSecret, userID.String(), pkgauth.TokenTypeAccess, AccessTokenTTL)
	if err != nil {
		return nil, err
	}
	refresh, err := pkgauth.GenerateToken(s.jwtSecret, userID.String(), pkgauth.TokenTypeRefresh, RefreshTokenTTL)
	if err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int64(AccessTokenTTL.Seconds()),
	}, nil
}
