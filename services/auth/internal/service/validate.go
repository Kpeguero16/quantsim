package service

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// Registration input limits.
//
// The password minimum is counted in RUNES and the maximum in BYTES. The
// asymmetry is deliberate, not an oversight:
//
//   - The minimum is a user-facing promise. Someone who types 15 characters
//     has typed 15 characters whatever script they're in; counting bytes
//     would accept one 15-character password and reject another.
//   - The maximum is a bcrypt limit. golang.org/x/crypto/bcrypt returns
//     ErrPasswordTooLong above 72 bytes -- it does not silently truncate,
//     which is the good outcome, since truncation would make two different
//     passwords hash identically.
//
// 72 bytes is a documented deviation from NIST SP 800-63B's "SHOULD permit
// at least 64 characters": a 64-character password in a multi-byte script
// exceeds it and is rejected. See SPEC.md 2.4 -- the fix is to stop using
// bcrypt directly, which is deferred to its own step.
const (
	MinPasswordRunes = 15
	MaxPasswordBytes = 72

	// MaxEmailBytes is RFC 5321's practical maximum address length.
	MaxEmailBytes = 254

	MinUsernameRunes = 3
	MaxUsernameRunes = 30
)

// NormalizeEmail trims surrounding whitespace and lowercases the address.
//
// The domain part is case-insensitive by RFC; the local part technically is
// not, but no mail provider treats Khalil@ and khalil@ as different
// mailboxes. Case-sensitive uniqueness would let an attacker who knows
// victim@example.com exists register Victim@example.com -- a second row for
// one real mailbox, which any future password-reset or notification flow
// would then have two candidate accounts for.
//
// Idempotent: normalizing an already-normalized address changes nothing.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NormalizeUsername trims surrounding whitespace and lowercases the name.
// Same reasoning as NormalizeEmail, applied to the impersonation case: if
// Admin and admin can coexist, the name shown in the dashboard header is
// ambiguous about who is who. Idempotent.
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// ValidateRegistration applies every registration rule and returns the
// normalized email and username to store. Returning the normalized values
// (rather than validating in place) is what keeps a caller from forgetting
// to normalize: there is no way to use this function and still write the
// raw input.
//
// Every failure wraps ErrInvalidInput with a message written for the person
// who submitted it -- the frontend renders it verbatim. Describing the
// caller's own input back to them discloses nothing.
//
// Pure: no context, no store, no HTTP. This is the single choke point the
// rules live behind, so a future gRPC entry point or seed script can't get
// around them by not going through the chi handler.
func ValidateRegistration(email, username, password string) (string, string, error) {
	normEmail := NormalizeEmail(email)
	normUsername := NormalizeUsername(username)

	if err := validateEmail(normEmail); err != nil {
		return "", "", err
	}
	if err := validateUsername(normUsername); err != nil {
		return "", "", err
	}
	// Password last: the blocklist needs the validated email and username as
	// context terms, since a password containing your own username is
	// exactly the "expected" case a blocklist is required to catch.
	if err := validatePassword(password, normEmail, normUsername); err != nil {
		return "", "", err
	}

	return normEmail, normUsername, nil
}

func validateEmail(email string) error {
	if email == "" {
		return fmt.Errorf("%w: email is required", ErrInvalidInput)
	}
	// Length before parsing, so a megabyte of junk isn't handed to the parser.
	if len(email) > MaxEmailBytes {
		return fmt.Errorf("%w: email must be at most %d characters", ErrInvalidInput, MaxEmailBytes)
	}

	// net/mail implements RFC 5322 already. A hand-rolled regex is the known
	// trap here: it rejects valid addresses (plus-tags, new TLDs) while still
	// admitting nonsense.
	//
	// The equality check matters. ParseAddress accepts "Khalil <a@b.test>"
	// and returns a@b.test as the address -- without comparing back to the
	// input, that entire display-name string would be accepted as an email.
	//
	// Not required: a dot in the domain. user@localhost is a valid address,
	// and the check is theatre anyway -- a@b.co passes it and is equally
	// disposable. Throwaway registrations are rate limiting's problem.
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return fmt.Errorf("%w: enter a valid email address", ErrInvalidInput)
	}

	return nil
}

func validateUsername(username string) error {
	if username == "" {
		return fmt.Errorf("%w: username is required", ErrInvalidInput)
	}

	if n := utf8.RuneCountInString(username); n < MinUsernameRunes || n > MaxUsernameRunes {
		return fmt.Errorf("%w: username must be between %d and %d characters",
			ErrInvalidInput, MinUsernameRunes, MaxUsernameRunes)
	}

	// The rule is [A-Za-z0-9_-]; only the lowercase half is checked because
	// the input is already normalized, which accepts and rejects exactly the
	// same set.
	//
	// This excludes non-Latin usernames, which is a real cost and the right
	// trade here: unrestricted Unicode allows homograph impersonation --
	// "раypal" with a Cyrillic а and р renders indistinguishably from
	// "paypal". Restricting the charset eliminates the class instead of
	// trying to detect it. A product serving non-Latin scripts would need
	// Unicode normalization plus confusable detection, not a wider charset.
	for _, r := range username {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return fmt.Errorf("%w: username may contain only letters, numbers, hyphens, and underscores", ErrInvalidInput)
		}
	}

	return nil
}

func validatePassword(password, email, username string) error {
	if password == "" {
		return fmt.Errorf("%w: password is required", ErrInvalidInput)
	}

	if utf8.RuneCountInString(password) < MinPasswordRunes {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidInput, MinPasswordRunes)
	}
	if len(password) > MaxPasswordBytes {
		return fmt.Errorf("%w: password must be at most %d bytes -- try a shorter one", ErrInvalidInput, MaxPasswordBytes)
	}

	// No composition rules -- no required uppercase, digit, or symbol. That
	// is a SHALL NOT in NIST SP 800-63B 3.1.1.2, not an omission.

	return checkBlocklist(password, email, username)
}
