package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/handler"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/llm"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
	narrativemock "github.com/kpeguero/quantsim/services/ai-insights/internal/narrative/mock"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service/mock"
)

// A draft that phrases the traded account's report using only tokens that
// report actually offers.
const goodDraft = `{"risk":"Your largest holding is {risk.largest_position_symbol}.",` +
	`"benchmarking":"You returned {benchmarking.portfolio_return_pct} over the window.",` +
	`"behavior":"You traded {behavior.trade_count} times."}`

func newNarrativeRouter(trading *mock.TradingClient, history *mock.HistoryClient, g narrative.Generator) http.Handler {
	return newNarrativeRouterWithCache(trading, history, g, nil)
}

func newNarrativeRouterWithCache(trading *mock.TradingClient, history *mock.HistoryClient, g narrative.Generator, c narrative.Cache) http.Handler {
	return newNarrativeRouterWith(trading, history, g, c, &narrativemock.Counter{})
}

func newNarrativeRouterWith(trading *mock.TradingClient, history *mock.HistoryClient, g narrative.Generator, c narrative.Cache, counter narrative.Counter) http.Handler {
	svc := service.NewService(trading, history, nil)
	return handler.NewRouter(
		handler.NewInsightsHandler(svc),
		handler.NewNarrativeHandler(svc, g, c, counter, "test-model"),
		jwtSecret,
	)
}

