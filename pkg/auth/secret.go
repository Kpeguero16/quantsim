package auth

import "fmt"

// MinSecretLength is the shortest JWT signing secret QuantSim accepts.
//
// HS256's security reduces entirely to the entropy of this secret: a short
// one can be brute-forced offline from a single captured token, after which
// an attacker mints tokens every service accepts as genuine, indefinitely and
// undetectably. 32 bytes matches HMAC-SHA256's block size and is what
// `openssl rand -base64 32` produces -- the command .env.example already
// tells you to run.
const MinSecretLength = 32

// ValidateSecret reports whether a signing secret is long enough to use. It
// is meant to be called at startup, by every service that signs or verifies
// tokens, so a weak secret fails loudly at boot rather than silently
// weakening every token the system issues.
func ValidateSecret(secret []byte) error {
	if len(secret) < MinSecretLength {
		return fmt.Errorf("JWT signing secret must be at least %d bytes, got %d (generate one with: openssl rand -base64 32)",
			MinSecretLength, len(secret))
	}
	return nil
}
