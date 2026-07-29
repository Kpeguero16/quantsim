// Package auth holds JWT primitives shared by any QuantSim service that
// issues or validates tokens (the auth service today; the gateway later).
// It has no dependency on any service's internal packages.
package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

// Claims is the JWT payload QuantSim tokens carry: a standard subject (the
// user ID) plus a type distinguishing access from refresh tokens.
type Claims struct {
	TokenType string `json:"type"`
	jwt.RegisteredClaims
}

// GenerateToken signs a new HS256 JWT for userID, valid for ttl.
func GenerateToken(secret []byte, userID, tokenType string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}
