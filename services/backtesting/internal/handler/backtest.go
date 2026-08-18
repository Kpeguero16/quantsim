// Package handler wires the backtesting service's routes and translates
// between HTTP and the service layer.
package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/backtesting/internal/service"
)

// maxBodyBytes matches the cap every other write endpoint in this project
// applies (auth since Step 9, trading-engine since Step 14). A backtest
// request is a handful of fields; this is about making an unbounded body
// impossible to send, not about tightness.
const maxBodyBytes = 64 << 10

type BacktestHandler struct {
	service *service.Service
}

func NewBacktestHandler(svc *service.Service) *BacktestHandler {
	return &BacktestHandler{service: svc}
}

// RunBacktest runs one strategy synchronously and returns the full result,
// including its trade log -- there is no separate GET needed right after a
// POST to see what just happened (SPEC.md §2.7, § Non-goals: no async job
// queue at this data size).
func (h *BacktestHandler) RunBacktest(w http.ResponseWriter, r *http.Request) {
	userID, ok := userID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req service.RunBacktestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}

	detail, err := h.service.RunBacktest(r.Context(), userID, req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusCreated, detail)
}

// ListBacktests returns the caller's own runs, newest first.
//
// No runs yet is 200 with an empty array, never 404 -- the same "an absent
// history is a valid answer" rule trading-engine's ListOrders already
// follows.
func (h *BacktestHandler) ListBacktests(w http.ResponseWriter, r *http.Request) {
	userID, ok := userID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	backtests, err := h.service.ListBacktests(r.Context(), userID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if backtests == nil {
		backtests = []service.Backtest{}
	}

	WriteJSON(w, http.StatusOK, service.BacktestsResponse{Backtests: backtests})
}

// GetBacktest returns one run's full detail, including its trade log.
//
// A malformed id in the URL is 400: it can never match a row, but it is a
// client-formed request rather than one that omitted anything (SPEC.md
// §2.7's 404-vs-403 concern is about which user owns a real id, not about a
// string that was never a UUID to begin with).
func (h *BacktestHandler) GetBacktest(w http.ResponseWriter, r *http.Request) {
	userID, ok := userID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "id must be a UUID")
		return
	}

	detail, err := h.service.GetBacktest(r.Context(), userID, id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusOK, detail)
}

// userID reads the subject RequireAuth put on the context and parses it.
//
// A token whose subject is not a UUID fails here rather than being passed
// along as a string, because every store lookup keys on it -- the same
// reasoning trading-engine's identically named helper documents.
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
	case errors.Is(err, service.ErrInvalidRequest):
		WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())

	case errors.Is(err, service.ErrSymbolUnavailable):
		WriteError(w, http.StatusBadRequest, "symbol_unavailable",
			"no historical data is available for that symbol")

	case errors.Is(err, service.ErrDateRangeUnavailable):
		WriteError(w, http.StatusBadRequest, "date_range_unavailable",
			"no historical data is available in the requested date range")

	// 502, not 500: this service is fine, market-data is not.
	case errors.Is(err, service.ErrUpstreamUnavailable):
		WriteError(w, http.StatusBadGateway, "upstream_unavailable",
			"market data is unavailable; the backtest was not run")

	case errors.Is(err, service.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found", "backtest not found")

	default:
		log.Printf("backtesting: %s %s: %v", r.Method, r.URL.Path, err)
		WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
	}
}
