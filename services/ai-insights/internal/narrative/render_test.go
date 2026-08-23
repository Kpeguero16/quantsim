package narrative_test

import (
	"math"
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
)

func TestRender_SubstitutesEachKind(t *testing.T) {
	vocab := map[string]narrative.Value{
		"pct":    {Kind: narrative.KindPercent, Num: 62.5},
		"signed": {Kind: narrative.KindSignedPercent, Num: 7.25},
		"neg":    {Kind: narrative.KindSignedPercent, Num: -12.4},
		"ratio":  {Kind: narrative.KindRatio, Num: 0.8412},
		"index":  {Kind: narrative.KindIndex, Num: 0.5313},
		"money":  {Kind: narrative.KindMoney, Num: 1750.5},
		"qty":    {Kind: narrative.KindQuantity, Num: 10},
		"count":  {Kind: narrative.KindCount, Num: 34},
		"date":   {Kind: narrative.KindDate, Text: "2026-08-14"},
		"sym":    {Kind: narrative.KindSymbol, Text: "AAPL"},
	}
	for _, tc := range []struct{ token, want string }{
		{"pct", "62.5%"},
		{"signed", "+7.3%"},
		{"neg", "-12.4%"},
		{"ratio", "0.84"},
		{"index", "0.531"},
		{"money", "1,750.50"},
		{"qty", "10"},
		{"count", "34"},
		{"date", "8/14/2026"},
		{"sym", "AAPL"},
	} {
		t.Run(tc.token, func(t *testing.T) {
			got, err := narrative.Render("["+"{"+tc.token+"}"+"]", vocab)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != "["+tc.want+"]" {
				t.Errorf("rendered %q, want %q", got, "["+tc.want+"]")
			}
		})
	}
}

