package handler_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/handler"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/service/mock"
)

// Requests go through the real router with a real token, rather than having
// an identity planted on the request context.
//
// pkg/auth exposes no way to seed that context -- the key is unexported -- and
// adding one is a change to pkg/auth, which SPEC.md's Boundaries put behind
// "ask first". Going through RequireAuth is the better test regardless: it
// covers the route being protected at all, which a handler called directly
// cannot tell you.
const testSecret = "test-secret-that-is-long-enough-to-pass-validation"

// The tests drive a real service.Service wired to the in-memory doubles rather
// than a mocked Service.
//
// service.Service is a concrete type, so mocking it would mean adding an
// interface that exists only for the tests -- and the mapping under test here
// is precisely the one between the errors that service actually produces and
// the statuses this handler writes. A fake service would let that mapping
// drift from the real error set and stay green.
func newHandler(t *testing.T) (http.Handler, *mock.TradingStore, *mock.PriceClient, *mock.AccountStore) {
	t.Helper()

	accounts := &mock.AccountStore{Account: service.Account{ID: uuid.New(), Balance: 100000}}
	trading := &mock.TradingStore{}
	prices := &mock.PriceClient{Prices: map[string]float64{"AAPL": 150}}

	h := handler.NewTradingHandler(service.NewService(accounts, trading, prices))
	return handler.NewRouter(h, []byte(testSecret)), trading, prices, accounts
}

func tokenFor(t *testing.T, subject string) string {
	t.Helper()
	token, err := pkgauth.GenerateToken([]byte(testSecret), subject, pkgauth.TokenTypeAccess, time.Minute)
	if err != nil {
		t.Fatalf("generating token: %v", err)
	}
	return token
}

// do sends an authenticated order for a random user.
func do(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doAs(t, h, uuid.NewString(), body)
}