func getNarrative(t *testing.T, router http.Handler, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/insights/portfolio/narrative", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

type narrativeBody struct {
	AsOfDate   string            `json:"as_of_date"`
	ReportHash string            `json:"report_hash"`
	State      string            `json:"state"`
	Narrative  map[string]string `json:"narrative"`
	Reason     string            `json:"reason"`
	Model      string            `json:"model"`
}

func decodeNarrative(t *testing.T, rec *httptest.ResponseRecorder) narrativeBody {
	t.Helper()
	var body narrativeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestNarrative_RendersProseForATradedAccount(t *testing.T) {
	trading, history := tradedAccount()
	g := &narrativemock.Generator{Drafts: []string{goodDraft}}
	router := newNarrativeRouter(trading, history, g)

	rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	body := decodeNarrative(t, rec)
	if body.State != "ok" {
		t.Errorf("state = %q, want ok", body.State)
	}
	if body.Narrative["behavior"] == "" {
		t.Fatalf("no behavior prose: %+v", body)
	}
	if body.Model != "test-model" {
		t.Errorf("model = %q, want the configured model", body.Model)
	}
	// The figure came from the struct, not from the model.
	if got := body.Narrative["risk"]; got == "" || got == goodDraft {
		t.Errorf("risk prose was not rendered: %q", got)
	}
}

func TestNarrative_RequiresAToken(t *testing.T) {
	trading, history := tradedAccount()
	router := newNarrativeRouter(trading, history, &narrativemock.Generator{})

	if rec := getNarrative(t, router, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", rec.Code)
	}
}

// The report's failures propagate unchanged. This is inherited behaviour, and
// inherited behaviour is what stops being true silently -- so it is asserted
// here rather than assumed from the other endpoint's tests.
func TestNarrative_ReportErrorsPropagateAndAreNotDegradedInto200s(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
		set  func(*mock.TradingClient, *mock.HistoryClient)
	}{
		{"missing symbol is a 404", http.StatusNotFound, func(tr *mock.TradingClient, h *mock.HistoryClient) {
			h.Bars = map[string][]service.Bar{}
		}},
		{"refused token is a 401", http.StatusUnauthorized, func(tr *mock.TradingClient, h *mock.HistoryClient) {
			tr.TradesErr = service.ErrUnauthorized
		}},
		{"upstream outage is a 502", http.StatusBadGateway, func(tr *mock.TradingClient, h *mock.HistoryClient) {
			tr.TradesErr = service.ErrUpstreamUnavailable
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trading, history := tradedAccount()
			tc.set(trading, history)
			g := &narrativemock.Generator{Drafts: []string{goodDraft}}
			router := newNarrativeRouter(trading, history, g)

			rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

			if rec.Code != tc.want {
				t.Errorf("status %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
			if g.Calls() != 0 {
				t.Errorf("made %d generator calls for a report that failed", g.Calls())
			}
		})
	}
}

// Failed phrasing is not an outage: every figure is already correct and
// available at the other endpoint (SPEC 2.11).
func TestNarrative_GenerationFailuresAre200WithADistinctReason(t *testing.T) {
	dirty := `{"risk":"A fall of 12.4 over the window.","benchmarking":"x","behavior":"y"}`

	for _, tc := range []struct {
		name string
		g    narrative.Generator
	}{
		{"upstream error", &narrativemock.Generator{Err: errors.New("boom")}},
		{"two unusable drafts", &narrativemock.Generator{Drafts: []string{dirty, dirty}}},
		{"not configured", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trading, history := tradedAccount()
			router := newNarrativeRouter(trading, history, tc.g)

			rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 -- failed phrasing is not an outage: %s", rec.Code, rec.Body)
			}
			body := decodeNarrative(t, rec)
			if body.State != "unavailable" {
				t.Errorf("state = %q, want unavailable", body.State)
			}
			if body.Reason == "" {
				t.Error("no reason given for an unavailable narrative")
			}
			if body.Narrative != nil {
				t.Errorf("narrative = %v, want null", body.Narrative)
			}
		})
	}
}

// narrative must marshal as null rather than {} when there is none, so a
// caller can tell "no prose" from "prose with no sections".
func TestNarrative_AbsentNarrativeIsNullNotAnEmptyObject(t *testing.T) {
	trading, history := tradedAccount()
	router := newNarrativeRouter(trading, history, &narrativemock.Generator{Err: errors.New("boom")})

	rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := string(raw["narrative"]); got != "null" {
		t.Errorf("narrative marshalled as %s, want null", got)
	}
}

// A report with nothing computed buys no API call (SPEC 2.12).
func TestNarrative_FullyDegradedReportMakesNoGeneratorCall(t *testing.T) {
	trading, history := tradedAccount()
	trading.TradesResult = nil // no trades: every section is insufficient_data
	g := &narrativemock.Generator{Drafts: []string{goodDraft}}
	router := newNarrativeRouter(trading, history, g)

	rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}
	if g.Calls() != 0 {
		t.Errorf("made %d generator calls for a report with nothing in it", g.Calls())
	}
	if body := decodeNarrative(t, rec); body.State != "unavailable" || body.Reason == "" {
		t.Errorf("got state=%q reason=%q, want an explained unavailable", body.State, body.Reason)
	}
}

var _ = context.Background

func TestNarrative_ACacheHitServesStoredProseAndMakesNoCall(t *testing.T) {
	trading, history := tradedAccount()
	svc := service.NewService(trading, history, nil)
	report, err := svc.PortfolioInsights(context.Background(), testUserID, "Bearer x")
	if err != nil {
		t.Fatalf("PortfolioInsights: %v", err)
	}

	c := &narrativemock.Cache{}
	c.Seed(testUserID, service.ReportHash(report), map[string]string{"risk": "stored prose"})
	g := &narrativemock.Generator{Drafts: []string{goodDraft}}
	router := newNarrativeRouterWithCache(trading, history, g, c)

	rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	body := decodeNarrative(t, rec)
	if body.Narrative["risk"] != "stored prose" {
		t.Errorf("risk = %q, want the stored prose", body.Narrative["risk"])
	}
	if g.Calls() != 0 {
		t.Errorf("made %d generator calls on a cache hit", g.Calls())
	}
	if body.AsOfDate == "" || body.State != "ok" {
		t.Errorf("a cache hit returned %+v", body)
	}
}

