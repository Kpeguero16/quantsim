package service

// Internal tests. These assert invariants of the embedded blocklist itself,
// which is unexported and so can't be inspected from service_test. All
// behaviour is covered externally in validate_test.go.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestBlocklistEntriesAreLongEnough is the invariant that keeps the file
// honest. An entry shorter than the password minimum can never match a
// password that got far enough to be checked, so it is dead weight
// masquerading as coverage.
func TestBlocklistEntriesAreLongEnough(t *testing.T) {
	for entry := range commonPasswords {
		if n := utf8.RuneCountInString(entry); n < MinPasswordRunes {
			t.Errorf("blocklist entry %q is %d runes; entries must be at least %d, or the length rule already rejects them",
				entry, n, MinPasswordRunes)
		}
	}
}

// TestBlocklistEntriesAreNormalized guards the comparison in checkBlocklist,
// which lowercases the password but looks the entry up verbatim. An entry
// with stray case or whitespace would silently never match.
func TestBlocklistEntriesAreNormalized(t *testing.T) {
	for entry := range commonPasswords {
		if entry != strings.ToLower(strings.TrimSpace(entry)) {
			t.Errorf("blocklist entry %q is not lowercased and trimmed", entry)
		}
	}
}

func TestBlocklistIsNotEmpty(t *testing.T) {
	// Not a quality bar, just a smoke test: if the //go:embed directive
	// breaks or the file is emptied, every password silently passes the
	// blocklist check and nothing else would notice.
	if len(commonPasswords) < 50 {
		t.Fatalf("blocklist has %d entries, expected the embedded file to load", len(commonPasswords))
	}
}

func TestParseBlocklistSkipsCommentsAndBlanks(t *testing.T) {
	got := parseBlocklist("# a comment\n\n  Padded-Entry-Here  \nplain-entry-here\n   \n")

	want := map[string]struct{}{
		"padded-entry-here": {},
		"plain-entry-here":  {},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %v", len(got), len(want), got)
	}
	for entry := range want {
		if _, ok := got[entry]; !ok {
			t.Errorf("missing entry %q", entry)
		}
	}
}

func TestIsSequential(t *testing.T) {
	cases := []struct {
		password string
		want     bool
	}{
		{"abcdefghijklmnop", true},
		{"ponmlkjihgfedcba", true},
		{"0123456789", true},
		{"ab", true},
		{"", false},
		{"a", false},
		{"acegik", false},
		{"abcdefgz", false},
		{"aaaa", false},
		{"the-otter-sings", false},
	}

	for _, tc := range cases {
		if got := isSequential(tc.password); got != tc.want {
			t.Errorf("isSequential(%q) = %v, want %v", tc.password, got, tc.want)
		}
	}
}

func TestIsRepeatedRune(t *testing.T) {
	cases := []struct {
		password string
		want     bool
	}{
		{"aaaaaaaaaaaaaaaa", true},
		{"1111111111111111", true},
		{"a", true},
		{"", false},
		{"aaaaaaaaaaaaaaab", false},
		{"the-otter-sings", false},
	}

	for _, tc := range cases {
		if got := isRepeatedRune(tc.password); got != tc.want {
			t.Errorf("isRepeatedRune(%q) = %v, want %v", tc.password, got, tc.want)
		}
	}
}
