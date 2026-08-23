package handler

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

type InsightsHandler struct {
	service *service.Service
}

// InsightsResponse is GET /insights/portfolio: the report, plus the identity
// of the report (Step 22 SPEC.md §2.3).
//
// The hash is attached HERE rather than added as a field on
// service.PortfolioInsights, and that placement is the whole design. ReportHash
// marshals that struct to derive the digest, so a field on it would hash
// itself: every narrative cache key minted since Step 21 would move, for a
// field that is not a measurement.
//
// Embedded, so the JSON body gains exactly one key and every existing consumer
// parses the shape it already parses.
//
// WHY it exists at all: the narrative endpoint computes the report
// independently and returns the hash of what IT saw. Without a hash on this
// side there is nothing to compare that against, and a caller composing the two
// -- which is exactly what Step 22's UI does -- can render a figure from one
// report beside a sentence describing another. Both correct, no error, and
// indistinguishable on the page. That is the failure Step 21 made structurally
// impossible inside this service; this key is what keeps it impossible one
// layer up.
type InsightsResponse struct {
	service.PortfolioInsights
	ReportHash string `json:"report_hash"`
}

func NewInsightsHandler(svc *service.Service) *InsightsHandler {
	return &InsightsHandler{service: svc}
}

// PortfolioInsights serves GET /insights/portfolio (SPEC.md §2.4).
//
// The caller's Authorization header is read straight off the request and
// handed to the service, which forwards it to trading-engine (§6.5).
// RequireAuth has already validated it by the time this runs, so what is
// forwarded is a token this service verified rather than one it relayed
// blind.
func (h *InsightsHandler) PortfolioInsights(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	insights, err := h.service.PortfolioInsights(r.Context(), id.String(), r.Header.Get("Authorization"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusOK, InsightsResponse{
		PortfolioInsights: insights,
		ReportHash:        service.ReportHash(insights),
	})
}

// userID reads the subject RequireAuth put on the context and parses it.
//
// It is the cache key (insights:{user_id}, SPEC.md §2.8) and nothing else --
// no upstream call carries it, because trading-engine resolves the account
// from the forwarded token itself, so this service never learns an account id
// and cannot express a request for someone else's history.
//
// Parsing it as a UUID rather than passing the raw subject through is what
// keeps that key well-formed: an arbitrary token subject would become an
// arbitrary Redis key, and a subject containing a colon could be written to
// look like another namespace's. The canonical uuid.String() cannot.
func userID(r *http.Request) (uuid.UUID, bool) {
	raw, ok := pkgauth.UserIDFromContext(r.Context())
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	// 404, not backtesting's 400: there the symbol came from the request body
	// and a bad one is the caller's mistake, whereas here it came from the
	// caller's own trade history or from this service's benchmark list. The
	// caller asked for nothing wrong; a symbol they legitimately hold has no
	// stored data (SPEC.md §2.10).
	case errors.Is(err, service.ErrSymbolUnavailable):
		WriteError(w, http.StatusNotFound, "symbol_unavailable", symbolUnavailableMessage(err))

	// 401, not 502: the caller's own credential was refused on the onward
	// hop, which is not an outage and must not read as one. Usually a token
	// that expired between this service validating it and the call to
	// trading-engine (SPEC.md §6.5).
	case errors.Is(err, service.ErrUnauthorized):
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")

	// 502, not 500: this service is fine, an upstream is not.
	case errors.Is(err, service.ErrUpstreamUnavailable):
		WriteError(w, http.StatusBadGateway, "upstream_unavailable",
			"portfolio or market data is unavailable; no insights were computed")

	default:
		log.Printf("ai-insights: %s %s: %v", r.Method, r.URL.Path, err)
		WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
	}
}

// symbolUnavailableMessage names the symbol when the error carries one.
//
// The fallback is not dead code: ErrSymbolUnavailable can be matched from a
// bare sentinel anyone constructs, and a message reading "no historical data
// is available for " would be worse than a vaguer one that is at least a
// sentence.
func symbolUnavailableMessage(err error) string {
	var unavailable *service.SymbolUnavailableError
	if errors.As(err, &unavailable) && unavailable.Symbol != "" {
		return fmt.Sprintf("no historical data is available for %s", unavailable.Symbol)
	}
	return "no historical data is available for one of the portfolio's symbols"
}