func TestNarrative_AMissGeneratesAndStoresUnderTheReportHash(t *testing.T) {
	trading, history := tradedAccount()
	c := &narrativemock.Cache{}
	g := &narrativemock.Generator{Drafts: []string{goodDraft}}
	router := newNarrativeRouterWithCache(trading, history, g, c)

	rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if g.Calls() != 1 {
		t.Errorf("made %d generator calls, want 1", g.Calls())
	}
	if c.SetCount() != 1 {
		t.Fatalf("made %d cache writes, want 1", c.SetCount())
	}
	body := decodeNarrative(t, rec)
	if body.ReportHash == "" {
		t.Error("no report_hash returned")
	}
	if c.Sets[0] != testUserID+":"+body.ReportHash {
		t.Errorf("stored under %q, want the hash it reported (%q)", c.Sets[0], body.ReportHash)
	}
}

// A degraded response must not stick. Caching it would turn one bad minute
// into a bad day, and the next request would never get to try again.
func TestNarrative_FailuresAreNeverCached(t *testing.T) {
	dirty := `{"risk":"A fall of 12.4 over the window.","benchmarking":"x","behavior":"y"}`
	for _, tc := range []struct {
		name string
		g    narrative.Generator
	}{
		{"upstream error", &narrativemock.Generator{Err: errors.New("boom")}},
		{"two unusable drafts", &narrativemock.Generator{Drafts: []string{dirty, dirty}}},
		{"not configured", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trading, history := tradedAccount()
			c := &narrativemock.Cache{}
			router := newNarrativeRouterWithCache(trading, history, tc.g, c)

			if rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID)); rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			if c.SetCount() != 0 {
				t.Errorf("cached a failed generation (%d writes)", c.SetCount())
			}
		})
	}
}

// A cache that is down costs a recomputation and nothing else, in both
// directions.
func TestNarrative_CacheOutageStillAnswers(t *testing.T) {
	t.Run("read failure generates", func(t *testing.T) {
		trading, history := tradedAccount()
		c := &narrativemock.Cache{GetErr: errors.New("redis down")}
		g := &narrativemock.Generator{Drafts: []string{goodDraft}}
		router := newNarrativeRouterWithCache(trading, history, g, c)

		rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))
		if rec.Code != http.StatusOK || decodeNarrative(t, rec).State != "ok" {
			t.Fatalf("a cache read failure was not survived: %d %s", rec.Code, rec.Body)
		}
		if g.Calls() != 1 {
			t.Errorf("made %d generator calls, want 1", g.Calls())
		}
	})

	t.Run("write failure still returns the prose", func(t *testing.T) {
		trading, history := tradedAccount()
		c := &narrativemock.Cache{SetErr: errors.New("redis down")}
		g := &narrativemock.Generator{Drafts: []string{goodDraft}}
		router := newNarrativeRouterWithCache(trading, history, g, c)

		rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		if body := decodeNarrative(t, rec); body.State != "ok" || body.Narrative["risk"] == "" {
			t.Errorf("a cache write failure lost the narrative it had already generated: %+v", body)
		}
	})
}

// A stored narrative is correct for these exact figures whatever the
// generator's state, so withholding it because a key is missing would refuse a
// right answer over a configuration detail.
func TestNarrative_ACacheHitIsServedEvenWithNoGeneratorConfigured(t *testing.T) {
	trading, history := tradedAccount()
	svc := service.NewService(trading, history, nil)
	report, err := svc.PortfolioInsights(context.Background(), testUserID, "Bearer x")
	if err != nil {
		t.Fatalf("PortfolioInsights: %v", err)
	}

	c := &narrativemock.Cache{}
	c.Seed(testUserID, service.ReportHash(report), map[string]string{"risk": "stored prose"})
	router := newNarrativeRouterWithCache(trading, history, nil, c)

	rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))
	if body := decodeNarrative(t, rec); body.State != "ok" || body.Narrative["risk"] != "stored prose" {
		t.Errorf("a cache hit was withheld because no generator was configured: %+v", body)
	}
}

