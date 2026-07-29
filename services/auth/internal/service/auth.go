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
