package proxy_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// failingTransport fails every request, so the ErrorHandler path is exercised
// without depending on a port staying closed between bind and dial.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp 127.0.0.1:8082: connect: connection refused")
}

// TestUnreachableBackendReturnsJSON502 covers SPEC.md 2.8. ReverseProxy's
// default ErrorHandler writes an empty body; the gateway must not, or the
// frontend has nothing to show and the JSON error contract is broken.
func TestUnreachableBackendReturnsJSON502(t *testing.T) {
	p := proxy.New(mustURL(t, "http://market-data.invalid"), failingTransport{}, "market-data")

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

// countingBackend holds each request long enough for concurrent ones to overlap,
// and records the TCP connections it accepts and the most requests it ever had
// in flight together.
type countingBackend struct {
	*httptest.Server
	accepted atomic.Int64
	inFlight atomic.Int64
	peak     atomic.Int64
}

func newCountingBackend(t *testing.T) *countingBackend {
	t.Helper()
	b := &countingBackend{}
	b.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := b.inFlight.Add(1)
		defer b.inFlight.Add(-1)
		for {
			peak := b.peak.Load()
			if n <= peak || b.peak.CompareAndSwap(peak, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	b.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			b.accepted.Add(1)
		}
	}
	b.Start()
	t.Cleanup(b.Close)
	return b
}

// sendRounds runs rounds of concurrency requests through each handler at once,
// waiting for one round to finish before starting the next.
func sendRounds(t *testing.T, handlers []http.Handler, concurrency, rounds int) {
	t.Helper()
	for round := range rounds {
		var wg sync.WaitGroup
		for _, h := range handlers {
			for range concurrency {
				wg.Add(1)
				go func() {
					defer wg.Done()
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/market-data/prices/AAPL", nil))
					if rec.Code != http.StatusOK {
						t.Errorf("round %d: got status %d, want 200", round, rec.Code)
					}
				}()
			}
		}
		wg.Wait()
	}
}

// TestTransportReusesConnectionsUnderConcurrency is the regression test for
// ephemeral port exhaustion. Every connection the pool cannot keep is closed,
// sits in TIME_WAIT holding a port, and is replaced by a fresh dial on the next
// request. With Go's default of 2 idle connections per host that happened to
// nearly every request above 2 in flight: at 100 concurrent clients the gateway
// ran out of ports and 60% of price reads came back as 502s.
//
// All five backends run at once because the gateway shares one transport among
// them, so a limit on total idle connections would have to cover the sum.
//
// The bound allows some margin because the transport may drop the odd connection
// under scheduling pressure, which -race reliably produces. Reuse opens about
// concurrency connections over all the rounds; the bug opens about
// concurrency * rounds.
func TestTransportReusesConnectionsUnderConcurrency(t *testing.T) {
	const (
		backends    = 5
		concurrency = 100
		rounds      = 5
	)

	transport := proxy.NewTransport()
	t.Cleanup(transport.CloseIdleConnections)

	servers := make([]*countingBackend, backends)
	handlers := make([]http.Handler, backends)
	for i := range servers {
		servers[i] = newCountingBackend(t)
		handlers[i] = proxy.New(mustURL(t, servers[i].URL), transport, "backend")
	}

	sendRounds(t, handlers, concurrency, rounds)

	for i, s := range servers {
		if got := s.accepted.Load(); got > 2*concurrency {
			t.Errorf("backend %d accepted %d connections over %d rounds of %d concurrent requests, want at most %d -- "+
				"the rest are connections the pool closed and redialed",
				i, got, rounds, concurrency, 2*concurrency)
		}
	}
}

// TestTransportCapsConnectionsAbovePoolSize covers the failure that a larger
// idle pool alone only moves: with idle limits of 128 and no cap on connections,
// 500 concurrent clients still overflowed the pool, and the gateway ran out of
// ports again. Above the cap, requests must queue for a connection rather than
// dial a new one.
func TestTransportCapsConnectionsAbovePoolSize(t *testing.T) {
	const (
		concurrency = 500
		rounds      = 3
		maxConns    = 128
	)

	transport := proxy.NewTransport()
	t.Cleanup(transport.CloseIdleConnections)
	backend := newCountingBackend(t)
	h := proxy.New(mustURL(t, backend.URL), transport, "backend")

	sendRounds(t, []http.Handler{h}, concurrency, rounds)

	// A dropped connection frees its slot while the backend is still finishing
	// that request, so the peak can pass the cap by a little. No cap reaches 500.
	if got := backend.peak.Load(); got > maxConns+maxConns/4 {
		t.Errorf("backend saw %d requests in flight from %d concurrent clients, want about %d", got, concurrency, maxConns)
	}
	if got := backend.accepted.Load(); got > 2*maxConns {
		t.Errorf("backend accepted %d connections over %d rounds, want at most %d", got, rounds, 2*maxConns)
	}
}

// TestSaturatedBackendQueuesOnlyItsOwnRequests pins down what the cap costs. A
// backend holding every connection makes further requests to it wait, and that
// wait is bounded by the client rather than by ResponseHeaderTimeout. It must
// not reach the other backends, and a client that gives up must get out.
func TestSaturatedBackendQueuesOnlyItsOwnRequests(t *testing.T) {
	const maxConns = 128

	release := make(chan struct{})
	var arrived atomic.Int64
	saturated := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if arrived.Add(1) == maxConns {
			close(saturated)
		}
		<-release
	}))
	t.Cleanup(hung.Close)
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(healthy.Close)

	transport := proxy.NewTransport()
	t.Cleanup(transport.CloseIdleConnections)
	hungProxy := proxy.New(mustURL(t, hung.URL), transport, "hung")
	healthyProxy := proxy.New(mustURL(t, healthy.URL), transport, "healthy")

	var saturating sync.WaitGroup
	for range maxConns {
		saturating.Add(1)
		go func() {
			defer saturating.Done()
			hungProxy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
		}()
	}
	// Runs before the servers' Close, which would otherwise wait on these forever.
	defer saturating.Wait()
	defer close(release)
	select {
	case <-saturated:
	case <-time.After(10 * time.Second):
		t.Fatalf("only %d of %d requests reached the backend", arrived.Load(), maxConns)
	}

	rec := httptest.NewRecorder()
	healthyProxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthy backend: got status %d, want 200 while another backend is saturated", rec.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	rec = httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		hungProxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx))
	}()
	select {
	case <-done:
		if rec.Code != http.StatusBadGateway {
			t.Errorf("queued request whose client gave up: got status %d, want 502", rec.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued request did not return after its client gave up")
	}
}
