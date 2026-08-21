package narrative_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
)

func vocab() map[string]narrative.Value {
	return map[string]narrative.Value{
		"risk.max_drawdown_pct":        {Kind: narrative.KindPercent, Num: 12.4},
		"risk.largest_position_pct":    {Kind: narrative.KindPercent, Num: 62.5},
		"behavior.trade_count":         {Kind: narrative.KindCount, Num: 34},
		"risk.largest_position_symbol": {Kind: narrative.KindSymbol, Text: "AAPL"},
	}
}

func TestValidate_AcceptsACleanDraft(t *testing.T) {
	draft := "Your deepest fall from a high was {risk.max_drawdown_pct}, and " +
		"{risk.largest_position_symbol} alone accounts for {risk.largest_position_pct} of the account."
	if err := narrative.Validate(draft, vocab()); err != nil {
		t.Fatalf("a clean draft was rejected: %v", err)
	}
}

// THE test for this step. A draft carrying a number that is CORRECT -- the
// true drawdown, formatted exactly as the renderer would format it -- must
// still be rejected.
//
// Correctness is not the criterion; provenance is. A validator that lets this
// through has quietly become a whitelist, and the whole guarantee of SPEC 2.2
// is gone: the next figure to arrive that way will be wrong, and it will look
// exactly like this one.
func TestValidate_RejectsACorrectNumberBecauseProvenanceIsTheCriterion(t *testing.T) {
	// 12.4% is precisely what {risk.max_drawdown_pct} renders to. Unsigned:
	// a drawdown is a magnitude (see render_test.go).
	rendered, err := narrative.Render("{risk.max_drawdown_pct}", vocab())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if rendered != "12.4%" {
		t.Fatalf("fixture drifted: rendered %q", rendered)
	}

	draft := "Your deepest fall from a high was " + rendered + " over the period."
	if err := narrative.Validate(draft, vocab()); err == nil {
		t.Fatal("a correct figure was accepted -- the validator is a whitelist, not a provenance check")
	}
}

func TestValidate_RejectsADigitInAnyPosition(t *testing.T) {
	for _, tc := range []struct{ name, draft string }{
		{"first character", "7 was the number of trades."},
		{"last character", "Your account held steady all 7"},
		{"mid-word", "Your S7P exposure is heavy."},
		{"inside a decimal", "A fall of 12.4 over the window."},
		{"inside a token name", "You hold {risk.positions.AAPL2.weight_pct} of it."},
		{"a lone zero", "Your realised loss was 0."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := narrative.Validate(tc.draft, vocab()); err == nil {
				t.Errorf("accepted a draft containing a digit: %q", tc.draft)
			}
		})
	}
}

func TestValidate_RejectsNumberWordsAndBareUnits(t *testing.T) {
	for _, word := range []string{
		"zero", "one", "two", "three", "nine", "ten", "twelve", "twenty", "ninety",
		"hundred", "thousand", "million", "billion",
		"half", "third", "quarter", "fifth", "tenth", "dozen",
		"twice", "double", "triple",
		"percent", "percentage", "dollars", "cents", "USD",
	} {
		t.Run(word, func(t *testing.T) {
			draft := "Your account moved by about " + word + " over the window."
			if err := narrative.Validate(draft, vocab()); err == nil {
				t.Errorf("accepted the number word %q", word)
			}
		})
	}
}

// Word boundaries, not substrings. Rejecting legitimate prose is a cost this
// design accepts, but only where a number word is actually being used.
func TestValidate_DoesNotRejectWordsThatMerelyContainOne(t *testing.T) {
	for _, phrase := range []string{
		"Your headquarters exposure is concentrated.",
		"The position was onerous to hold.",
		"Nothing here is atonal.",
		"Your holdings are somewhat lopsided.",
	} {
		if err := narrative.Validate(phrase, vocab()); err != nil {
			t.Errorf("rejected legitimate prose %q: %v", phrase, err)
		}
	}
}

// The renderer supplies the unit along with the figure, so a unit symbol in
// the draft is either a duplicate or a figure the model meant to write itself.
func TestValidate_RejectsBareUnitSymbols(t *testing.T) {
	for _, draft := range []string{
		"Your drawdown was {risk.max_drawdown_pct}% over the window.",
		"You are down ${risk.largest_position_pct} on the position.",
	} {
		if err := narrative.Validate(draft, vocab()); err == nil {
			t.Errorf("accepted a bare unit symbol: %q", draft)
		}
	}
}

func TestValidate_RejectsAnUnknownTokenAndNamesIt(t *testing.T) {
	err := narrative.Validate("Your {risk.nonsense} is high.", vocab())
	if err == nil {
		t.Fatal("accepted an unknown token")
	}
	if !strings.Contains(err.Error(), "risk.nonsense") {
		t.Errorf("error %q does not name the offending token", err)
	}
}