// Ordinary reading must not consume an allowance. This is the easiest thing
// in the design to get backwards, and getting it backwards would cap a user
// who never generated anything.
func TestNarrative_ACacheHitDoesNotConsumeTheDailyAllowance(t *testing.T) {
	trading, history := tradedAccount()
	svc := service.NewService(trading, history, nil)
	report, err := svc.PortfolioInsights(context.Background(), testUserID, "Bearer x")
	if err != nil {
		t.Fatalf("PortfolioInsights: %v", err)
	}

	c := &narrativemock.Cache{}
	c.Seed(testUserID, service.ReportHash(report), map[string]string{"risk": "stored prose"})
	counter := &narrativemock.Counter{}
	router := newNarrativeRouterWith(trading, history, &narrativemock.Generator{}, c, counter)

	if rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID)); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if counter.Calls != 0 {
		t.Errorf("a cache hit consumed %d allowances", counter.Calls)
	}
}

func TestNarrative_OverTheCapIsARefusalWithItsOwnReason(t *testing.T) {
	trading, history := tradedAccount()
	// Already at the cap: the next reservation is the one over it.
	counter := &narrativemock.Counter{Count: narrative.DailyGenerationCap}
	g := &narrativemock.Generator{Drafts: []string{goodDraft}}
	router := newNarrativeRouterWith(trading, history, g, &narrativemock.Cache{}, counter)

	rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want a degraded 200", rec.Code)
	}
	body := decodeNarrative(t, rec)
	if body.State != "unavailable" || !strings.Contains(body.Reason, "limit") {
		t.Errorf("got state=%q reason=%q, want a stated limit", body.State, body.Reason)
	}
	if g.Calls() != 0 {
		t.Error("generated anyway, over the cap")
	}
}

// Both sides of the boundary, since an off-by-one here is the difference
// between the cap meaning what it says and meaning one more than it says.
func TestNarrative_TheCapBoundaryHoldsOnBothSides(t *testing.T) {
	for _, tc := range []struct {
		name      string
		already   int
		wantCalls int
	}{
		{"one below the cap", narrative.DailyGenerationCap - 2, 1},
		{"exactly at the cap", narrative.DailyGenerationCap - 1, 1},
		{"one over the cap", narrative.DailyGenerationCap, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trading, history := tradedAccount()
			counter := &narrativemock.Counter{Count: tc.already}
			g := &narrativemock.Generator{Drafts: []string{goodDraft}}
			router := newNarrativeRouterWith(trading, history, g, &narrativemock.Cache{}, counter)

			getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

			if g.Calls() != tc.wantCalls {
				t.Errorf("made %d generator calls, want %d", g.Calls(), tc.wantCalls)
			}
		})
	}
}

// Fail CLOSED, deliberately unlike the cache beside it. The same outage
// removes the cache and the cap at once, so failing open here would mean every
// request generating and none being counted.
func TestNarrative_AnUnavailableCounterRefusesGeneration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		counter narrative.Counter
	}{
		{"counter errors", &narrativemock.Counter{Err: errors.New("redis down")}},
		{"no counter configured", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trading, history := tradedAccount()
			g := &narrativemock.Generator{Drafts: []string{goodDraft}}
			router := newNarrativeRouterWith(trading, history, g, &narrativemock.Cache{}, tc.counter)

			rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want a degraded 200 -- the report is still fine", rec.Code)
			}
			if body := decodeNarrative(t, rec); body.State != "unavailable" || body.Reason == "" {
				t.Errorf("got %+v, want an explained unavailable", body)
			}
			if g.Calls() != 0 {
				t.Errorf("generated %d times with no working cap -- uncached and uncapped "+
					"is the one combination with no cost ceiling", g.Calls())
			}
		})
	}
}

