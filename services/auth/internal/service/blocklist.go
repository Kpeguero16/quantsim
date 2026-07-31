package service

import (
	_ "embed"
	"fmt"
	"strings"
	"unicode/utf8"
)

// blocklistFile holds common and weak passwords, one per line. Only entries
// of MinPasswordRunes or longer earn their place -- anything shorter is
// already rejected by the length rule, so a generic top-10k list would be
// almost entirely dead weight. See blocklist.txt for what it is and isn't.
//
//go:embed blocklist.txt
var blocklistFile string

// commonPasswords is the parsed blocklist, lowercased for comparison.
var commonPasswords = parseBlocklist(blocklistFile)

// serviceTerms are the context-specific words NIST SP 800-63B 3.1.1.2 names
// directly: "the name of the service" and derivatives. The submitted
// username and email local part are added per-request.
//
// Kept deliberately short. Every term here is matched as a substring against
// every password, so a generic English word added casually ("portfolio",
// "market") would start rejecting legitimate passphrases.
var serviceTerms = []string{"quantsim", "trading"}

// minContextTerm is the shortest context term worth substring-matching.
// Without it, an email local part of "a" would reject every password
// containing the letter a. Usernames are always at least MinUsernameRunes,
// so this only ever filters short local parts.
const minContextTerm = 3

func parseBlocklist(raw string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, line := range strings.Split(raw, "\n") {
		entry := strings.ToLower(strings.TrimSpace(line))
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		set[entry] = struct{}{}
	}
	return set
}

// checkBlocklist compares a prospective password against known-weak values:
// the embedded list, context-specific terms, and trivial patterns. NIST SP
// 800-63B 3.1.1.2 makes this a SHALL, and it is what stops a length minimum
// from being defeated by "aaaaaaaaaaaaaaaa".
//
// The password is lowercased for comparison but never trimmed -- leading and
// trailing spaces are legitimate password characters, and modifying the
// secret before hashing it is how distinct passwords end up colliding.
//
// Deliberately not an online lookup against Have I Been Pwned. Its
// k-anonymity API is the stronger control and the right eventual answer, but
// it puts a third-party network call on the registration path, with its own
// latency, failure mode, and a fail-open/fail-closed decision that deserves
// deciding on purpose. Deferred in SPEC.md 7.
func checkBlocklist(password, email, username string) error {
	lower := strings.ToLower(password)

	if _, blocked := commonPasswords[lower]; blocked {
		return fmt.Errorf("%w: password is too common -- choose something less predictable", ErrInvalidInput)
	}

	for _, term := range contextTerms(email, username) {
		if strings.Contains(lower, term) {
			return fmt.Errorf("%w: password must not contain your username, your email address, or the name of this service", ErrInvalidInput)
		}
	}

	if isRepeatedRune(password) || isSequential(password) {
		return fmt.Errorf("%w: password must not be a single repeated character or a simple sequence", ErrInvalidInput)
	}

	return nil
}

// contextTerms returns the words this particular user must not build a
// password out of: the service name, their username, and their email local
// part.
//
// Substring matching is broader than exact matching, and that is the point
// -- "alice-alice-alice" is the case the requirement names. The cost is that
// a short username appearing incidentally inside a long passphrase is also
// rejected. That is accepted friction, bounded by minContextTerm.
func contextTerms(email, username string) []string {
	terms := make([]string, 0, len(serviceTerms)+2)
	terms = append(terms, serviceTerms...)

	for _, candidate := range []string{username, emailLocalPart(email)} {
		if utf8.RuneCountInString(candidate) >= minContextTerm {
			terms = append(terms, candidate)
		}
	}

	return terms
}

// emailLocalPart returns everything before the final "@", or "" if there is
// none. The email reaching here has already been validated, so the simple
// split is safe.
func emailLocalPart(email string) string {
	if at := strings.LastIndex(email, "@"); at > 0 {
		return email[:at]
	}
	return ""
}

// isRepeatedRune reports whether a password is one character repeated:
// "aaaaaaaaaaaaaaaa" is 16 characters and worthless.
func isRepeatedRune(password string) bool {
	runes := []rune(password)
	if len(runes) == 0 {
		return false
	}
	for _, r := range runes[1:] {
		if r != runes[0] {
			return false
		}
	}
	return true
}

// isSequential reports whether a password is a single unbroken run of
// consecutive characters, ascending or descending -- "abcdefghijklmnop",
// "9876543210987654". Named keyboard walks like "qwertyuiopasdfgh" aren't
// arithmetic runs and live in blocklist.txt instead.
func isSequential(password string) bool {
	runes := []rune(password)
	if len(runes) < 2 {
		return false
	}

	step := runes[1] - runes[0]
	if step != 1 && step != -1 {
		return false
	}
	for i := 2; i < len(runes); i++ {
		if runes[i]-runes[i-1] != step {
			return false
		}
	}
	return true
}
