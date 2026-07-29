package proxy_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kpeguero/quantsim/services/gateway/internal/httperr"
	"github.com/kpeguero/quantsim/services/gateway/internal/proxy"
)

// mustURL fails the test rather than the proxy if a fixture URL is malformed.
func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing %q: %v", raw, err)
	}
	return u
}

// TestForwardsRequestUnchanged is the core contract of SPEC.md 2.2: the
// gateway routes on the same prefix it forwards, so the backend must observe
// the request exactly as the client sent it -- same path, method, body, and
// Authorization header.
func TestForwardsRequestUnchanged(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotAuth   string
		gotBody   string
	)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	p := proxy.New(mustURL(t, backend.URL), proxy.NewTransport(), "market-data")

	req := httptest.NewRequest(http.MethodPost, "/market-data/ingest", strings.NewReader(`{"symbols":["AAPL"]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if gotPath != "/market-data/ingest" {
		t.Errorf("backend saw path %q, want %q (no rewriting)", gotPath, "/market-data/ingest")
	}
	if gotMethod != http.MethodPost {
		t.Errorf("backend saw method %q, want POST", gotMethod)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("backend saw Authorization %q, want it forwarded intact", gotAuth)
	}
	if gotBody != `{"symbols":["AAPL"]}` {
		t.Errorf("backend saw body %q, want it forwarded intact", gotBody)
	}

	if rec.Code != http.StatusCreated {
		t.Errorf("client got status %d, want %d (backend status passed through)", rec.Code, http.StatusCreated)
	}
	if body := rec.Body.String(); body != `{"ok":true}` {
		t.Errorf("client got body %q, want the backend's response", body)
	}
}

// TestForwardsQueryString covers the history/prices endpoints, which carry
// their parameters in the query rather than the path.
func TestForwardsQueryString(t *testing.T) {
	var gotQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
	}))
	defer backend.Close()

	p := proxy.New(mustURL(t, backend.URL), proxy.NewTransport(), "market-data")

	req := httptest.NewRequest(http.MethodGet, "/market-data/history/AAPL?limit=50&timeframe=1Day", nil)
	p.ServeHTTP(httptest.NewRecorder(), req)

	if gotQuery != "limit=50&timeframe=1Day" {
		t.Errorf("backend saw query %q, want it forwarded intact", gotQuery)
	}
}

// TestForwardingHeadersAreNotClientControlled: a client must not be able to
// author the headers that describe it. ReverseProxy's older Director path
// appends to an inbound X-Forwarded-For, leaving the attacker's value first --
// and reading the first entry as "the client IP" is the common pattern, so a
// future rate limiter or audit log keyed on it would be trivially defeated.
func TestForwardingHeadersAreNotClientControlled(t *testing.T) {
	var gotXFF, gotRealIP string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotRealIP = r.Header.Get("X-Real-IP")
	}))
	defer backend.Close()

	p := proxy.New(mustURL(t, backend.URL), proxy.NewTransport(), "auth")

	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	req.RemoteAddr = "203.0.113.9:54321"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	p.ServeHTTP(httptest.NewRecorder(), req)

	if strings.Contains(gotXFF, "1.2.3.4") {
		t.Errorf("backend saw X-Forwarded-For %q; the client-supplied value must be discarded, not appended to", gotXFF)
	}
	if gotXFF != "203.0.113.9" {
		t.Errorf("backend saw X-Forwarded-For %q, want the real connection IP %q", gotXFF, "203.0.113.9")
	}
	if gotRealIP != "" {
		t.Errorf("backend saw X-Real-IP %q, want it stripped", gotRealIP)
	}
}

// TestUnreachableBackendReturnsJSON502 covers SPEC.md 2.8. ReverseProxy's
// default ErrorHandler writes an empty body; the gateway must not, or the
// frontend has nothing to show and the JSON error contract is broken.
func TestUnreachableBackendReturnsJSON502(t *testing.T) {
	// Start a server only to take a port, then close it -- the address is
	// now guaranteed to refuse connections.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	p := proxy.New(mustURL(t, deadURL), proxy.NewTransport(), "market-data")

	req := httptest.NewRequest(http.MethodGet, "/market-data/symbols", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("got Content-Type %q, want application/json", ct)
	}

	var body httperr.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not the JSON error shape: %v (body: %q)", err, rec.Body.String())
	}
	if body.Code != "upstream_unavailable" {
		t.Errorf("got code %q, want upstream_unavailable", body.Code)
	}
	if body.Message == "" {
		t.Error("got empty message, want a human-readable one")
	}
}