func doAs(t *testing.T, h http.Handler, subject, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/trading/orders", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, subject))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func get(t *testing.T, h http.Handler, path, subject string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if subject != "" {
		req.Header.Set("Authorization", "Bearer "+tokenFor(t, subject))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding error body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestPlaceOrder_FillReturns201AndTheWholeResult(t *testing.T) {
	h, trading, _, _ := newHandler(t)
	orderID, tradeID := uuid.New(), uuid.New()
	price := 150.0
	trading.ExecuteResult = service.PlaceOrderResult{
		Order: service.Order{ID: orderID, Symbol: "AAPL", Side: service.SideBuy,
			Quantity: 10, Status: service.StatusFilled, OrderType: service.OrderTypeMarket, FilledPrice: &price},
		Trade:   service.Trade{ID: tradeID, OrderID: orderID, Symbol: "AAPL", Quantity: 10, Price: 150},
		Balance: 98500,
	}

	rec := do(t, h, `{"symbol":"AAPL","side":"buy","quantity":10}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("got Content-Type %q, want application/json", ct)
	}

	var got service.PlaceOrderResult
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Order.ID != orderID || got.Trade.ID != tradeID {
		t.Errorf("order/trade did not survive encoding: %+v", got)
	}
	if got.Balance != 98500 {
		t.Errorf("got balance %v, want 98500 -- the post-trade balance saves a round trip", got.Balance)
	}
	if got.Order.FilledPrice == nil || *got.Order.FilledPrice != 150 {
		t.Errorf("got filled_price %v, want 150", got.Order.FilledPrice)
	}
}

// The status and code table from SPEC.md §2.9, in one place.
func TestPlaceOrder_ErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		storeErr   error
		priceErr   error
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name: "malformed JSON", body: `{"symbol":`,
			wantStatus: http.StatusBadRequest, wantCode: "invalid_request",
		},
		{
			name: "empty body", body: ``,
			wantStatus: http.StatusBadRequest, wantCode: "invalid_request",
		},
		{
			name: "non-positive quantity", body: `{"symbol":"AAPL","side":"buy","quantity":0}`,
			wantStatus: http.StatusBadRequest, wantCode: "invalid_request",
		},
		{
			name: "unknown side", body: `{"symbol":"AAPL","side":"sideways","quantity":1}`,
			wantStatus: http.StatusBadRequest, wantCode: "invalid_request",
		},
		{
			name: "unknown symbol", body: `{"symbol":"NOSUCH","side":"buy","quantity":1}`,
			wantStatus: http.StatusNotFound, wantCode: "symbol_unavailable",
		},
		{
			name: "market-data down", priceErr: service.ErrUpstreamUnavailable,
			body:       `{"symbol":"AAPL","side":"buy","quantity":1}`,
			wantStatus: http.StatusBadGateway, wantCode: "upstream_unavailable",
		},
		{
			name: "insufficient balance", storeErr: service.ErrInsufficientBalance,
			body:       `{"symbol":"AAPL","side":"buy","quantity":1}`,
			wantStatus: http.StatusBadRequest, wantCode: "insufficient_balance",
		},
		{
			name: "insufficient position", storeErr: service.ErrInsufficientPosition,
			body:       `{"symbol":"AAPL","side":"sell","quantity":1}`,
			wantStatus: http.StatusBadRequest, wantCode: "insufficient_position",
		},
		{
			name: "unexpected store failure", storeErr: errors.New("connection reset"),
			body:       `{"symbol":"AAPL","side":"buy","quantity":1}`,
			wantStatus: http.StatusInternalServerError, wantCode: "internal_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, trading, prices, _ := newHandler(t)
			trading.ExecuteErr = tt.storeErr
			prices.Err = tt.priceErr

			rec := do(t, h, tt.body)

			if rec.Code != tt.wantStatus {
				t.Fatalf("got %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if got := decodeError(t, rec).Code; got != tt.wantCode {
				t.Errorf("got code %q, want %q", got, tt.wantCode)
			}
		})
	}
}

// A broken invariant must not leak as a 4xx that suggests the caller did
// something wrong.
func TestPlaceOrder_MissingAccountIs500(t *testing.T) {
	h, _, _, accounts := newHandler(t)
	accounts.Err = service.ErrAccountNotFound

	rec := do(t, h, `{"symbol":"AAPL","side":"buy","quantity":1}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
	if got := decodeError(t, rec).Code; got != "internal_error" {
		t.Errorf("got code %q, want internal_error", got)
	}
}

// A rejection tells the caller what happened and nothing more. SPEC.md §2.9
// keeps the order id out of this response deliberately -- it is visible
// through GET /trading/orders like every other order.
func TestPlaceOrder_RejectionBodyCarriesOnlyCodeAndMessage(t *testing.T) {
	h, trading, _, _ := newHandler(t)
	trading.ExecuteErr = service.ErrInsufficientBalance

	rec := do(t, h, `{"symbol":"AAPL","side":"buy","quantity":1}`)

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, unwanted := range []string{"order", "order_id", "id", "trade"} {
		if _, present := raw[unwanted]; present {
			t.Errorf("rejection body carries %q: %v", unwanted, raw)
		}
	}
}

// Without a usable identity there is nothing to trade on behalf of. The
// failure has to be a 401, not a zero UUID that queries someone else's account
// -- or nobody's -- without ever looking wrong.
func TestPlaceOrder_MissingOrUnparseableIdentityIs401(t *testing.T) {
	const body = `{"symbol":"AAPL","side":"buy","quantity":1}`

	// This one also pins that the route is authenticated at all. A handler
	// called directly cannot tell you that, and an unauthenticated
	// /trading/orders is the worst bug this service could ship.
	t.Run("no token", func(t *testing.T) {
		h, trading, _, _ := newHandler(t)
		req := httptest.NewRequest(http.MethodPost, "/trading/orders", strings.NewReader(body))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401 -- /trading/orders is not behind RequireAuth", rec.Code)
		}
		if len(trading.ExecuteCalls) != 0 {
			t.Error("an unauthenticated request reached the store")
		}
	})

	t.Run("token signed with a different secret", func(t *testing.T) {
		h, trading, _, _ := newHandler(t)
		other, err := pkgauth.GenerateToken([]byte("a-completely-different-secret-of-sufficient-length"),
			uuid.NewString(), pkgauth.TokenTypeAccess, time.Minute)
		if err != nil {
			t.Fatalf("generating token: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/trading/orders", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+other)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", rec.Code)
		}
		if len(trading.ExecuteCalls) != 0 {
			t.Error("a forged token reached the store")
		}
	})

	// A validly signed token whose subject is not a UUID gets past RequireAuth
	// and has to be caught by the handler.
	t.Run("subject is not a UUID", func(t *testing.T) {
		h, trading, _, _ := newHandler(t)

		rec := doAs(t, h, "not-a-uuid", body)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", rec.Code)
		}
		if len(trading.ExecuteCalls) != 0 {
			t.Error("an unparseable subject reached the store")
		}
	})
}

// The identity comes from the token, never from the body. A caller supplying
// their own account or user id must not be able to trade as anyone else.
func TestPlaceOrder_IdentityComesFromTheTokenNotTheBody(t *testing.T) {
	h, trading, _, accounts := newHandler(t)
	subject := uuid.New()

	rec := doAs(t, h, subject.String(),
		`{"symbol":"AAPL","side":"buy","quantity":1,"user_id":"00000000-0000-0000-0000-000000000001","account_id":"00000000-0000-0000-0000-000000000002"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", rec.Code, rec.Body)
	}
	if len(accounts.Calls) != 1 || accounts.Calls[0] != subject {
		t.Errorf("account was resolved from %v, want the token subject %s", accounts.Calls, subject)
	}
	if got := trading.ExecuteCalls[0].AccountID; got != accounts.Account.ID {
		t.Errorf("order executed against account %s, want the one resolved from the token", got)
	}
}

func TestPlaceOrder_OversizedBodyIsRejected(t *testing.T) {
	h, trading, _, _ := newHandler(t)
	// Valid JSON, just far too much of it.
	body := `{"symbol":"AAPL","side":"buy","quantity":1,"padding":"` + strings.Repeat("x", 128<<10) + `"}`

	rec := do(t, h, body)

	if rec.Code == http.StatusCreated {
		t.Fatal("a 128 KiB body was accepted; the per-request cap is not applied")
	}
	if len(trading.ExecuteCalls) != 0 {
		t.Error("an oversized body reached the store")
	}
}

func TestListOrders_ReturnsTheHistoryWithRejectionsIncluded(t *testing.T) {
	h, trading, _, _ := newHandler(t)
	price := 150.0
	reason := "insufficient_balance"
	trading.Orders = []service.Order{
		{ID: uuid.New(), Symbol: "AAPL", Side: service.SideBuy, Quantity: 10,
			Status: service.StatusFilled, OrderType: service.OrderTypeMarket, FilledPrice: &price},
		{ID: uuid.New(), Symbol: "MSFT", Side: service.SideBuy, Quantity: 1000,
			Status: service.StatusRejected, OrderType: service.OrderTypeMarket, RejectionReason: &reason},
	}

	rec := get(t, h, "/trading/orders", uuid.NewString())

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body)
	}

	var got service.OrdersResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got.Orders) != 2 {
		t.Fatalf("got %d orders, want 2 -- a history that hides its rejections is not an audit trail", len(got.Orders))
	}
	if got.Orders[0].FilledPrice == nil || *got.Orders[0].FilledPrice != 150 {
		t.Errorf("filled order lost its price: %+v", got.Orders[0])
	}
	if got.Orders[1].RejectionReason == nil || *got.Orders[1].RejectionReason != reason {
		t.Errorf("rejected order lost its reason: %+v", got.Orders[1])
	}
	if got.Orders[1].FilledPrice != nil {
		t.Errorf("rejected order reports a fill price %v; it never filled", *got.Orders[1].FilledPrice)
	}
}

// An account that has never traded gets [], not null. A client that maps over
// the field would crash on null, and "never traded" is an answer rather than a
// missing resource -- so not a 404 either.
func TestListOrders_EmptyHistoryIsAnEmptyArray(t *testing.T) {
	h, _, _, _ := newHandler(t)

	rec := get(t, h, "/trading/orders", uuid.NewString())

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); !strings.Contains(body, `"orders":[]`) {
		t.Errorf("got %s, want an empty array", body)
	}
}

func TestListOrders_RequiresAuthenticationAndScopesToTheToken(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		h, _, _, _ := newHandler(t)

		if rec := get(t, h, "/trading/orders", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401 -- GET /trading/orders is not behind RequireAuth", rec.Code)
		}
	})

	// The account is resolved from the token subject and from nowhere else,
	// which is what makes reading another user's history unexpressable rather
	// than merely forbidden.
	t.Run("scoped to the token subject", func(t *testing.T) {
		h, _, _, accounts := newHandler(t)
		subject := uuid.New()

		if rec := get(t, h, "/trading/orders", subject.String()); rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", rec.Code)
		}
		if len(accounts.Calls) != 1 || accounts.Calls[0] != subject {
			t.Errorf("history was resolved from %v, want the token subject %s", accounts.Calls, subject)
		}
	})
}

func TestListOrders_StoreFailureIs500(t *testing.T) {
	h, trading, _, _ := newHandler(t)
	trading.OrdersErr = errors.New("connection reset")

	rec := get(t, h, "/trading/orders", uuid.NewString())

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
	if got := decodeError(t, rec).Code; got != "internal_error" {
		t.Errorf("got code %q, want internal_error", got)
	}
}

// 🔴 The read path degrades instead of failing. With market-data unreachable
// this endpoint is still 200, and latest_price is JSON null rather than 0 --
// 0 is a plausible price and would render as "this is worthless".
func TestListPositions_UpstreamDownIs200WithNullPrices(t *testing.T) {
	h, trading, prices, _ := newHandler(t)
	prices.Err = service.ErrUpstreamUnavailable
	trading.Holdings = []service.Holding{{Symbol: "AAPL", Quantity: 10, AvgCost: 100}}

	rec := get(t, h, "/trading/positions", uuid.NewString())

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 -- the read path must fail open: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"latest_price":null`) {
		t.Errorf("got %s, want latest_price null", body)
	}

	var got service.PositionsResponse
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got.Positions) != 1 || got.Positions[0].Quantity != 10 || got.Positions[0].AvgCost != 100 {
		t.Errorf("the holding itself did not survive the outage: %+v", got.Positions)
	}
	if got.Positions[0].UnrealizedPL != 0 {
		t.Errorf("unrealized_pl = %v, want 0", got.Positions[0].UnrealizedPL)
	}
}

