package handler

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/llm"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// The states a narrative response can carry. "ok" means prose; "unavailable"
// means the report is fine and only the phrasing is missing.
const (
	stateOK          = "ok"
	stateUnavailable = "unavailable"
)

// The reasons an "unavailable" narrative can carry. They are a closed set and
// distinct from one another on purpose: "we could not phrase it", "there was
// nothing to phrase" and "phrasing is switched off" are three different facts
// about the system, and collapsing them would make the first outage
// indistinguishable from a missing environment variable.
const (
	reasonNotConfigured = "narrative generation is not configured"
	reasonNothing       = "no analysis is available to describe"
	reasonUnusable      = "no usable narrative could be generated"
	reasonUpstream      = "the narrative service is unavailable"
	reasonOverCap       = "daily generation limit reached"
	reasonNoCounter     = "narrative generation is unavailable without a generation counter"
	reasonRateLimited   = "the narrative service is rate limited"
	reasonTimedOut      = "narrative generation timed out"
	reasonRefused       = "the model declined to describe this portfolio"
)

// cacheTTL is how long a narrative outlives its generation. Long, because the
// key already carries the report's content hash (SPEC.md §2.10): an entry can
// only be served while every figure it describes is unchanged, so age is not
// what makes it stale.
const cacheTTL = 24 * time.Hour

// NarrativeResponse is GET /insights/portfolio/narrative (SPEC.md §2.1).
//
// Narrative is a map so an absent section is an absent key, and so that no
// narrative at all marshals as null rather than {}. A caller has to be able to
// tell "there is no prose" from "there is prose describing nothing".
type NarrativeResponse struct {
	AsOfDate    string            `json:"as_of_date,omitempty"`
	ReportHash  string            `json:"report_hash,omitempty"`
	State       string            `json:"state"`
	Narrative   map[string]string `json:"narrative"`
	Reason      string            `json:"reason,omitempty"`
	Model       string            `json:"model,omitempty"`
	GeneratedAt *time.Time        `json:"generated_at,omitempty"`
}

type NarrativeHandler struct {
	service *service.Service

	// generator is nil when ANTHROPIC_API_KEY is unset, which is a supported
	// configuration rather than a boot failure -- matching REDIS_URL's
	// precedent in this same service.
	generator narrative.Generator

	// cache is nil when REDIS_URL is unset. Like the report cache it fronts,
	// it is an optimisation this service runs without.
	cache narrative.Cache

	// counter is nil when REDIS_URL is unset, and that REFUSES generation
	// rather than allowing it -- see the comment on the check itself.
	counter narrative.Counter
	model   string
}

func NewNarrativeHandler(svc *service.Service, g narrative.Generator, c narrative.Cache, counter narrative.Counter, model string) *NarrativeHandler {
	return &NarrativeHandler{service: svc, generator: g, cache: c, counter: counter, model: model}
}

