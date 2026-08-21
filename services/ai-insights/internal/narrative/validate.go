package narrative

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ValidationError is a rejected draft, named precisely enough that the retry
// (SPEC.md §2.5) can quote the problem back to the model without re-scanning.
type ValidationError struct {
	// Check is which of the checks failed: "empty", "length", "digit",
	// "number_word", "unknown_token" or "malformed".
	Check string

	// Fragment is the offending text, or the offending token.
	Fragment string

	// Section is set only by ValidateSections.
	Section string

	detail string
}

func (e *ValidationError) Error() string {
	where := ""
	if e.Section != "" {
		where = " in the " + e.Section + " section"
	}
	return fmt.Sprintf("narrative: draft rejected%s (%s): %s: %q", where, e.Check, e.detail, e.Fragment)
}

var (
	// Any Arabic digit, anywhere. Total and mechanical, and it covers the
	// realistic failure: a model copying a figure it was shown.
	//
	// It also means a placeholder token may not contain a digit, which rules
	// out a ticker with a numeral in it. Nothing on QuantSim's watchlist has
	// one (AAPL, AMZN, GOOGL, MSFT, NVDA, QQQ, SPY, TSLA), and the day one
	// does, the fix is to exempt token interiors -- not to weaken the check.
	digitPattern = regexp.MustCompile(`[0-9]`)

	// Unit symbols. The renderer supplies these with the figure, so one here
	// is either a duplicate ("{x}%" renders "-12.4%%") or a hand-written
	// figure.
	unitSymbolPattern = regexp.MustCompile(`[%$]`)

	bannedWordPattern = regexp.MustCompile(`(?i)\b(` + strings.Join(bannedWords, "|") + `)\b`)

	// A token is everything between braces. Deliberately excludes braces
	// inside, so a nested "{a{b}" is read as the token "a{b" and fails the
	// unknown-token check rather than being silently unwrapped.
	tokenPattern = regexp.MustCompile(`\{([^{}]*)\}`)
)

// Validate applies SPEC.md §2.5's three checks plus the caps to one section's
// draft.
//
// The checks run in a fixed order -- emptiness, length, digits, number words,
// then tokens -- so the same bad draft always reports the same reason, and the
// retry message is reproducible.
//
// Nothing is ever repaired. Stripping a stray number out of a sentence leaves
// a sentence that still parses, still reads fluently, and now claims something
// its author did not write. A rejected draft costs a retry; a repaired one is
// undetectable. This is the same rule, for the same reason, as the
// reconciliation invariant in SPEC.md §2.12.
func Validate(draft string, vocab map[string]Value) error {
	if strings.TrimSpace(draft) == "" {
		return &ValidationError{Check: "empty", detail: "the draft is empty", Fragment: draft}
	}
	if len(draft) > MaxSectionChars {
		return &ValidationError{
			Check:    "length",
			detail:   fmt.Sprintf("the draft is %d characters, over the %d cap", len(draft), MaxSectionChars),
			Fragment: truncate(draft),
		}
	}

	if loc := digitPattern.FindStringIndex(draft); loc != nil {
		return &ValidationError{
			Check:    "digit",
			detail:   "a figure must be written as a placeholder, never as a numeral",
			Fragment: around(draft, loc[0]),
		}
	}
	if loc := unitSymbolPattern.FindStringIndex(draft); loc != nil {
		return &ValidationError{
			Check:    "digit",
			detail:   "the unit is supplied with the figure, so it must not be written in the prose",
			Fragment: around(draft, loc[0]),
		}
	}
	if loc := bannedWordPattern.FindStringIndex(draft); loc != nil {
		// The surrounding phrase, not the bare word. A log line saying only
		// "three" cannot tell you whether the model was counting trades (which
		// has a token) or benchmarks (which does not), and those want
		// different fixes. The retry message reads better for the same reason.
		return &ValidationError{
			Check:    "number_word",
			detail:   "a quantity must be written as a placeholder, never spelled out",
			Fragment: around(draft, loc[0]),
		}
	}

	// An unmatched brace is malformed rather than merely unknown: it means the
	// draft's token structure cannot be read at all.
	if strings.Count(draft, "{") != strings.Count(draft, "}") {
		return &ValidationError{
			Check:    "malformed",
			detail:   "a placeholder is unterminated",
			Fragment: truncate(draft),
		}
	}
	for _, m := range tokenPattern.FindAllStringSubmatch(draft, -1) {
		if _, ok := vocab[m[1]]; !ok {
			return &ValidationError{
				Check:    "unknown_token",
				detail:   "no such figure was offered",
				Fragment: m[1],
			}
		}
	}
	// Braces that the token pattern could not read -- "{a{b}" leaves a stray
	// "{a" behind. Counting alone would not catch it, since the counts match.
	if leftover := tokenPattern.ReplaceAllString(draft, ""); strings.ContainsAny(leftover, "{}") {
		return &ValidationError{
			Check:    "malformed",
			detail:   "a placeholder is nested or malformed",
			Fragment: truncate(draft),
		}
	}

	return nil
}

// ValidateSections applies Validate to each section and the total cap across
// all of them.
//
// Sections are checked in a stable order so that a draft failing in two places
// reports the same one every time.
func ValidateSections(sections map[string]string, vocab map[string]Value) error {
	names := make([]string, 0, len(sections))
	total := 0
	for name, draft := range sections {
		names = append(names, name)
		total += len(draft)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := Validate(sections[name], vocab); err != nil {
			var v *ValidationError
			if ok := asValidationError(err, &v); ok {
				v.Section = name
				return v
			}
			return err
		}
	}

	if total > MaxTotalChars {
		return &ValidationError{
			Check:    "length",
			detail:   fmt.Sprintf("the narrative is %d characters, over the %d cap", total, MaxTotalChars),
			Fragment: fmt.Sprintf("%d sections", len(sections)),
		}
	}
	return nil
}

func asValidationError(err error, out **ValidationError) bool {
	v, ok := err.(*ValidationError)
	if ok {
		*out = v
	}
	return ok
}

// around quotes a short window of text either side of an offence, so the retry
// message shows the model where it slipped rather than only what it wrote.
//
// The window is snapped to rune boundaries. This fragment is interpolated into
// the RETRY PROMPT, which is JSON sent to the API, and into the log line -- a
// half-cut multi-byte rune would put a replacement character in both, exactly
// when readable diagnostics matter most.
func around(s string, i int) string {
	const pad = 20
	start := i - pad
	if start < 0 {
		start = 0
	}
	end := i + pad
	if end > len(s) {
		end = len(s)
	}
	for start > 0 && !utf8.RuneStart(s[start]) {
		start--
	}
	for end < len(s) && !utf8.RuneStart(s[end]) {
		end++
	}
	return s[start:end]
}
