package service_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

// The fixture every table case varies one field of. Chosen to pass every
// rule: 22 runes, ASCII, not on the blocklist, and containing neither the
// username, the email local part, nor a service term.
const (
	validEmail    = "alice@example.test"
	validUsername = "alice"
	validPassword = "quiet-harbor-lantern-9"
)

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"mixed case", "Khalil@Example.TEST", "khalil@example.test"},
		{"surrounding whitespace", "  khalil@example.test  ", "khalil@example.test"},
		{"both", "\t Khalil@EXAMPLE.test \n", "khalil@example.test"},
		{"already normalized", "khalil@example.test", "khalil@example.test"},
		{"empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := service.NormalizeEmail(tc.input)
			if got != tc.want {
				t.Fatalf("NormalizeEmail(%q) = %q, want %q", tc.input, got, tc.want)
			}
			// Idempotence is asserted, not assumed: normalization runs at
			// registration and again at every login lookup, so a second pass
			// that changed the value would silently break sign-in.
			if again := service.NormalizeEmail(got); again != got {
				t.Fatalf("NormalizeEmail not idempotent: %q -> %q -> %q", tc.input, got, again)
			}
		})
	}
}

func TestNormalizeUsername(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"mixed case", "Khalil", "khalil"},
		{"surrounding whitespace", "  khalil  ", "khalil"},
		{"both", " ADMIN ", "admin"},
		{"already normalized", "khalil", "khalil"},
		{"empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := service.NormalizeUsername(tc.input)
			if got != tc.want {
				t.Fatalf("NormalizeUsername(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if again := service.NormalizeUsername(got); again != got {
				t.Fatalf("NormalizeUsername not idempotent: %q -> %q -> %q", tc.input, got, again)
			}
		})
	}
}

func TestValidateRegistration_ReturnsNormalizedValues(t *testing.T) {
	email, username, err := service.ValidateRegistration("  Alice@Example.TEST  ", "  ALICE  ", validPassword)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if email != "alice@example.test" {
		t.Fatalf("email = %q, want %q", email, "alice@example.test")
	}
	if username != "alice" {
		t.Fatalf("username = %q, want %q", username, "alice")
	}
}

func TestValidateRegistration_Email(t *testing.T) {
	cases := []struct {
		name    string
		email   string
		wantErr bool
	}{
		{"ordinary address", "alice@example.test", false},
		{"plus tag", "alice+trades@example.test", false},
		{"subdomain", "alice@mail.example.test", false},
		{"no dot in domain", "alice@localhost", false},
		{"mixed case is normalized, not rejected", "Alice@Example.TEST", false},
		{"exactly 254 bytes", strings.Repeat("a", 254-len("@example.test")) + "@example.test", false},

		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"no at sign", "x", true},
		{"no domain", "alice@", true},
		{"no local part", "@example.test", true},
		{"two at signs", "alice@@example.test", true},
		{"internal space", "ali ce@example.test", true},
		// ParseAddress accepts this and returns alice@example.test. Without
		// comparing the parsed address back to the input, the whole
		// display-name string would be stored as an email.
		{"display name form", "Khalil <alice@example.test>", true},
		{"angle brackets only", "<alice@example.test>", true},
		{"255 bytes", strings.Repeat("a", 255-len("@example.test")) + "@example.test", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A distinct username avoids the context-term rule firing on the
			// email local part and masking the result under test.
			_, _, err := service.ValidateRegistration(tc.email, "zephyr", validPassword)
			assertInvalidInput(t, err, tc.wantErr)
		})
	}
}