// Parity with frontend/src/format.ts. These expectations are not guesses --
// each was produced by running that file's exact toLocaleString calls under
// node and pasting the result. The frontend and this service format the same
// figure in two languages, and where they disagree the user sees one number
// written two ways on one screen (SPEC.md §2.4).
func TestRender_MatchesTheFrontendFormatter(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    narrative.Value
		want string // verbatim output of format.ts for the same input
	}{
		{"formatPrice 1750.5", narrative.Value{Kind: narrative.KindMoney, Num: 1750.5}, "1,750.50"},
		{"formatPrice 1234567.891", narrative.Value{Kind: narrative.KindMoney, Num: 1234567.891}, "1,234,567.89"},
		{"formatPrice 0", narrative.Value{Kind: narrative.KindMoney, Num: 0}, "0.00"},
		{"formatQuantity 10", narrative.Value{Kind: narrative.KindQuantity, Num: 10}, "10"},
		{"formatQuantity 10.5", narrative.Value{Kind: narrative.KindQuantity, Num: 10.5}, "10.5"},
		{"formatQuantity 1234.56789", narrative.Value{Kind: narrative.KindQuantity, Num: 1234.56789}, "1,234.5679"},
		{"formatDate 2026-08-14", narrative.Value{Kind: narrative.KindDate, Text: "2026-08-14"}, "8/14/2026"},
		{"formatDate 2026-01-02", narrative.Value{Kind: narrative.KindDate, Text: "2026-01-02"}, "1/2/2026"},
		{"formatDate 2026-12-31", narrative.Value{Kind: narrative.KindDate, Text: "2026-12-31"}, "12/31/2026"},

		// Percentages, added in Step 22 when format.ts finally grew a formatter
		// for them. Until then this table could only pin money, quantities and
		// dates, and the percent rule this file defines was guarded in one
		// direction only: the frontend's table would catch the frontend drifting,
		// and nothing at all would catch THIS file drifting. Changing `places`
		// here from 1 to 2 used to leave every test in both languages green while
		// the two silently disagreed on screen.
		{"formatPercent 33.333", narrative.Value{Kind: narrative.KindPercent, Num: 33.333}, "33.3%"},
		{"formatPercent 12.35", narrative.Value{Kind: narrative.KindPercent, Num: 12.35}, "12.4%"},
		{"formatPercent -99.85", narrative.Value{Kind: narrative.KindPercent, Num: -99.85}, "-99.9%"},
		{"formatPercent 0", narrative.Value{Kind: narrative.KindPercent, Num: 0}, "0.0%"},
		// No thousands separator, unlike money -- and that asymmetry is the whole
		// reason format.ts has to pass useGrouping: false, which is not its
		// default and is not what formatPrice does.
		{"formatPercent 1234.5", narrative.Value{Kind: narrative.KindPercent, Num: 1234.5}, "1234.5%"},
		// A value too small to display, but negative. Both languages print the
		// signed zero rather than swallowing the sign.
		{"formatPercent -0.04", narrative.Value{Kind: narrative.KindPercent, Num: -0.04}, "-0.0%"},
		{"formatPercent negative zero", narrative.Value{Kind: narrative.KindPercent, Num: math.Copysign(0, -1)}, "-0.0%"},

		{"formatSignedPercent 12.35", narrative.Value{Kind: narrative.KindSignedPercent, Num: 12.35}, "+12.4%"},
		{"formatSignedPercent -12.35", narrative.Value{Kind: narrative.KindSignedPercent, Num: -12.35}, "-12.4%"},
		// The sign is decided on the raw value, not the rounded one: a positive
		// figure too small to show still reads as a gain. format.ts matches by
		// testing `pct > 0` rather than the formatted string.
		{"formatSignedPercent 0.04", narrative.Value{Kind: narrative.KindSignedPercent, Num: 0.04}, "+0.0%"},
		// An exact zero is a measurement, not a gain -- a portfolio that matched
		// its benchmark exactly. It carries no sign in either language.
		{"formatSignedPercent 0", narrative.Value{Kind: narrative.KindSignedPercent, Num: 0}, "0.0%"},

		// Ratios and the concentration index, added alongside format.ts's
		// formatRatio and formatIndex. These precisions are where the two
		// languages CAN come apart: a plain toLocaleString on the raw value
		// reads the shortest decimal form and renders -9.995 as "-10.00",
		// where the scale-then-round below sees the double sitting just under
		// the halfway point and renders "-9.99". format.ts ports this
		// function rather than approximating it, and these cases are what say
		// so from this side.
		{"formatRatio -9.995", narrative.Value{Kind: narrative.KindRatio, Num: -9.995}, "-9.99"},
		{"formatRatio -1.005", narrative.Value{Kind: narrative.KindRatio, Num: -1.005}, "-1.00"},
		{"formatRatio 2.675", narrative.Value{Kind: narrative.KindRatio, Num: 2.675}, "2.68"},
		{"formatRatio a computed sharpe", narrative.Value{Kind: narrative.KindRatio, Num: 5.298553258534417}, "5.30"},
		{"formatRatio 0", narrative.Value{Kind: narrative.KindRatio, Num: 0}, "0.00"},
		{"formatRatio -0.004", narrative.Value{Kind: narrative.KindRatio, Num: -0.004}, "-0.00"},
		{"formatRatio 1234.567", narrative.Value{Kind: narrative.KindRatio, Num: 1234.567}, "1234.57"},
		{"formatIndex -8.1885", narrative.Value{Kind: narrative.KindIndex, Num: -8.1885}, "-8.188"},
		{"formatIndex a computed hhi", narrative.Value{Kind: narrative.KindIndex, Num: 0.3333333333333333}, "0.333"},
		{"formatIndex 0.0005", narrative.Value{Kind: narrative.KindIndex, Num: 0.0005}, "0.001"},
		{"formatIndex -0.0004", narrative.Value{Kind: narrative.KindIndex, Num: -0.0004}, "-0.000"},
		{"formatIndex 1", narrative.Value{Kind: narrative.KindIndex, Num: 1}, "1.000"},
		{"formatIndex 1234.5678", narrative.Value{Kind: narrative.KindIndex, Num: 1234.5678}, "1234.568"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := narrative.Render("{x}", map[string]narrative.Value{"x": tc.v})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != tc.want {
				t.Errorf("Go renders %q, format.ts renders %q -- the same figure would appear "+
					"two ways on one screen", got, tc.want)
			}
		})
	}
}

// An unknown token must be an error, never a passthrough. The validator
// rejects these earlier; this is the seam that actually writes to the page, and
// literal braces reaching a reader is the one outcome worse than no narrative.
func TestRender_UnknownTokenIsAnErrorNamingIt(t *testing.T) {
	_, err := narrative.Render("your drawdown was {risk.nonsense} last month", map[string]narrative.Value{})
	if err == nil {
		t.Fatal("an unknown token rendered without error")
	}
	if !strings.Contains(err.Error(), "risk.nonsense") {
		t.Errorf("error %q does not name the offending token", err)
	}
}