func TestListPositions_PricedHoldingsCarryTheirValuation(t *testing.T) {
	h, trading, _, _ := newHandler(t)
	trading.Holdings = []service.Holding{{Symbol: "AAPL", Quantity: 10, AvgCost: 100}}

	rec := get(t, h, "/trading/positions", uuid.NewString())

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body)
	}
	var got service.PositionsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got.Positions) != 1 {
		t.Fatalf("got %d positions, want 1", len(got.Positions))
	}
	if got.Positions[0].LatestPrice == nil || *got.Positions[0].LatestPrice != 150 {
		t.Fatalf("latest_price = %v, want 150", got.Positions[0].LatestPrice)
	}
	if got.Positions[0].UnrealizedPL != 500 {
		t.Errorf("unrealized_pl = %v, want 500", got.Positions[0].UnrealizedPL)
	}
}

func TestListPositions_NoHoldingsIsAnEmptyArray(t *testing.T) {
	h, _, _, _ := newHandler(t)

	rec := get(t, h, "/trading/positions", uuid.NewString())

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); !strings.Contains(body, `"positions":[]`) {
		t.Errorf("got %s, want an empty array", body)
	}
}

func TestListPositions_RequiresAuthentication(t *testing.T) {
	h, _, _, _ := newHandler(t)

	if rec := get(t, h, "/trading/positions", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 -- GET /trading/positions is not behind RequireAuth", rec.Code)
	}
}

