package narrative

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// reportDateFormat is the layout the report carries dates in
// (service/insights.go's dateFormat). Values arrive as strings in this shape
// and are re-rendered for a reader.
const reportDateFormat = "2006-01-02"

// Render substitutes every {token} in a draft with its figure from the
// vocabulary, formatted by kind.
//
// This is the only place a figure becomes text, which is what makes SPEC.md
// §2.2's guarantee mean anything: the number a user reads came out of strconv,
// applied to a struct field, here.
//
// An unknown token is an error rather than a passthrough. The validator
// rejects those earlier, so reaching this branch means the two disagreed --
// and literal braces on a reader's screen is the one outcome worse than
// showing no narrative at all.
func Render(draft string, vocab map[string]Value) (string, error) {
	var out strings.Builder
	rest := draft

	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		close := strings.IndexByte(rest[open:], '}')
		if close < 0 {
			// An unterminated brace is malformed rather than absent: writing
			// the remainder through would put a bare "{" on the page.
			return "", fmt.Errorf("narrative: unterminated placeholder in draft at %q", truncate(rest[open:]))
		}
		close += open

		token := rest[open+1 : close]
		v, ok := vocab[token]
		if !ok {
			return "", fmt.Errorf("narrative: unknown placeholder %q", token)
		}
		formatted, err := format(v)
		if err != nil {
			return "", fmt.Errorf("narrative: placeholder %q: %w", token, err)
		}

		out.WriteString(rest[:open])
		out.WriteString(formatted)
		rest = rest[close+1:]
	}
}

// format turns one value into the string a reader sees.
//
// Money, quantities and dates deliberately match frontend/src/format.ts --
// formatPrice, formatQuantity and formatDate respectively -- because the same
// figure appears in the JSON report the frontend formats itself and inside the
// sentence this service formats. Where the two disagree, one number appears
// two ways on one screen.
//
// Percentages have no counterpart there: format.ts has no percent formatter at
// all. This is therefore the convention, and Step 22 has to follow it rather
// than invent a second one.
//
// Every numeric kind rounds halfway cases AWAY FROM ZERO, via roundHalfAway,
// rather than using strconv's own rounding. That is not a stylistic
// preference. Go's FormatFloat rounds a halfway case to even, and every
// JavaScript formatter the frontend would reach for -- toFixed, toLocaleString,
// Intl.NumberFormat -- rounds it away from zero. An exact 7.25 renders "7.2"
// under Go's rule and "7.3" under the browser's, and the parity test in this
// package caught precisely that. Matching the browser is what lets Step 22
// format a figure with the obvious one-liner and still agree with the sentence
// beside it.
func format(v Value) (string, error) {
	switch v.Kind {
	case KindPercent:
		return decimal(v.Num, 1) + "%", nil

	case KindSignedPercent:
		s := decimal(v.Num, 1)
		if v.Num > 0 {
			s = "+" + s
		}
		return s + "%", nil

	case KindRatio:
		return decimal(v.Num, 2), nil

	case KindIndex:
		return decimal(v.Num, 3), nil

	case KindMoney:
		return group(decimal(v.Num, 2)), nil

	case KindQuantity:
		// Up to four places with trailing zeros trimmed, so a round holding
		// reads as "10" rather than "10.0000" -- formatQuantity's rule, and
		// the backend's quantityScale behind it.
		return group(trimTrailingZeros(decimal(v.Num, 4))), nil

	case KindCount:
		return group(decimal(v.Num, 0)), nil

	case KindDate:
		// Read off the date's own calendar fields. The report already carries
		// a calendar date rather than an instant, so there is no zone to
		// convert through -- which is the same conclusion format.ts reaches
		// from the other direction with timeZone: 'UTC'.
		d, err := time.Parse(reportDateFormat, v.Text)
		if err != nil {
			return "", fmt.Errorf("value %q is not a date", v.Text)
		}
		return fmt.Sprintf("%d/%d/%d", int(d.Month()), d.Day(), d.Year()), nil

	case KindSymbol:
		return v.Text, nil

	default:
		return "", fmt.Errorf("value has no kind")
	}
}

// decimal formats to a fixed number of places, rounding halfway cases away
// from zero so the result matches what a browser would print. See format's
// comment for why that matters.
func decimal(v float64, places int) string {
	return strconv.FormatFloat(roundHalfAway(v, places), 'f', places, 64)
}

// roundHalfAway rounds to `places` decimals, sending an exact .5 away from
// zero -- JavaScript's rule, and not Go's.
func roundHalfAway(v float64, places int) float64 {
	scale := math.Pow(10, float64(places))
	scaled := v * scale
	// math.Round already rounds half away from zero; the multiply is what
	// moves the halfway case onto the integer boundary where it can see it.
	return math.Round(scaled) / scale
}

// group inserts thousands separators into an already-formatted decimal,
// matching toLocaleString('en-US'). Written out rather than pulled from
// golang.org/x/text, which would be a new module dependency for one comma.
func group(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	intPart, frac := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, frac = s[:dot], s[dot:]
	}

	var b strings.Builder
	for i, digit := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	return sign + b.String() + frac
}

func trimTrailingZeros(s string) string {
	if !strings.ContainsRune(s, '.') {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// truncate bounds a quoted fragment, snapping to a rune boundary so the result
// is always valid UTF-8 -- see around() in validate.go for why that matters.
func truncate(s string) string {
	const max = 40
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "..."
}
