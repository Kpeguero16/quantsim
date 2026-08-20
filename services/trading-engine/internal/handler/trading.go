package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
)

// maxBodyBytes matches the cap auth has applied per request since Step 9. An
// order payload is a couple of hundred bytes; this is about making an
// unbounded body impossible to send, not about tightness.
const maxBodyBytes = 64 << 10

type TradingHandler struct {
	service *service.Service
}

func NewTradingHandler(svc *service.Service) *TradingHandler {
	return &TradingHandler{service: svc}
}

// PlaceOrder executes a market order and returns the fill.
//
// 201 rather than 200: a successful order creates an order, a trade, and
// usually a position. A rejected one gets the standard error shape and its
// order id is deliberately not echoed back -- SPEC.md §2.9 keeps the rejection
// response to code and message, and the row is visible through
// GET /trading/orders like any other.
func (h *TradingHandler) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	userID, ok := userID(r)
	if !ok {
		// RequireAuth should have made this unreachable. It is handled rather
		// than assumed because the alternative -- a zero UUID reaching the
		// store -- would query someone else's account, or nobody's, without
		// ever looking wrong.
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req service.PlaceOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}

	result, err := h.service.PlaceOrder(r.Context(), userID, req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusCreated, result)
}

// ListOrders returns the caller's order history.
//
// An account with no orders is 200 with an empty array, never 404: "you have
// never traded" is an answer, not a missing resource.
func (h *TradingHandler) ListOrders(w http.ResponseWriter, r *http.Request) {
	userID, ok := userID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	orders, err := h.service.Orders(r.Context(), userID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	// The store already returns a non-nil slice; this is the encoding boundary
	// enforcing its own promise, because the difference between [] and null is
	// the difference between a client rendering an empty list and one crashing
	// on it.
	if orders == nil {
		orders = []service.Order{}
	}

	WriteJSON(w, http.StatusOK, service.OrdersResponse{Orders: orders})
}

// ListTrades returns the caller's executions, oldest first.
//
// Like ListOrders, an account that has never traded is 200 with an empty
// array. The `limit` query parameter is parsed leniently: anything that is not
// a usable positive integer falls back to the default rather than 400-ing,
// because there is no interpretation of "limit=banana" that a caller is better
// off having refused than defaulted.
func (h *TradingHandler) ListTrades(w http.ResponseWriter, r *http.Request) {
	userID, ok := userID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	// A parse failure yields 0, which normalizeTradeLimit already maps to the
	// default -- so the unparseable and absent cases need no separate branch.
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	trades, err := h.service.Trades(r.Context(), userID, limit)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if trades == nil {
		trades = []service.Trade{}
	}

	WriteJSON(w, http.StatusOK, service.TradesResponse{Trades: trades})
}

// ListPositions returns the caller's open holdings, priced where possible.
//
// A symbol market-data cannot price still appears, with latest_price null --
// this endpoint degrades rather than failing (SPEC.md §2.9). The only thing
// that produces a non-200 here is being unable to read the holdings at all.
func (h *TradingHandler) ListPositions(w http.ResponseWriter, r *http.Request) {
	userID, ok := userID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	positions, err := h.service.Positions(r.Context(), userID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if positions == nil {
		positions = []service.Position{}
	}

	WriteJSON(w, http.StatusOK, service.PositionsResponse{Positions: positions})
}

// Portfolio returns cash, positions and totals in one response, so a dashboard
// needs one call rather than composing three.
//
// It inherits the positions endpoint's fail-open behaviour: an unpriceable
// position is valued at cost rather than dropped from the total.
func (h *TradingHandler) Portfolio(w http.ResponseWriter, r *http.Request) {
	userID, ok := userID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
		return
	}

	portfolio, err := h.service.Portfolio(r.Context(), userID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if portfolio.Positions == nil {
		portfolio.Positions = []service.Position{}
	}

	WriteJSON(w, http.StatusOK, portfolio)
}

// userID reads the subject RequireAuth put on the context and parses it.
//
// A token whose subject is not a UUID fails here rather than being passed
// along as a string, because every store lookup keys on it: an unparseable
// subject that reached the database would either match nothing or, worse,
// match something.
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

// writeServiceError is the single place service errors become statuses, so
// the mapping in SPEC.md §2.9 lives in one readable list rather than being
// spread across handlers.
func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidOrder):
		WriteError(w, http.StatusBadRequest, "invalid_request",
			"symbol, side (buy or sell) and a quantity of at least 0.0001 are required")

	case errors.Is(err, service.ErrInsufficientBalance):
		WriteError(w, http.StatusBadRequest, "insufficient_balance",
			"account balance does not cover this order")

	case errors.Is(err, service.ErrInsufficientPosition):
		WriteError(w, http.StatusBadRequest, "insufficient_position",
			"cannot sell more than the position holds")

	case errors.Is(err, service.ErrSymbolUnavailable):
		WriteError(w, http.StatusNotFound, "symbol_unavailable",
			"no price is available for that symbol")

	// 502, not 500: the trading engine is fine, the service it depends on is
	// not, and the order was rejected rather than filled at an unknown price.
	case errors.Is(err, service.ErrUpstreamUnavailable):
		WriteError(w, http.StatusBadGateway, "upstream_unavailable",
			"market data is unavailable; the order was not filled")

	// A broken invariant: every registered user has an account. Logged
	// because nobody will report it -- the caller just sees a 500 -- and it
	// means something upstream created a user without one.
	case errors.Is(err, service.ErrAccountNotFound):
		log.Printf("trading-engine: %s %s: authenticated user has no account row", r.Method, r.URL.Path)
		WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")

	default:
		log.Printf("trading-engine: %s %s: %v", r.Method, r.URL.Path, err)
		WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
	}
}