// Fail-open covers the price lookup, not the database behind it.
func TestListPositions_StoreFailureIs500(t *testing.T) {
	h, trading, _, _ := newHandler(t)
	trading.HoldingsErr = errors.New("connection reset")

	rec := get(t, h, "/trading/positions", uuid.NewString())

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
}

func TestPortfolio_ReturnsTheRollup(t *testing.T) {
	h, trading, _, accounts := newHandler(t)
	accounts.Account = service.Account{ID: accounts.Account.ID, Balance: 5000}
	trading.Holdings = []service.Holding{{Symbol: "AAPL", Quantity: 10, AvgCost: 100}}

	rec := get(t, h, "/trading/portfolio", uuid.NewString())

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body)
	}
	var got service.PortfolioResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Balance != 5000 || got.TotalEquity != 6500 || got.TotalUnrealizedPL != 500 {
		t.Errorf("rollup is wrong: %+v", got)
	}
	if len(got.Positions) != 1 {
		t.Errorf("got %d positions, want 1", len(got.Positions))
	}
}

func TestPortfolio_EmptyPortfolioHasAnEmptyPositionsArray(t *testing.T) {
	h, _, _, _ := newHandler(t)

	rec := get(t, h, "/trading/portfolio", uuid.NewString())

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); !strings.Contains(body, `"positions":[]`) {
		t.Errorf("got %s, want an empty array", body)
	}
}