func TestValidateRegistration_Username(t *testing.T) {
	cases := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{"at minimum length", "abc", false},
		{"at maximum length", strings.Repeat("u", 30), false},
		{"digits, hyphen, underscore", "a_b-c123", false},
		{"mixed case is normalized, not rejected", "Alice", false},

		{"empty", "", true},
		{"one under minimum", "ab", true},
		{"one over maximum", strings.Repeat("u", 31), true},
		{"space", "ali ce", true},
		{"dot", "ali.ce", true},
		{"at sign", "ali@ce", true},
		// The homograph case 2.9 exists for: Cyrillic а and р render
		// indistinguishably from the Latin letters.
		{"cyrillic lookalike", "раypal", true},
		{"emoji", "ali\U0001F600ce", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := service.ValidateRegistration(validEmail, tc.username, validPassword)
			assertInvalidInput(t, err, tc.wantErr)
		})
	}
}

func TestValidateRegistration_PasswordLength(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"exactly 15 runes", "fifteen-chars-x", false},
		{"exactly 72 bytes", strings.Repeat("ab", 36), false},

		{"empty", "", true},
		{"one rune", "a", true},
		{"one rune under minimum", "fourteen-chars", true},
		{"73 bytes", strings.Repeat("ab", 36) + "c", true},
		{"80 bytes", strings.Repeat("z", 80), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := service.ValidateRegistration(validEmail, validUsername, tc.password)
			assertInvalidInput(t, err, tc.wantErr)
		})
	}
}

// TestValidateRegistration_PasswordRuneByteSplit proves the minimum is
// counted in runes and the maximum in bytes, rather than both in whichever
// unit happens to be convenient.
//
// Both directions are needed. Accepting a 15-rune/18-byte password proves
// nothing on its own: because runes <= bytes, a byte-counted minimum is
// strictly more permissive and would accept it too. The case that
// distinguishes them is the reverse one -- 14 runes but 16 bytes, which a
// rune minimum rejects and a byte minimum wrongly accepts.
func TestValidateRegistration_PasswordRuneByteSplit(t *testing.T) {
	t.Run("15 runes over 15 bytes is accepted", func(t *testing.T) {
		const password = "café-münchen-zü"

		assertRunesBytes(t, password, service.MinPasswordRunes, service.MinPasswordRunes+1)

		if _, _, err := service.ValidateRegistration(validEmail, validUsername, password); err != nil {
			t.Fatalf("15-rune multi-byte password rejected: %v", err)
		}
	})

	t.Run("14 runes over 15 bytes is rejected", func(t *testing.T) {
		const password = "cafés-münchens"

		assertRunesBytes(t, password, service.MinPasswordRunes-1, service.MinPasswordRunes)

		_, _, err := service.ValidateRegistration(validEmail, validUsername, password)
		assertInvalidInput(t, err, true)
	})

	// The maximum needs the same treatment in reverse. Every ASCII case has
	// bytes == runes, so a rune-counted maximum would pass all of them while
	// letting an 80-byte password through to bcrypt -- which is exactly the
	// 500-instead-of-400 bug this step exists to fix.
	t.Run("40 runes over 72 bytes is rejected", func(t *testing.T) {
		password := strings.Repeat("éü", 20)

		assertRunesBytes(t, password, 40, service.MaxPasswordBytes+1)
		if utf8.RuneCountInString(password) > service.MaxPasswordBytes {
			t.Fatalf("fixture is %d runes, which must stay under the %d maximum for this test to distinguish the two units",
				utf8.RuneCountInString(password), service.MaxPasswordBytes)
		}

		_, _, err := service.ValidateRegistration(validEmail, validUsername, password)
		assertInvalidInput(t, err, true)
	})
}

// assertRunesBytes pins a fixture's shape, so a later edit that changes its
// rune or byte count turns into a clear failure here rather than a test that
// quietly stops proving what it claims to.
func assertRunesBytes(t *testing.T, password string, wantRunes, minBytes int) {
	t.Helper()

	if runes := utf8.RuneCountInString(password); runes != wantRunes {
		t.Fatalf("fixture %q is %d runes, want exactly %d", password, runes, wantRunes)
	}
	if len(password) < minBytes {
		t.Fatalf("fixture %q is %d bytes, want at least %d -- it must be multi-byte to prove anything",
			password, len(password), minBytes)
	}
}

