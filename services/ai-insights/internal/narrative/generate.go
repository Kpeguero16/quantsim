package narrative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// Generator produces one raw draft. It is the only thing in this step that
// talks to a model.
//
// It returns the model's RAW text -- not sections, not a narrative, not
// anything rendered (plan D3). Validation, substitution and rendering all
// happen on this side of the interface, so an implementation of Generator has
// no knowledge of SPEC.md §2.2's guarantee and therefore cannot be the place
// it is weakened. A future change to the client cannot accidentally return
// something already-rendered, because the type does not allow it.
type Generator interface {
	Draft(ctx context.Context, p Prompt) (string, error)
}

// Prompt is what the model is given. Built by the llm package (SPEC.md §2.6);
// carried here so the retry can extend it.
type Prompt struct {
	System string
	User   string
}

var (
	// ErrGenerationUnusable means two drafts in a row broke the contract. The
	// endpoint degrades rather than failing (SPEC.md §2.11): every figure in
	// the report is already correct and available at the other endpoint.
	ErrGenerationUnusable = errors.New("narrative: no usable draft after a retry")

	// ErrNothingToPhrase means the report had nothing computed in it, so no
	// call was made (SPEC.md §2.12).
	ErrNothingToPhrase = errors.New("narrative: the report has no analysis to describe")
)

// sectionNames are the sections a draft may carry, matching the report's own.
var sectionNames = []string{"risk", "benchmarking", "behavior"}

// Generate asks for a draft, checks it, and renders it -- retrying exactly
// once if the model broke the contract.
//
// The retry is for a draft that broke the contract, never for an upstream that
// is slow or broken (SPEC.md §2.11). Retrying a failed call doubles the wait
// before returning the same failure, and the endpoint has a budget to keep.
//
// Nothing partially rendered ever escapes: a section that fails takes the
// whole narrative with it, because prose describing two thirds of a portfolio
// while silently dropping the third is worse than prose describing none of it.
func Generate(ctx context.Context, g Generator, p Prompt, vocab map[string]Value) (map[string]string, error) {
	// An empty vocabulary is a fully-degraded report announcing itself. There
	// is nothing to phrase, and an account with no analysis would otherwise
	// produce a paid call whose entire output is encouragement -- which
	// SPEC.md §2.6 forbids anyway.
	if len(vocab) == 0 {
		return nil, ErrNothingToPhrase
	}

	attempt := p
	var last error

	// Exactly two attempts. Bounded by a literal rather than a loop condition
	// someone can later widen without noticing what it costs.
	for i := 0; i < 2; i++ {
		raw, err := g.Draft(ctx, attempt)
		if err != nil {
			// The generator's own failure, passed through untouched so the
			// caller can tell a timeout from a rate limit from a refusal.
			return nil, err
		}

		sections, err := parseDraft(raw)
		if err == nil {
			err = ValidateSections(sections, vocab)
		}
		if err == nil {
			rendered, renderErr := renderSections(sections, vocab)
			if renderErr == nil {
				return rendered, nil
			}
			// Render disagreeing with Validate is a bug in this package rather
			// than a bad draft, and retrying would only hide it.
			return nil, fmt.Errorf("narrative: validated draft failed to render: %w", renderErr)
		}

		last = err
		// The rejected draft, logged in full-ish. It contains placeholders and
		// prose and -- by the guarantee this package enforces -- no figures,
		// so there is nothing sensitive in it. Without this, tuning the word
		// list means guessing at what the model actually wrote.
		log.Printf("narrative: draft %d rejected: %v\n%s", i+1, err, truncateDraft(raw))
		attempt = p.withCorrection(err)
	}

	return nil, fmt.Errorf("%w: %v", ErrGenerationUnusable, last)
}

// withCorrection appends the rejection to the instructions so the second
// attempt is told what was wrong with the first, quoting the offending
// fragment rather than restating the rule it broke.
func (p Prompt) withCorrection(reason error) Prompt {
	out := p
	out.User = p.User + "\n\n" +
		"Your previous draft was rejected and not shown to anyone. " + reason.Error() + "\n" +
		"Rewrite it. Every figure must be a placeholder from the list above; " +
		"write no digits, no spelled-out numbers, and no units."
	return out
}

// parseDraft reads the model's text as the section object it was asked for.
//
// A leading code fence is tolerated. Models emit them often enough that
// refusing would spend the single retry on formatting instead of on the
// guarantee, and stripping one weakens nothing: every check still runs on what
// is inside.
func parseDraft(raw string) (map[string]string, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "```") {
		if i := strings.IndexByte(trimmed, '\n'); i >= 0 {
			trimmed = trimmed[i+1:]
		}
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	}

	var out map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed)), &out); err != nil {
		return nil, &ValidationError{
			Check:    "malformed",
			detail:   "the draft is not the JSON object of sections that was asked for",
			Fragment: truncate(trimmed),
		}
	}

	// Unknown keys are dropped rather than rejected: a model that also
	// returned a "summary" has not produced a figure, and nothing downstream
	// reads a section this service did not ask for.
	kept := make(map[string]string, len(out))
	for _, name := range sectionNames {
		if s, ok := out[name]; ok {
			kept[name] = s
		}
	}
	if len(kept) == 0 {
		return nil, &ValidationError{
			Check:    "malformed",
			detail:   "the draft carries none of the sections that were asked for",
			Fragment: truncate(trimmed),
		}
	}
	return kept, nil
}

// truncateDraft bounds what a rejected draft can write to the log.
func truncateDraft(s string) string {
	const max = 1200
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func renderSections(sections map[string]string, vocab map[string]Value) (map[string]string, error) {
	out := make(map[string]string, len(sections))
	for name, draft := range sections {
		rendered, err := Render(draft, vocab)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = rendered
	}
	return out, nil
}

// Cache stores rendered narratives against the report they describe.
//
// The key is the caller's id plus the report's content hash, so an entry can
// only be served while every figure it describes is unchanged. Errors are
// never cached (SPEC.md §2.10): a degraded response must not stick.
type Cache interface {
	Get(ctx context.Context, userID, reportHash string) (map[string]string, bool, error)
	Set(ctx context.Context, userID, reportHash string, sections map[string]string, ttl time.Duration) error
}

// Counter bounds how many generations one caller may buy in a day.
//
// Allow reserves a slot BEFORE the model is called, not after, so a request
// that dies mid-generation still counts against the cap. Under-counting is the
// direction that costs money.
type Counter interface {
	Allow(ctx context.Context, userID string, cap int) (bool, error)
}