func TestPortfolio_RequiresAuthentication(t *testing.T) {
	h, _, _, _ := newHandler(t)

	if rec := get(t, h, "/trading/portfolio", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 -- GET /trading/portfolio is not behind RequireAuth", rec.Code)
	}
}

// --- GET /trading/trades (Step 20 T2) ---

func TestListTrades_ReturnsTheTradeLog(t *testing.T) {
	h, trading, _, _ := newHandler(t)
	pl := 125.50
	trading.Trades = []service.Trade{
		{ID: uuid.New(), OrderID: uuid.New(), Symbol: "AAPL", Side: service.SideBuy, Quantity: 10, Price: 150},
		{ID: uuid.New(), OrderID: uuid.New(), Symbol: "AAPL", Side: service.SideSell, Quantity: 10, Price: 162.55, RealizedPL: &pl},
	}

	rec := get(t, h, "/trading/trades", uuid.NewString())

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body)
	}
	var got service.TradesResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got.Trades) != 2 {
		t.Fatalf("got %d trades, want 2", len(got.Trades))
	}
	// realized_pl is absent on the buy and present on the sell. Encoding the
	// buy's as 0 would report a break-even round trip that never happened.
	if got.Trades[0].RealizedPL != nil {
		t.Errorf("buy carries realized_pl %v, want null", *got.Trades[0].RealizedPL)
	}
	if got.Trades[1].RealizedPL == nil || *got.Trades[1].RealizedPL != pl {
		t.Errorf("sell's realized_pl = %v, want %v", got.Trades[1].RealizedPL, pl)
	}
}

func TestListTrades_EmptyLogHasAnEmptyArray(t *testing.T) {
	h, _, _, _ := newHandler(t)

	rec := get(t, h, "/trading/trades", uuid.NewString())

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); !strings.Contains(body, `"trades":[]`) {
		t.Errorf("got %s, want an empty array -- null crashes a client that maps over it", body)
	}
}

func TestListTrades_RequiresAuthentication(t *testing.T) {
	h, _, _, _ := newHandler(t)

	if rec := get(t, h, "/trading/trades", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 -- GET /trading/trades is not behind RequireAuth", rec.Code)
	}
}

// The query string is parsed leniently: anything not a usable positive integer
// defaults rather than 400-ing. What reaches the store is asserted directly,
// because that is the value that becomes a LIMIT clause.
func TestListTrades_LimitQueryParam(t *testing.T) {
	tests := []struct {
		query string
		want  int
	}{
		{"", service.DefaultTradeLimit},
		{"?limit=", service.DefaultTradeLimit},
		{"?limit=banana", service.DefaultTradeLimit},
		{"?limit=0", service.DefaultTradeLimit},
		{"?limit=-3", service.DefaultTradeLimit},
		{"?limit=12.5", service.DefaultTradeLimit},
		{"?limit=250", 250},
		{"?limit=10000", service.MaxTradeLimit},
		{"?limit=99999", service.MaxTradeLimit},
	}

	for _, tt := range tests {
		t.Run("limit"+tt.query, func(t *testing.T) {
			h, trading, _, _ := newHandler(t)

			rec := get(t, h, "/trading/trades"+tt.query, uuid.NewString())

			if rec.Code != http.StatusOK {
				t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body)
			}
			if len(trading.TradeLimits) != 1 {
				t.Fatalf("got %d ListTrades calls, want 1", len(trading.TradeLimits))
			}
			if got := trading.TradeLimits[0]; got != tt.want {
				t.Errorf("store received limit %d, want %d", got, tt.want)
			}
		})
	}
}
