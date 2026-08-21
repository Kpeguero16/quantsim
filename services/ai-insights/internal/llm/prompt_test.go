package llm_test

import (
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/llm"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

func report() service.PortfolioInsights {
	return service.PortfolioInsights{
		AsOfDate: "2026-08-14",
		Window:   service.Window{StartDate: "2026-06-01", TradingDays: 52},
		Risk: service.RiskSection{
			State:            service.StateOK,
			Positions:        []service.PositionWeight{{Symbol: "AAPL", Quantity: 10, MarketValue: 1750.5, WeightPct: 62.5}},
			ConcentrationHHI: 0.531,
			MaxDrawdownPct:   12.4,
		},
		Benchmarking: service.BenchmarkingSection{
			State:      service.StateOK,
			Benchmarks: []service.Benchmark{{Symbol: "SPY", Label: "S&P 500", ReturnPct: 8.1, Sharpe: 1.02}},
		},
		Behavior: service.BehaviorSection{State: service.StateOK, TradeCount: 34},
	}
}

// A stray timestamp or user id in the system prompt is the classic silent
// cache invalidator, and it would also make the prompt irreproducible -- two
// users would be asking two subtly different questions and nothing would say
// so.
func TestBuildPrompt_TheSystemPromptIsIdenticalForEveryReport(t *testing.T) {
	a := report()

	b := report()
	b.AsOfDate = "2026-09-30"
	b.Risk.Positions = []service.PositionWeight{{Symbol: "TSLA", Quantity: 3, WeightPct: 100}}
	b.Behavior.TradeCount = 999

	first := llm.BuildPrompt(a, narrative.Placeholders(a)).System
	second := llm.BuildPrompt(b, narrative.Placeholders(b)).System

	if first != second {
		t.Error("the system prompt varies with the report; it must be byte-identical across requests")
	}
	if first == "" {
		t.Fatal("the system prompt is empty")
	}
}

// SPEC 2.3's whole point: the tokens the prompt advertises and the tokens the
// renderer will substitute are ONE map, proven, not two lists that agree
// today.
func TestBuildPrompt_AdvertisesExactlyTheVocabularyItWasGiven(t *testing.T) {
	r := report()
	vocab := narrative.Placeholders(r)
	user := llm.BuildPrompt(r, vocab).User

	for token := range vocab {
		if !strings.Contains(user, "{"+token+"}") {
			t.Errorf("token %q is in the vocabulary but not offered in the prompt", token)
		}
	}

	// And nothing is offered that the renderer would not accept. Every
	// brace-wrapped name in the prompt must be a real key.
	for _, offered := range bracedNames(user) {
		if _, ok := vocab[offered]; !ok {
			t.Errorf("the prompt offers %q, which the renderer would reject", offered)
		}
	}
}

func bracedNames(s string) []string {
	var out []string
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			return out
		}
		closeAt := strings.IndexByte(s[open:], '}')
		if closeAt < 0 {
			return out
		}
		out = append(out, s[open+1:open+closeAt])
		s = s[open+closeAt:]
	}
}

// D1: the model is shown the raw value so it can judge severity, never the
// finished string it is forbidden to produce.
func TestBuildPrompt_ShowsRawValuesNotRenderedOnes(t *testing.T) {
	r := report()
	user := llm.BuildPrompt(r, narrative.Placeholders(r)).User

	if !strings.Contains(user, "12.4") {
		t.Error("the drawdown's raw value is not shown; the model cannot judge severity")
	}
	for _, rendered := range []string{"12.4%", "+8.1%", "1,750.50", "8/14/2026"} {
		if strings.Contains(user, rendered) {
			t.Errorf("the prompt shows the RENDERED figure %q -- the model is being handed "+
				"the exact string it must not produce", rendered)
		}
	}
}