// PortfolioNarrative serves GET /insights/portfolio/narrative.
//
// It calls the same service path the report endpoint calls and re-derives
// nothing, so it inherits the reconciliation guard, the 404 on an unavailable
// symbol, the 401 on a refused token and the 502 on an upstream outage.
//
// The split that matters (SPEC.md §2.11): **a failed report is an error, and
// failed phrasing is not.** If the report cannot be produced there is nothing
// to phrase, and answering 200 with an empty narrative would hide a real
// failure behind a cosmetic one. If the report is fine and only the prose is
// missing, every figure is still correct and still available at the other
// endpoint, so the response is a 200 that says so.
func (h *NarrativeHandler) PortfolioNarrative(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	report, err := h.service.PortfolioInsights(r.Context(), id.String(), r.Header.Get("Authorization"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	// Built once and used twice: as the tokens the prompt offers and as the
	// substitution table (SPEC.md §2.3). An empty vocabulary is how a fully
	// degraded report announces there is nothing to phrase.
	vocab := narrative.Placeholders(report)
	hash := service.ReportHash(report)

	// A hit is served whatever the generator's state, including when there is
	// no generator at all: the prose is already correct for these exact
	// figures, and refusing to serve it because the key is missing would
	// withhold a right answer over a configuration detail.
	if sections, ok := h.cachedNarrative(r, id.String(), hash); ok {
		writeNarrative(w, report, hash, sections, h.model, nil)
		return
	}

	// Checked here rather than left to narrative.Generate, and the ORDER is
	// load-bearing: this must come before the cap is reserved. A fully
	// degraded report costs no API call, so it must cost no allowance either
	// -- otherwise an account with no trades that polls this endpoint burns
	// its whole daily quota without a single call being made, and is then
	// refused on the day it finally has something to describe.
	if len(vocab) == 0 {
		writeUnavailable(w, report, hash, reasonNothing)
		return
	}

	if h.generator == nil {
		writeUnavailable(w, report, hash, reasonNotConfigured)
		return
	}

	// The cap is checked only after the cache has missed, so ordinary reading
	// never consumes an allowance (SPEC.md §2.7).
	//
	// This FAILS CLOSED, deliberately the opposite posture to the fail-open
	// cache two branches above, and the asymmetry is the whole point. The same
	// Redis outage removes the cache and the cap at once: every request
	// becomes a miss AND every request becomes uncounted, which is precisely
	// the combination that turns ordinary traffic into an unbounded bill.
	// Failing open on the cache costs latency; failing open here costs money
	// with no ceiling. A missing counter is treated the same as a broken one,
	// because "no Redis configured" produces exactly the same uncached and
	// uncapped state that an outage does.
	//
	// The report itself is unaffected either way, and the user-visible result
	// is the same degraded 200.
	if allowed, err := h.allowGeneration(r, id.String()); !allowed {
		writeUnavailable(w, report, hash, err)
		return
	}

	sections, err := narrative.Generate(r.Context(), h.generator, llm.BuildPrompt(report, vocab), vocab)
	if err != nil {
		// Never cached. A degraded response must not stick: the next request
		// gets to try again (SPEC.md §2.10).
		writeUnavailable(w, report, hash, narrativeReason(r, err))
		return
	}

	if h.cache != nil {
		if err := h.cache.Set(r.Context(), id.String(), hash, sections, cacheTTL); err != nil {
			// Fail-open the other way too: the narrative is already generated
			// and already correct, and failing to store it costs the next
			// request a recomputation and nothing else.
			log.Printf("ai-insights: narrative cache write failed: %v", err)
		}
	}

	now := time.Now().UTC()
	writeNarrative(w, report, hash, sections, h.model, &now)
}

// allowGeneration reserves one paid generation, or explains why it may not.
func (h *NarrativeHandler) allowGeneration(r *http.Request, userID string) (bool, string) {
	if h.counter == nil {
		return false, reasonNoCounter
	}
	allowed, err := h.counter.Allow(r.Context(), userID, narrative.DailyGenerationCap)
	if err != nil {
		log.Printf("ai-insights: generation counter unavailable, refusing to generate: %v", err)
		return false, reasonNoCounter
	}
	if !allowed {
		return false, reasonOverCap
	}
	return true, ""
}

// cachedNarrative reads the cache, treating any failure as a miss.
//
// Logged rather than silent, and distinct from a miss: a miss is the cache
// working, an error is a cache that is down, and reporting the second as the
// first would make a Redis outage invisible while it quietly doubled the bill.
func (h *NarrativeHandler) cachedNarrative(r *http.Request, userID, hash string) (map[string]string, bool) {
	if h.cache == nil {
		return nil, false
	}
	sections, ok, err := h.cache.Get(r.Context(), userID, hash)
	if err != nil {
		log.Printf("ai-insights: narrative cache read failed, generating: %v", err)
		return nil, false
	}
	return sections, ok
}

func writeNarrative(w http.ResponseWriter, report service.PortfolioInsights, hash string, sections map[string]string, model string, generatedAt *time.Time) {
	WriteJSON(w, http.StatusOK, NarrativeResponse{
		AsOfDate:    report.AsOfDate,
		ReportHash:  hash,
		State:       stateOK,
		Narrative:   sections,
		Model:       model,
		GeneratedAt: generatedAt,
	})
}

// narrativeReason maps a generation failure to the one fact the caller is told.
//
// Everything that is not a known degradation is logged: an unusable draft is
// expected occasionally and says something about the prompt, whereas an
// unrecognised error says something about the code.
func narrativeReason(r *http.Request, err error) string {
	switch {
	case errors.Is(err, narrative.ErrNothingToPhrase):
		return reasonNothing
	case errors.Is(err, narrative.ErrGenerationUnusable):
		log.Printf("ai-insights: %s %s: no usable draft: %v", r.Method, r.URL.Path, err)
		return reasonUnusable
	case errors.Is(err, llm.ErrRateLimited):
		log.Printf("ai-insights: %s %s: rate limited: %v", r.Method, r.URL.Path, err)
		return reasonRateLimited
	case errors.Is(err, llm.ErrTimeout):
		log.Printf("ai-insights: %s %s: generation timed out: %v", r.Method, r.URL.Path, err)
		return reasonTimedOut
	case errors.Is(err, llm.ErrRefused):
		log.Printf("ai-insights: %s %s: refusal: %v", r.Method, r.URL.Path, err)
		return reasonRefused
	default:
		log.Printf("ai-insights: %s %s: generation failed: %v", r.Method, r.URL.Path, err)
		return reasonUpstream
	}
}

// writeUnavailable is the degraded 200: the report stands, the prose does not.
// Narrative is left nil so it marshals as null.
func writeUnavailable(w http.ResponseWriter, report service.PortfolioInsights, hash, reason string) {
	WriteJSON(w, http.StatusOK, NarrativeResponse{
		AsOfDate:   report.AsOfDate,
		ReportHash: hash,
		State:      stateUnavailable,
		Reason:     reason,
	})
}
