// Package auth holds JWT primitives shared by any QuantSim service that
// issues or validates tokens (the auth service today; the gateway later).
// It has no dependency on any service's internal packages.
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

// ErrInvalidToken covers every way a token can fail validation (bad
// signature, malformed, expired). One sentinel, one error vocabulary --
// distinguishing the failure modes to the caller would only help an
// attacker probe which case they hit.
var ErrInvalidToken = errors.New("invalid or expired token")

// Claims is the JWT payload QuantSim tokens carry: a standard subject (the
// user ID) plus a type distinguishing access from refresh tokens.
type Claims struct {
	TokenType string `json:"type"`
	jwt.RegisteredClaims
}

// GenerateToken signs a new HS256 JWT for userID, valid for ttl. Every token
// gets a unique jti (RegisteredClaims.ID), access and refresh alike -- one
// code path, even though only a refresh token's jti is ever checked against
// the revocation store (SPEC.md Step 13, 2.4).
func GenerateToken(secret []byte, userID, tokenType string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ValidateToken checks signature and expiry and returns the parsed claims.
// It rejects anything not signed with HMAC, regardless of what alg the
// token header claims, so a token can't downgrade its own verification.
func ValidateToken(secret []byte, tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