func TestBuildPrompt_NamesDegradedSectionsAsUnavailable(t *testing.T) {
	r := report()
	r.Benchmarking = service.BenchmarkingSection{
		State:  service.StateInsufficientData,
		Reason: "the window is too short to compare",
	}

	p := llm.BuildPrompt(r, narrative.Placeholders(r))

	if !strings.Contains(p.User, "benchmarking") {
		t.Error("the degraded section is not mentioned at all")
	}
	if !strings.Contains(p.User, "the window is too short to compare") {
		t.Error("the section's own reason is not passed on")
	}
	if strings.Contains(p.User, "{benchmarking.") {
		t.Error("a degraded section's tokens are still being offered")
	}
}

// The rules the guarantee rests on have to actually be stated.
func TestBuildPrompt_StatesTheContractAndTheBoundaries(t *testing.T) {
	r := report()
	system := strings.ToLower(llm.BuildPrompt(r, narrative.Placeholders(r)).System)

	for _, rule := range []string{"placeholder", "digit", "advice"} {
		if !strings.Contains(system, rule) {
			t.Errorf("the system prompt never mentions %q", rule)
		}
	}
}

// The response shape has to be asked for, or parseDraft is guessing.
func TestBuildPrompt_AsksForTheSectionObject(t *testing.T) {
	r := report()
	system := llm.BuildPrompt(r, narrative.Placeholders(r)).System

	for _, key := range []string{"risk", "benchmarking", "behavior"} {
		if !strings.Contains(system, key) {
			t.Errorf("the system prompt never names the %q section", key)
		}
	}
}

// The vocabulary must be USED, not rebuilt.
//
// This test exists because a mutant survived: replacing the passed-in map with
// a fresh narrative.Placeholders(r) call changed nothing, since every existing
// test handed BuildPrompt exactly what Placeholders would have produced. Those
// tests proved the two agree when built the same way -- which is the drift
// they were meant to preclude, not evidence against it.
//
// Passing a map that Placeholders would never return is the only way to tell
// the difference, and it is what SPEC 2.3's single-source claim actually
// rests on: whatever the renderer will substitute from is what the prompt
// offers, whether or not it looks like a freshly derived vocabulary.
func TestBuildPrompt_UsesTheVocabularyItIsGivenRatherThanDerivingItsOwn(t *testing.T) {
	r := report()
	custom := map[string]narrative.Value{
		"behavior.trade_count":                     {Kind: narrative.KindCount, Num: 34},
		"a.token.placeholders.would.never.produce": {Kind: narrative.KindSymbol, Text: "ZZZZ"},
	}

	user := llm.BuildPrompt(r, custom).User

	if !strings.Contains(user, "{a.token.placeholders.would.never.produce}") {
		t.Error("the prompt does not offer a token from the map it was given; " +
			"it is deriving its own vocabulary, which can drift from what the renderer substitutes")
	}
	// And nothing from the report that the given map does not carry.
	for _, absent := range []string{"{risk.max_drawdown_pct}", "{risk.concentration_hhi}", "{as_of_date}"} {
		if strings.Contains(user, absent) {
			t.Errorf("the prompt offers %s, which is not in the vocabulary it was handed", absent)
		}
	}
}

// The prompt must not use the words the validator rejects.
//
// It did: the concentration index was described as "an index between zero and
// one", and a draft was then thrown away for saying "a value near one means a
// single name". The prompt was inviting the rejection it would punish, and it
// cost a full generation -- two API calls and no narrative at all.
func TestBuildPrompt_DoesNotUseWordsTheValidatorRejects(t *testing.T) {
	r := report()
	p := llm.BuildPrompt(r, narrative.Placeholders(r))

	// The system prompt quotes banned words on purpose, to forbid them. The
	// value descriptions have no such excuse.
	for _, banned := range []string{"zero", "one", "two", "three", "half", "percent", "dollars"} {
		if strings.Contains(strings.ToLower(p.User), " "+banned+" ") {
			t.Errorf("the value descriptions use %q, a word the validator rejects -- "+
				"the prompt is inviting the failure it punishes", banned)
		}
	}
}
