package narrative_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative/mock"
)

const cleanDraft = `{"risk":"Your deepest fall was {risk.max_drawdown_pct}.",
	"benchmarking":"You held {risk.largest_position_symbol} throughout.",
	"behavior":"You traded {behavior.trade_count} times."}`

func TestGenerate_CleanFirstDraftRendersInOneCall(t *testing.T) {
	g := &mock.Generator{Drafts: []string{cleanDraft}}

	got, err := narrative.Generate(context.Background(), g, narrative.Prompt{}, vocab())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if g.Calls() != 1 {
		t.Errorf("made %d calls, want exactly 1", g.Calls())
	}
	if got["risk"] != "Your deepest fall was 12.4%." {
		t.Errorf("risk rendered as %q", got["risk"])
	}
	if got["behavior"] != "You traded 34 times." {
		t.Errorf("behavior rendered as %q", got["behavior"])
	}
}

func TestGenerate_DirtyDraftIsRetriedOnceAndTheViolationIsQuotedBack(t *testing.T) {
	dirty := `{"risk":"Your deepest fall was 12.4 over the window.","benchmarking":"x","behavior":"y"}`
	g := &mock.Generator{Drafts: []string{dirty, cleanDraft}}

	got, err := narrative.Generate(context.Background(), g, narrative.Prompt{User: "original"}, vocab())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if g.Calls() != 2 {
		t.Fatalf("made %d calls, want exactly 2", g.Calls())
	}
	if got["risk"] != "Your deepest fall was 12.4%." {
		t.Errorf("risk rendered as %q", got["risk"])
	}

	retry := g.Prompts[1].User
	if !strings.Contains(retry, "original") {
		t.Error("the retry dropped the original instructions")
	}
	if !strings.Contains(retry, "12.4") {
		t.Errorf("the retry does not quote the offending fragment back: %q", retry)
	}
}

// Never three. A loop that runs once more than intended costs a real API call
// on every bad draft, and returns the same answer either way.
func TestGenerate_TwoDirtyDraftsGiveUpAfterExactlyTwoCalls(t *testing.T) {
	dirty := `{"risk":"A fall of 12.4 percent.","benchmarking":"x","behavior":"y"}`
	g := &mock.Generator{Drafts: []string{dirty, dirty, cleanDraft}}

	got, err := narrative.Generate(context.Background(), g, narrative.Prompt{}, vocab())
	if !errors.Is(err, narrative.ErrGenerationUnusable) {
		t.Fatalf("got %v, want ErrGenerationUnusable", err)
	}
	if g.Calls() != 2 {
		t.Errorf("made %d calls, want exactly 2 -- a third draft was requested", g.Calls())
	}
	if got != nil {
		t.Errorf("a failed generation returned %v, want nothing partially rendered", got)
	}
}

// A generator failure is not a rejected draft. The retry in SPEC 2.5 exists
// for a draft that broke the contract, not for an upstream that is slow or
// broken -- retrying that doubles the wait before the same failure.
func TestGenerate_AGeneratorErrorIsNotRetried(t *testing.T) {
	boom := errors.New("upstream exploded")
	g := &mock.Generator{Err: boom}

	_, err := narrative.Generate(context.Background(), g, narrative.Prompt{}, vocab())
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the generator's own error", err)
	}
	if errors.Is(err, narrative.ErrGenerationUnusable) {
		t.Error("an upstream failure was reported as an unusable draft")
	}
	if g.Calls() != 1 {
		t.Errorf("made %d calls, want exactly 1 -- an upstream failure was retried", g.Calls())
	}
}

// An unparseable draft is the model breaking the contract, so it earns the
// same single retry a dirty draft does.
func TestGenerate_UnparseableDraftIsRetriedThenGivesUp(t *testing.T) {
	g := &mock.Generator{Drafts: []string{"I'm afraid I can't do that.", cleanDraft}}

	got, err := narrative.Generate(context.Background(), g, narrative.Prompt{}, vocab())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if g.Calls() != 2 {
		t.Errorf("made %d calls, want 2", g.Calls())
	}
	if got["risk"] == "" {
		t.Error("the retry's clean draft was not used")
	}
}

// Models wrap JSON in code fences often enough that refusing it would spend a
// retry on formatting rather than on the guarantee. Stripping the fence is not
// a weakening -- every check still runs on what is inside.
func TestGenerate_AcceptsAFencedDraft(t *testing.T) {
	g := &mock.Generator{Drafts: []string{"```json\n" + cleanDraft + "\n```"}}

	got, err := narrative.Generate(context.Background(), g, narrative.Prompt{}, vocab())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if g.Calls() != 1 {
		t.Errorf("made %d calls, want 1 -- a code fence cost a retry", g.Calls())
	}
	if got["risk"] != "Your deepest fall was 12.4%." {
		t.Errorf("risk rendered as %q", got["risk"])
	}
}

// A section naming a figure the report never computed must not survive to the
// page, even though it is well-formed prose.
func TestGenerate_UnknownTokenSurvivesNeitherDraft(t *testing.T) {
	bad := `{"risk":"Your {risk.invented_metric} is high.","benchmarking":"x","behavior":"y"}`
	g := &mock.Generator{Drafts: []string{bad, bad}}

	if _, err := narrative.Generate(context.Background(), g, narrative.Prompt{}, vocab()); !errors.Is(err, narrative.ErrGenerationUnusable) {
		t.Fatalf("got %v, want ErrGenerationUnusable", err)
	}
}

// An empty vocabulary is how a fully-degraded report announces itself
// (SPEC 2.12). There is nothing to phrase, so there is nothing to pay for.
func TestGenerate_EmptyVocabularyMakesNoCallAtAll(t *testing.T) {
	g := &mock.Generator{Drafts: []string{cleanDraft}}

	got, err := narrative.Generate(context.Background(), g, narrative.Prompt{}, map[string]narrative.Value{})
	if !errors.Is(err, narrative.ErrNothingToPhrase) {
		t.Fatalf("got %v, want ErrNothingToPhrase", err)
	}
	if g.Calls() != 0 {
		t.Errorf("made %d calls for a report with nothing in it", g.Calls())
	}
	if got != nil {
		t.Errorf("returned %v, want nothing", got)
	}
}

// Validate and Render check different things, so one can pass where the other
// fails: Validate does not inspect a Value's kind, and Render refuses a
// kindless one. When that happens the whole narrative is dropped -- prose
// describing two sections while silently omitting the third is worse than
// prose describing none, because nothing on the page says a section is
// missing.
//
// This case was found by a surviving mutant: replacing Render's error with a
// `continue` left every test green while partial output escaped.
func TestGenerate_AFailedSectionTakesTheWholeNarrativeWithIt(t *testing.T) {
	v := vocab()
	v["risk.kindless"] = narrative.Value{Num: 1} // passes Validate, fails Render

	draft := `{"risk":"Your figure is {risk.kindless}.",` +
		`"benchmarking":"You held {risk.largest_position_symbol} throughout.",` +
		`"behavior":"You traded {behavior.trade_count} times."}`
	g := &mock.Generator{Drafts: []string{draft}}

	got, err := narrative.Generate(context.Background(), g, narrative.Prompt{}, v)
	if err == nil {
		t.Fatal("a section that could not render was accepted")
	}
	if got != nil {
		t.Errorf("returned %v -- the sections that did render escaped alone", got)
	}
	// Not retried: a validated draft that will not render is this package's
	// bug, not the model's, and a second draft would only hide it.
	if g.Calls() != 1 {
		t.Errorf("made %d calls, want 1", g.Calls())
	}
}
