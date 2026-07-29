package service

import (
	"context"
	"errors"
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

// Register hashes the password and creates the user and its $100k starting
// account atomically (see UserStore.CreateUserWithAccount), then returns a
// fresh token pair.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (*TokenPair, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, err
	}

	userID, err := s.users.CreateUserWithAccount(ctx, req.Email, req.Username, hash, StartingBalance)
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
func (s *Service) Login(ctx context.Context, req LoginRequest) (*TokenPair, error) {
	user, err := s.users.GetUserByEmail(ctx, req.Email)
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
