package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/handler"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service/mock"
)

// Long enough to satisfy pkgauth.ValidateSecret, which every service checks
// at boot.
var jwtSecret = []byte("test-secret-that-is-long-enough-for-hs256")

const testUserID = "6f1e0e5a-1b2c-4d3e-8f90-abcdef012345"

func bar(n int, close float64) service.Bar {
	return service.Bar{Timestamp: time.Date(2026, 3, n, 20, 0, 0, 0, time.UTC), Close: close}
}

func accessToken(t *testing.T, subject string) string {
	t.Helper()
	tok, err := pkgauth.GenerateToken(jwtSecret, subject, pkgauth.TokenTypeAccess, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	return tok
}

func newRouter(trading *mock.TradingClient, history *mock.HistoryClient) http.Handler {
	// nil cache: these tests are about HTTP, and a cache would make a second
	// request in one test invisible to the mocks the assertions read.
	svc := service.NewService(trading, history, nil)
	return handler.NewRouter(handler.NewInsightsHandler(svc), jwtSecret)
}

// get issues GET /insights/portfolio with the given Authorization value
// verbatim -- "" meaning the header is omitted entirely.
func get(t *testing.T, router http.Handler, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/insights/portfolio", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// A funded, traded account: AAPL held over three days, both benchmarks
// covering the same days so nothing shortens the window.
func tradedAccount() (*mock.TradingClient, *mock.HistoryClient) {
	series := []service.Bar{bar(2, 100), bar(3, 110), bar(4, 105)}
	history := &mock.HistoryClient{Bars: map[string][]service.Bar{
		"AAPL": series, "SPY": series, "QQQ": series,
	}}
	trading := &mock.TradingClient{
		TradesResult: []service.Trade{{
			ID: "t1", Symbol: "AAPL", Side: service.SideBuy, Quantity: 10, Price: 100,
			ExecutedAt: time.Date(2026, 3, 2, 15, 0, 0, 0, time.UTC),
		}},
		PortfolioResult: service.LivePortfolio{
			Balance:   service.StartingBalance - 1000,
			Positions: []service.LivePosition{{Symbol: "AAPL", Quantity: 10}},
		},
	}
	return trading, history
}

func TestPortfolioInsights_RequiresAValidAccessToken(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
	}{
		{"no header", ""},
		{"not a bearer token", "Basic abc"},
		{"garbage token", "Bearer not-a-jwt"},
		{"signed with the wrong secret", "Bearer " + wrongSecretToken(t)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trading, history := tradedAccount()
			rec := get(t, newRouter(trading, history), tt.authorization)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want 401", rec.Code)
			}
			// The point of revalidating here rather than trusting the
			// gateway: a rejected token must never reach trading-engine.
			if n := len(trading.Authorizations()); n != 0 {
				t.Errorf("called trading-engine %d times with a rejected token", n)
			}
		})
	}
}