// Every way the narrative can be unavailable, in one table, each with its own
// reason. An operator reading a response or a log has to be able to tell a
// rate limit from a timeout from a refusal from a draft nobody could use from
// a cap from a missing key -- one catch-all reason would make all six look
// like the same outage, and the first real one would be indistinguishable
// from a misconfigured environment variable.
func TestNarrative_EveryDegradationHasItsOwnReason(t *testing.T) {
	dirty := `{"risk":"A fall of 12.4 over the window.","benchmarking":"x","behavior":"y"}`
	ok := func() *narrativemock.Generator { return &narrativemock.Generator{Drafts: []string{goodDraft}} }

	seen := map[string]string{}
	for _, tc := range []struct {
		name    string
		g       narrative.Generator
		counter narrative.Counter
		noTrade bool
	}{
		{name: "rate limited", g: &narrativemock.Generator{Err: fmt.Errorf("%w: 429", llm.ErrRateLimited)}},
		{name: "timed out", g: &narrativemock.Generator{Err: fmt.Errorf("%w: deadline", llm.ErrTimeout)}},
		{name: "refused", g: &narrativemock.Generator{Err: fmt.Errorf("%w: declined", llm.ErrRefused)}},
		{name: "upstream", g: &narrativemock.Generator{Err: fmt.Errorf("%w: 500", llm.ErrUpstream)}},
		{name: "unusable draft", g: &narrativemock.Generator{Drafts: []string{dirty, dirty}}},
		{name: "not configured", g: nil},
		{name: "over the cap", g: ok(), counter: &narrativemock.Counter{Count: narrative.DailyGenerationCap}},
		{name: "counter unavailable", g: ok(), counter: &narrativemock.Counter{Err: errors.New("redis down")}},
		{name: "nothing to phrase", g: ok(), noTrade: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trading, history := tradedAccount()
			if tc.noTrade {
				trading.TradesResult = nil
			}
			counter := tc.counter
			if counter == nil {
				counter = &narrativemock.Counter{}
			}
			c := &narrativemock.Cache{}
			router := newNarrativeRouterWith(trading, history, tc.g, c, counter)

			rec := getNarrative(t, router, "Bearer "+accessToken(t, testUserID))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 -- the report is fine, only the prose is missing", rec.Code)
			}
			body := decodeNarrative(t, rec)
			if body.State != "unavailable" || body.Reason == "" {
				t.Fatalf("got %+v, want an explained unavailable", body)
			}
			if body.Narrative != nil {
				t.Errorf("narrative = %v, want null", body.Narrative)
			}
			// No failure path may cache. Caching one would turn a bad minute
			// into a bad day, and the next request would never retry.
			if c.SetCount() != 0 {
				t.Errorf("a failed generation was cached (%d writes)", c.SetCount())
			}
			if prior, ok := seen[body.Reason]; ok {
				t.Errorf("%q reports the same reason as %q: %q", tc.name, prior, body.Reason)
			}
			seen[body.Reason] = tc.name
		})
	}

	if len(seen) != 9 {
		t.Errorf("got %d distinct reasons across 9 degradations: %v", len(seen), seen)
	}
}

// "Nothing to phrase" must cost nothing. An account with no analysis would
// otherwise buy a call whose entire output is encouragement, which the prompt
// forbids anyway.
func TestNarrative_NothingToPhraseCostsNoCallAndNoAllowance(t *testing.T) {
	trading, history := tradedAccount()
	trading.TradesResult = nil
	g := &narrativemock.Generator{Drafts: []string{goodDraft}}
	counter := &narrativemock.Counter{}
	router := newNarrativeRouterWith(trading, history, g, &narrativemock.Cache{}, counter)

	getNarrative(t, router, "Bearer "+accessToken(t, testUserID))

	if g.Calls() != 0 {
		t.Errorf("made %d generator calls for a report with nothing in it", g.Calls())
	}
	if counter.Calls != 0 {
		t.Errorf("consumed %d allowances for a report with nothing in it", counter.Calls)
	}
}