func TestValidate_RejectsMalformedAndEmptyDrafts(t *testing.T) {
	for _, tc := range []struct{ name, draft string }{
		{"unclosed brace", "Your {risk.max_drawdown_pct is deep."},
		{"nested brace", "Your {risk.{max}_drawdown_pct} is deep."},
		{"empty", ""},
		{"whitespace only", "   \n\t "},
		{"over the section cap", strings.Repeat("long prose. ", 5000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := narrative.Validate(tc.draft, vocab()); err == nil {
				t.Errorf("accepted a malformed draft: %s", tc.name)
			}
		})
	}
}

// The retry (SPEC 2.5) quotes the offending fragment back to the model, so the
// failure has to carry enough to build that message without re-scanning.
func TestValidate_FailureCarriesTheCheckAndTheFragment(t *testing.T) {
	err := narrative.Validate("Your drawdown was 12.4 over the window.", vocab())
	var v *narrative.ValidationError
	if !errors.As(err, &v) {
		t.Fatalf("got %T, want a *ValidationError", err)
	}
	if v.Check == "" {
		t.Error("failure names no check")
	}
	if v.Fragment == "" {
		t.Error("failure quotes no fragment")
	}
	if !strings.Contains(err.Error(), v.Fragment) {
		t.Errorf("Error() %q does not include the fragment %q", err, v.Fragment)
	}
}

func TestValidateSections_AppliesTheTotalCap(t *testing.T) {
	// Each section is individually under its cap; together they are not.
	section := strings.Repeat("prose ", 190)
	sections := map[string]string{"risk": section, "benchmarking": section, "behavior": section}

	if err := narrative.Validate(section, vocab()); err != nil {
		t.Fatalf("fixture is wrong -- one section already exceeds its own cap: %v", err)
	}
	if err := narrative.ValidateSections(sections, vocab()); err == nil {
		t.Error("three sections that individually fit were accepted past the total cap")
	}
}

func TestValidateSections_NamesTheFailingSection(t *testing.T) {
	sections := map[string]string{
		"risk":         "All good here with {risk.max_drawdown_pct}.",
		"benchmarking": "You beat the index by 4 points.",
	}
	err := narrative.ValidateSections(sections, vocab())
	if err == nil {
		t.Fatal("accepted a section containing a digit")
	}
	if !strings.Contains(err.Error(), "benchmarking") {
		t.Errorf("error %q does not name the failing section", err)
	}
}

// Words that assert a count without spelling a numeral. The manual pass
// produced "a couple of names" for a two-holding portfolio -- true, checkable,
// and still a figure the model wrote itself rather than one it was handed,
// which is precisely what this check exists to stop.
func TestValidate_RejectsCountWordsThatAreNotNumerals(t *testing.T) {
	for _, word := range []string{"couple", "pair"} {
		t.Run(word, func(t *testing.T) {
			draft := "The invested money sits in a " + word + " of names."
			if err := narrative.Validate(draft, vocab()); err == nil {
				t.Errorf("accepted %q, which asserts a count", word)
			}
		})
	}
	// Vague quantifiers are not the failure being guarded against: they assert
	// nothing specific, and banning them would cost drafts for no gain. Nor is
	// "both", which refers back to things already named by their own
	// placeholders -- it was banned for exactly one run of the manual pass and
	// cost a whole generation for "the two figures are both small".
	for _, word := range []string{"few", "several", "many"} {
		t.Run(word+" is allowed", func(t *testing.T) {
			if err := narrative.Validate("The account holds a "+word+" of names.", vocab()); err != nil {
				t.Errorf("rejected the vague quantifier %q: %v", word, err)
			}
		})
	}
	if err := narrative.Validate("Volatility and drawdown are both small.", vocab()); err != nil {
		t.Errorf("rejected \"both\", which refers back to figures already named: %v", err)
	}
}

// The rejection has to show the phrase, not the bare word. A log line saying
// only "three" cannot tell you whether the model was counting trades (which
// has a token) or benchmarks (which did not until the manual pass added one),
// and those want different fixes.
func TestValidate_ANumberWordFailureQuotesTheSurroundingPhrase(t *testing.T) {
	err := narrative.Validate("Sharpe ratios are negative for all three of the series shown.", vocab())
	var v *narrative.ValidationError
	if !errors.As(err, &v) {
		t.Fatalf("got %T, want *ValidationError", err)
	}
	if v.Fragment == "three" || len(v.Fragment) < len("three")+5 {
		t.Errorf("fragment is %q -- too little context to tell what was being counted", v.Fragment)
	}
}
