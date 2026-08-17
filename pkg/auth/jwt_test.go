package auth_test

import (
	"testing"
	"time"

	"github.com/kpeguero/quantsim/pkg/auth"
)

// TestGenerateToken_UniqueJTI pins the property Step 13's revocation denylist
// depends on: every token needs a stable, unique identifier to revoke by.
func TestGenerateToken_UniqueJTI(t *testing.T) {
	tokenA, err := auth.GenerateToken(testSecret, "user-123", auth.TokenTypeRefresh, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	tokenB, err := auth.GenerateToken(testSecret, "user-123", auth.TokenTypeRefresh, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	claimsA, err := auth.ValidateToken(testSecret, tokenA)
	if err != nil {
		t.Fatalf("failed to validate tokenA: %v", err)
	}
	claimsB, err := auth.ValidateToken(testSecret, tokenB)
	if err != nil {
		t.Fatalf("failed to validate tokenB: %v", err)
	}

	if claimsA.ID == "" {
		t.Fatal("expected a non-empty jti (RegisteredClaims.ID)")
	}
	if claimsA.ID == claimsB.ID {
		t.Fatalf("expected two tokens to have distinct jtis, both got %q", claimsA.ID)
	}
}

// TestGenerateToken_JTISetOnAccessTokensToo: the revocation check only ever
// consults a refresh token's jti (SPEC.md 2.4), but GenerateToken sets it
// unconditionally -- one code path, no branch on token type.
func TestGenerateToken_JTISetOnAccessTokensToo(t *testing.T) {
	token, err := auth.GenerateToken(testSecret, "user-123", auth.TokenTypeAccess, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	claims, err := auth.ValidateToken(testSecret, token)
	if err != nil {
		t.Fatalf("failed to validate token: %v", err)
	}
	if claims.ID == "" {
		t.Fatal("expected a non-empty jti on an access token")
	}
}