func TestRender_RepeatedAndAdjacentTokens(t *testing.T) {
	vocab := map[string]narrative.Value{
		"a": {Kind: narrative.KindSymbol, Text: "AAPL"},
		"b": {Kind: narrative.KindSymbol, Text: "MSFT"},
	}
	got, err := narrative.Render("{a}{b} and {a} again", vocab)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "AAPLMSFT and AAPL again" {
		t.Errorf("got %q", got)
	}
}

func TestRender_LeavesNoBracesBehind(t *testing.T) {
	got, err := narrative.Render("{a} and {b}", map[string]narrative.Value{
		"a": {Kind: narrative.KindCount, Num: 1},
		"b": {Kind: narrative.KindCount, Num: 2},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.ContainsAny(got, "{}") {
		t.Errorf("braces survived substitution: %q", got)
	}
}

// A Value that reached the renderer without a kind must not format through
// whatever the default branch happens to be.
func TestRender_KindlessValueIsAnError(t *testing.T) {
	if _, err := narrative.Render("{a}", map[string]narrative.Value{"a": {Num: 1}}); err == nil {
		t.Fatal("a Value with no kind rendered silently")
	}
}

func TestRender_DraftWithNoTokensIsUnchanged(t *testing.T) {
	const draft = "Your portfolio held steady over the period."
	got, err := narrative.Render(draft, map[string]narrative.Value{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != draft {
		t.Errorf("got %q, want it unchanged", got)
	}
}

// Halfway cases round away from zero, which is the browser's rule and not
// Go's. strconv.FormatFloat would send an exact 7.25 to "7.2" (half to even)
// where every JavaScript formatter sends it to "7.3" (half away from zero).
// The expectations below were produced by running toLocaleString and toFixed
// under node, and the divergence was found by the parity test above failing.
//
// This matters because Step 22 has no percent formatter to copy: it will
// reach for the obvious one-liner, and the obvious one-liner rounds this way.
func TestRender_HalfwayCasesRoundAwayFromZeroLikeTheBrowser(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    narrative.Value
		want string
	}{
		{"positive half", narrative.Value{Kind: narrative.KindPercent, Num: 7.25}, "7.3%"},
		{"negative half", narrative.Value{Kind: narrative.KindPercent, Num: -7.25}, "-7.3%"},
		{"positive half, signed", narrative.Value{Kind: narrative.KindSignedPercent, Num: 18.25}, "+18.3%"},
		{"not a halfway case", narrative.Value{Kind: narrative.KindPercent, Num: 0.125}, "0.1%"},
		{"already exact", narrative.Value{Kind: narrative.KindPercent, Num: 2.5}, "2.5%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := narrative.Render("{x}", map[string]narrative.Value{"x": tc.v})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q -- Go's half-to-even rounding has leaked back in", got, tc.want)
			}
		})
	}
}

// Zero renders as a measurement, not as an absence. An all-cash portfolio's
// concentration and a flat curve's volatility are both real readings of zero,
// and prose that omitted them would be describing a different portfolio.
func TestRender_ZeroIsAFigureNotAnAbsence(t *testing.T) {
	for _, tc := range []struct {
		kind narrative.Kind
		want string
	}{
		{narrative.KindPercent, "0.0%"},
		{narrative.KindSignedPercent, "0.0%"},
		{narrative.KindRatio, "0.00"},
		{narrative.KindIndex, "0.000"},
		{narrative.KindCount, "0"},
	} {
		got, err := narrative.Render("{x}", map[string]narrative.Value{"x": {Kind: tc.kind, Num: 0}})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if got != tc.want {
			t.Errorf("kind %d rendered zero as %q, want %q", tc.kind, got, tc.want)
		}
	}
}

// A drawdown is a magnitude, not a signed change. pkg/portfoliomath reports
// "the largest peak-to-trough decline ... as a positive percentage", so a 1.7%
// fall arrives as 1.7. Rendering it as a signed percentage would print "+1.7%"
// for a loss, which reads as a gain -- and the prose around it would say
// "fell", leaving the sentence contradicting its own figure.
//
// Found in the manual pass, against a real report. The unit fixture had used
// -12.4, a value no code path can produce, and it made the wrong kind look
// right.
func TestRender_DrawdownIsAMagnitudeAndCarriesNoPlusSign(t *testing.T) {
	got, err := narrative.Render("{risk.max_drawdown_pct}", narrative.Placeholders(realShapedReport()))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.HasPrefix(got, "+") {
		t.Errorf("drawdown rendered as %q; a decline must not be signed like a gain", got)
	}
	if got != "1.7%" {
		t.Errorf("drawdown rendered as %q, want 1.7%%", got)
	}
}
