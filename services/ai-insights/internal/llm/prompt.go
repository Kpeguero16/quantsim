// Package llm builds the prompt for narrative generation and talks to the
// Claude API.
//
// It knows nothing about SPEC.md §2.2's guarantee. Validation, substitution
// and rendering all live in the narrative package, on the other side of the
// Generator interface (plan D3), so nothing here can weaken them.
package llm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// systemPrompt is frozen: byte-identical for every request and every user.
//
// Nothing per-request belongs here. A timestamp or a user id in this string
// would break the cacheable prefix, and worse, would make two users' questions
// subtly different with nothing to say so.
const systemPrompt = `You explain a paper-trading simulator's portfolio analysis to the person who owns it. They are learning to trade and the account is simulated.

You will be given a report and a list of placeholders. Write three short paragraphs, one per section: risk, benchmarking, behavior. Two to four sentences each.

THE ONE RULE THAT MATTERS: you must never write a figure. Every number, date, ticker and quantity must appear as a placeholder token copied exactly from the list you are given, in braces, like {risk.max_drawdown_pct}. The system replaces each token with the real value.

This means, without exception:
- Write no digits at all. Not in a number, not in a date, not in a ticker.
- Write no spelled-out numbers or quantities either. This includes small ones: not "one", not "two", not "three", not "both", and not "half", "a third", "double" or "a dozen". This is the rule drafts fail most often. If you want to say how many trades there were, write {behavior.trade_count}. If you want to say how many holdings there are, write {risk.position_count}. If there is no placeholder for the count you want, name the things instead of counting them: write "{benchmarking.SPY.sharpe} for SPY and {benchmarking.QQQ.sharpe} for QQQ" rather than "all three are negative", and "your holdings" rather than "your three holdings". Words that imply a count are covered by this too: no "a couple", no "a pair", no "a dozen".
- Write no units. No percent signs, no dollar signs, no "percent", no "dollars". The unit is supplied with the figure.
- Use only placeholders from the list. If a figure you want is not on the list, write the sentence without it.

A draft that breaks any of these is discarded unread, so there is no benefit to guessing at a number.

WHAT TO WRITE: explain what a figure means, rather than restating it. "Concentration this high means a single holding moves the whole account" tells the reader something; "your concentration is {risk.concentration_hhi}" does not. Prefer plain language and define any term you use.

Be neutral. Do not congratulate, encourage, reassure or soften. A portfolio that lost money should read as a portfolio that lost money.

Give no advice. Do not tell the reader to buy, sell, hold, reduce, diversify or rebalance anything, and make no claim about what any price or return will do next. Describe and explain only.

If a section is marked unavailable, omit it entirely rather than writing around it.

Reply with a JSON object and nothing else, with the keys "risk", "benchmarking" and "behavior", each a single string. Omit the key for any unavailable section.`

// BuildPrompt assembles the request for one report.
//
// The vocabulary is PASSED IN rather than rebuilt, and that is the whole of
// SPEC.md §2.3: the tokens advertised here and the tokens substituted later
// are one map, so they cannot disagree. Rebuilding it from the report would
// reintroduce exactly the drift the single source exists to prevent.
func BuildPrompt(r service.PortfolioInsights, vocab map[string]narrative.Value) narrative.Prompt {
	var b strings.Builder

	b.WriteString("SECTIONS\n")
	for _, s := range []struct {
		name          string
		state, reason string
	}{
		{"risk", r.Risk.State, r.Risk.Reason},
		{"benchmarking", r.Benchmarking.State, r.Benchmarking.Reason},
		{"behavior", r.Behavior.State, r.Behavior.Reason},
	} {
		if s.state == service.StateOK {
			fmt.Fprintf(&b, "- %s: available\n", s.name)
			continue
		}
		reason := s.reason
		if reason == "" {
			reason = "not enough data"
		}
		fmt.Fprintf(&b, "- %s: UNAVAILABLE (%s) -- omit this section\n", s.name, reason)
	}

	b.WriteString("\nPLACEHOLDERS YOU MAY USE, WITH THEIR CURRENT VALUES\n")
	b.WriteString("Values are shown so you can judge what is worth saying. Copy the token, never the value.\n")

	// Sorted, so the prompt is reproducible for the same report rather than
	// reshuffled by Go's map iteration on every request.
	names := make([]string, 0, len(vocab))
	for name := range vocab {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "{%s} = %s\n", name, describe(vocab[name]))
	}

	return narrative.Prompt{System: systemPrompt, User: b.String()}
}

// describe shows a value as raw data with its unit named in words.
//
// It deliberately does NOT use the renderer (plan D1). Showing "-12.4%" here
// would hand the model the exact string it is forbidden to produce, one copy
// away from a rejected draft. Showing "-12.4 (a percentage)" gives it
// everything it needs to judge severity and nothing it can paste.
func describe(v narrative.Value) string {
	switch v.Kind {
	case narrative.KindPercent, narrative.KindSignedPercent:
		return raw(v.Num) + " (a percentage)"
	case narrative.KindRatio:
		return raw(v.Num) + " (a ratio)"
	case narrative.KindIndex:
		return raw(v.Num) + " (a concentration index; higher means more concentrated)"
	case narrative.KindMoney:
		return raw(v.Num) + " (an amount of money)"
	case narrative.KindQuantity:
		return raw(v.Num) + " (a number of shares)"
	case narrative.KindCount:
		return raw(v.Num) + " (a count)"
	case narrative.KindDate:
		return v.Text + " (a calendar date)"
	case narrative.KindSymbol:
		return v.Text + " (a ticker symbol)"
	default:
		return "(unknown)"
	}
}

func raw(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