func TestValidateRegistration_PasswordBlocklist(t *testing.T) {
	cases := []struct {
		name     string
		email    string
		username string
		password string
		wantErr  bool
	}{
		{
			name:     "exact blocklist entry",
			email:    validEmail,
			username: validUsername,
			password: "correcthorsebatterystaple",
			wantErr:  true,
		},
		{
			name:     "blocklist entry in a different case",
			email:    validEmail,
			username: validUsername,
			password: "QwertyuiopAsdfgh",
			wantErr:  true,
		},
		{
			name:     "contains the username",
			email:    "someone@example.test",
			username: "alice",
			password: "alice-alice-alice",
			wantErr:  true,
		},
		{
			name:     "contains the email local part",
			email:    "khalil@example.test",
			username: "zephyr",
			password: "khalil-is-my-name",
			wantErr:  true,
		},
		{
			name:     "contains the service name",
			email:    validEmail,
			username: "zephyr",
			password: "my-quantsim-login-secret",
			wantErr:  true,
		},
		{
			name:     "single repeated character",
			email:    validEmail,
			username: validUsername,
			password: "aaaaaaaaaaaaaaaa",
			wantErr:  true,
		},
		{
			name:     "ascending sequence",
			email:    validEmail,
			username: validUsername,
			password: "abcdefghijklmnop",
			wantErr:  true,
		},
		{
			name:     "descending sequence",
			email:    validEmail,
			username: validUsername,
			password: "ponmlkjihgfedcba",
			wantErr:  true,
		},
		// The negative cases. A blocklist that rejects good passwords is a
		// worse outcome than one that is slightly too narrow, because the
		// user has no way to tell what would satisfy it.
		{
			name:     "ordinary long passphrase passes",
			email:    validEmail,
			username: validUsername,
			password: "the-otter-sings-at-dawn",
			wantErr:  false,
		},
		{
			name:     "passphrase with a sequence inside it passes",
			email:    validEmail,
			username: validUsername,
			password: "my-abc-is-a-passphrase",
			wantErr:  false,
		},
		{
			name:     "repeated characters inside a passphrase pass",
			email:    validEmail,
			username: validUsername,
			password: "the-baaaaad-mango-tree",
			wantErr:  false,
		},
		{
			name:     "no composition rules: all lowercase letters and hyphens is fine",
			email:    validEmail,
			username: validUsername,
			password: "brittle-copper-window",
			wantErr:  false,
		},
		// A two-character email local part must not become a substring rule,
		// or every password containing "ab" would be rejected.
		{
			name:     "short email local part is not a context term",
			email:    "ab@example.test",
			username: "zephyr",
			password: "abundant-crimson-sky",
			wantErr:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := service.ValidateRegistration(tc.email, tc.username, tc.password)
			assertInvalidInput(t, err, tc.wantErr)
		})
	}
}

// TestValidateRegistration_MessagesAreUserFacing guards the property the
// handler depends on: the wrapped message is what the frontend renders, so
// it has to name the field and read as an instruction rather than a
// diagnostic. It must also never echo the rejected password back.
func TestValidateRegistration_MessagesAreUserFacing(t *testing.T) {
	const secret = "shortpw"

	cases := []struct {
		name     string
		email    string
		username string
		password string
		wantWord string
	}{
		{"email", "x", validUsername, validPassword, "email"},
		{"username", validEmail, "ab", validPassword, "username"},
		{"password", validEmail, validUsername, secret, "password"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := service.ValidateRegistration(tc.email, tc.username, tc.password)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantWord) {
				t.Fatalf("message %q does not mention %q", err.Error(), tc.wantWord)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("message echoes the submitted password: %q", err.Error())
			}
		})
	}
}

func assertInvalidInput(t *testing.T, err error, wantErr bool) {
	t.Helper()

	if wantErr {
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		// Every rejection has to be matchable with errors.Is, or the handler
		// maps it to 500 instead of 400.
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Fatalf("error %v does not wrap ErrInvalidInput", err)
		}
		return
	}

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