func wrongSecretToken(t *testing.T) string {
	t.Helper()
	tok, err := pkgauth.GenerateToken(
		[]byte("a-different-secret-also-long-enough-for-hs256"),
		testUserID, pkgauth.TokenTypeAccess, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	return tok
}

// A token this service accepts but whose subject is not a UUID is refused
// before the onward hop, rather than carried along as a string.
func TestPortfolioInsights_ANonUUIDSubjectIs401(t *testing.T) {
	trading, history := tradedAccount()
	rec := get(t, newRouter(trading, history), "Bearer "+accessToken(t, "not-a-uuid"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
	if n := len(trading.Authorizations()); n != 0 {
		t.Errorf("called trading-engine %d times for a malformed subject", n)
	}
}

func TestPortfolioInsights_ReturnsTheReportAndForwardsTheToken(t *testing.T) {
	trading, history := tradedAccount()
	token := accessToken(t, testUserID)

	rec := get(t, newRouter(trading, history), "Bearer "+token)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q", ct)
	}

	var got service.PortfolioInsights
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Risk.State != service.StateOK {
		t.Errorf("risk state: got %q, want ok", got.Risk.State)
	}
	if got.AsOfDate != "2026-03-04" {
		t.Errorf("as_of_date: got %q, want 2026-03-04", got.AsOfDate)
	}
	if got.Window.StartDate != "2026-03-02" || got.Window.TradingDays != 3 {
		t.Errorf("window: got %+v", got.Window)
	}
	if got.ComputedAt.IsZero() {
		t.Error("computed_at missing from the response")
	}

	// The exact header the caller sent, unmodified, on both onward calls.
	for i, auth := range trading.Authorizations() {
		if auth != "Bearer "+token {
			t.Errorf("onward call %d forwarded %q", i, auth)
		}
	}
}

// The acceptance criterion, checked against the bytes rather than against a
// decoded struct -- decoding turns both null and an absent key into a nil
// slice, so only the raw body can tell the three apart.
func TestPortfolioInsights_PositionsIsAlwaysAnArray(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		trading, history := tradedAccount()
		body := get(t, newRouter(trading, history), "Bearer "+accessToken(t, testUserID)).Body.String()
		if !strings.Contains(body, `"positions":[{`) {
			t.Errorf("expected a populated positions array, got: %s", body)
		}
	})

	// ComputeRisk's own degraded path, which is a different branch from the
	// never-traded one below: the account HAS traded, but the calendar holds
	// a single date so there is not one return to measure.
	t.Run("too few trading days", func(t *testing.T) {
		oneDay := []service.Bar{bar(2, 100)}
		history := &mock.HistoryClient{Bars: map[string][]service.Bar{
			"AAPL": oneDay, "SPY": oneDay, "QQQ": oneDay,
		}}
		trading, _ := tradedAccount()

		rec := get(t, newRouter(trading, history), "Bearer "+accessToken(t, testUserID))
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"state":"insufficient_data"`) {
			t.Errorf("a one-day calendar should be insufficient_data, got: %s", body)
		}
		if !strings.Contains(body, `"positions":[]`) {
			t.Errorf("expected positions to be [], got: %s", body)
		}
	})

	t.Run("never traded", func(t *testing.T) {
		trading := &mock.TradingClient{TradesResult: []service.Trade{}}
		body := get(t, newRouter(trading, &mock.HistoryClient{}), "Bearer "+accessToken(t, testUserID)).Body.String()
		if !strings.Contains(body, `"positions":[]`) {
			t.Errorf("expected positions to be [], got: %s", body)
		}
		if !strings.Contains(body, `"benchmarks":[]`) {
			t.Errorf("expected benchmarks to be [], got: %s", body)
		}
		if !strings.Contains(body, `"findings":[]`) {
			t.Errorf("expected findings to be [], got: %s", body)
		}
		if strings.Contains(body, `:null`) {
			t.Errorf("something marshalled as null: %s", body)
		}
	})
}

// A figure that is legitimately zero must still appear. concentration_hhi is
// the case that matters: 0 for an all-cash portfolio is the meaningful answer
// §2.5 argues for, and an omitted key would read as "not computed".
func TestPortfolioInsights_AZeroFigureIsStillReported(t *testing.T) {
	series := []service.Bar{bar(2, 100), bar(3, 110), bar(4, 105)}
	history := &mock.HistoryClient{Bars: map[string][]service.Bar{
		"AAPL": series, "SPY": series, "QQQ": series,
	}}
	// Bought and fully sold: the account is all cash, so HHI is 0.
	trading := &mock.TradingClient{
		TradesResult: []service.Trade{
			{ID: "t1", Symbol: "AAPL", Side: service.SideBuy, Quantity: 10, Price: 100,
				ExecutedAt: time.Date(2026, 3, 2, 15, 0, 0, 0, time.UTC)},
			{ID: "t2", Symbol: "AAPL", Side: service.SideSell, Quantity: 10, Price: 110,
				ExecutedAt: time.Date(2026, 3, 3, 15, 0, 0, 0, time.UTC)},
		},
		PortfolioResult: service.LivePortfolio{Balance: service.StartingBalance + 100},
	}

	rec := get(t, newRouter(trading, history), "Bearer "+accessToken(t, testUserID))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	for _, key := range []string{"concentration_hhi", "largest_position_pct", "cash_weight_pct", "max_drawdown_pct"} {
		if !strings.Contains(body, `"`+key+`"`) {
			t.Errorf("%s omitted from an ok section: %s", key, body)
		}
	}
	if !strings.Contains(body, `"concentration_hhi":0`) {
		t.Errorf("all-cash portfolio did not report concentration_hhi 0: %s", body)
	}
}

func TestPortfolioInsights_ErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		trading  *mock.TradingClient
		history  *mock.HistoryClient
		wantCode int
		wantBody string
	}{
		{
			name: "a held symbol has no bars",
			trading: func() *mock.TradingClient {
				trading, _ := tradedAccount()
				return trading
			}(),
			// Benchmarks present, AAPL absent.
			history: &mock.HistoryClient{Bars: map[string][]service.Bar{
				"SPY": {bar(2, 100), bar(3, 110)}, "QQQ": {bar(2, 100), bar(3, 110)},
			}},
			wantCode: http.StatusNotFound,
			wantBody: "symbol_unavailable",
		},
		{
			name:     "trading-engine refuses the forwarded token",
			trading:  &mock.TradingClient{TradesErr: service.ErrUnauthorized},
			history:  &mock.HistoryClient{},
			wantCode: http.StatusUnauthorized,
			wantBody: "invalid_token",
		},
		{
			name:     "trading-engine is down",
			trading:  &mock.TradingClient{TradesErr: service.ErrUpstreamUnavailable},
			history:  &mock.HistoryClient{},
			wantCode: http.StatusBadGateway,
			wantBody: "upstream_unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, newRouter(tt.trading, tt.history), "Bearer "+accessToken(t, testUserID))

			if rec.Code != tt.wantCode {
				t.Fatalf("got %d, want %d: %s", rec.Code, tt.wantCode, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body %s does not mention %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestHealthzNeedsNoToken(t *testing.T) {
	trading, history := tradedAccount()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	newRouter(trading, history).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}

// The handler is a defence in its own right, not only via the router.
//
// Every request that reaches it through NewRouter has already been through
// RequireAuth, which always puts a subject on the context -- so this branch is
// unreachable in the current wiring. It is tested anyway because the guard's
// whole job is to hold if that wiring ever changes: mounting this method
// outside the authenticated group is a one-line mistake, and without the check
// it would serve a report built from an empty Authorization header rather than
// refusing.
func TestPortfolioInsights_RefusesARequestThatSkippedTheMiddleware(t *testing.T) {
	trading, history := tradedAccount()
	h := handler.NewInsightsHandler(service.NewService(trading, history, nil))

	req := httptest.NewRequest(http.MethodGet, "/insights/portfolio", nil)
	rec := httptest.NewRecorder()
	h.PortfolioInsights(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if n := len(trading.Authorizations()); n != 0 {
		t.Errorf("called trading-engine %d times with no authenticated subject", n)
	}
}

// SPEC §2.10: the 404 names the symbol. "One of the portfolio's symbols" makes
// the user guess which of their holdings the system cannot price.
func TestPortfolioInsights_TheSymbolUnavailable404NamesTheSymbol(t *testing.T) {
	trading, _ := tradedAccount()
	// Benchmarks present; the held symbol is not.
	history := &mock.HistoryClient{Bars: map[string][]service.Bar{
		"SPY": {bar(2, 100), bar(3, 110)}, "QQQ": {bar(2, 100), bar(3, 110)},
	}}

	rec := get(t, newRouter(trading, history), "Bearer "+accessToken(t, testUserID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AAPL") {
		t.Errorf("the 404 does not name the symbol: %s", rec.Body.String())
	}
}

// A missing BENCHMARK is named too. The user does not hold SPY, so a message
// about "one of the portfolio's symbols" would be actively misleading here.
func TestPortfolioInsights_AMissing404NamesTheBenchmarkToo(t *testing.T) {
	trading, _ := tradedAccount()
	series := []service.Bar{bar(2, 100), bar(3, 110)}
	history := &mock.HistoryClient{Bars: map[string][]service.Bar{
		"AAPL": series, "QQQ": series, // no SPY
	}}

	rec := get(t, newRouter(trading, history), "Bearer "+accessToken(t, testUserID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "SPY") {
		t.Errorf("the 404 does not name the benchmark: %s", rec.Body.String())
	}
}

// A never-traded account is a 200, not a 404 and not an error. The most
// likely wrong answer for a brand-new user is the one this pins.
func TestPortfolioInsights_ANeverTradedAccountIs200(t *testing.T) {
	trading := &mock.TradingClient{TradesResult: []service.Trade{}}
	rec := get(t, newRouter(trading, &mock.HistoryClient{}), "Bearer "+accessToken(t, testUserID))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "symbol_unavailable") {
		t.Errorf("a brand-new user was 404'd over a benchmark: %s", body)
	}
}

// The empty-symbol fallback, which nothing else reaches.
//
// A SymbolUnavailableError carrying no symbol is not reachable from the real
// client today, so this is the same kind of defensive branch as the
// missing-subject check above -- and the same argument applies: it is one line
// away from mattering, and untested it would silently degrade to
// "no historical data is available for ", a sentence that stops mid-thought.
func TestPortfolioInsights_A404WithNoSymbolStillReadsAsASentence(t *testing.T) {
	trading, _ := tradedAccount()
	series := []service.Bar{bar(2, 100), bar(3, 110)}
	history := &mock.HistoryClient{
		Bars: map[string][]service.Bar{"AAPL": series, "SPY": series, "QQQ": series},
		Errs: map[string]error{"AAPL": service.SymbolUnavailable("")},
	}

	rec := get(t, newRouter(trading, history), "Bearer "+accessToken(t, testUserID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "one of the portfolio's symbols") {
		t.Errorf("expected the generic fallback, got: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `available for "`) {
		t.Errorf("the message stops mid-thought: %s", rec.Body.String())
	}
}
