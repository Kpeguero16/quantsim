package service

import (
	"context"
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
	accounts  AccountStore
	jwtSecret []byte
}

func NewService(users UserStore, accounts AccountStore, jwtSecret []byte) *Service {
	return &Service{users: users, accounts: accounts, jwtSecret: jwtSecret}
}

// Register hashes the password, creates the user and a $100k starting
// account, and returns a fresh token pair.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (*TokenPair, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, err
	}

	userID, err := s.users.CreateUser(ctx, req.Email, req.Username, hash)
	if err != nil {
		return nil, err
	}

	if _, err := s.accounts.CreateAccount(ctx, userID, StartingBalance); err != nil {
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
// whether an email is registered.
func (s *Service) Login(ctx context.Context, req LoginRequest) (*TokenPair, error) {
	user, err := s.users.GetUserByEmail(ctx, req.Email)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(req.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	return s.issueTokenPair(user.ID)
}

// Refresh validates a refresh token and issues a brand-new token pair. The
// old refresh token is not revoked and remains valid until its natural
// expiry -- refresh tokens are stateless by design (see SPEC.md); no
// revocation list exists.
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

	return s.issueTokenPair(userID)
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
