package auth_test

import (
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/pkg/auth"
)

func TestValidateSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{"empty", "", true},
		{"one byte short", strings.Repeat("a", auth.MinSecretLength-1), true},
		{"exactly the minimum", strings.Repeat("a", auth.MinSecretLength), false},
		{"openssl rand -base64 32 output length", strings.Repeat("a", 44), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.ValidateSecret([]byte(tt.secret))
			if tt.wantErr && err == nil {
				t.Errorf("ValidateSecret(%d bytes) = nil, want an error", len(tt.secret))
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateSecret(%d bytes) = %v, want nil", len(tt.secret), err)
			}
		})
	}
}

// TestValidateSecretErrorOmitsSecret: the error surfaces at startup and lands
// in logs, so it must describe the problem without echoing the value.
func TestValidateSecretErrorOmitsSecret(t *testing.T) {
	secret := "short-but-recognisable-secret"
	err := auth.ValidateSecret([]byte(secret))
	if err == nil {
		t.Fatal("expected an error for a short secret")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error message leaks the secret: %v", err)
	}
}
