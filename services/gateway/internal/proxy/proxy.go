// Package proxy builds the reverse proxies the gateway puts in front of each
// backend service.
package proxy

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/kpeguero/quantsim/services/gateway/internal/httperr"
)

// Timeouts for the transport shared by every proxy. ResponseHeaderTimeout is
// the important one: without it a backend that accepts a connection and then
// hangs pins a gateway goroutine and a client connection indefinitely. There
// is deliberately no overall request timeout -- that would cap streaming
// responses, and time-to-first-header is the meaningful bound here.
const (
	dialTimeout           = 5 * time.Second
	responseHeaderTimeout = 30 * time.Second
)

// NewTransport returns the http.Transport every proxy should share, so that
// connections to the backends are pooled rather than reopened per request.
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: dialTimeout}).DialContext
	t.ResponseHeaderTimeout = responseHeaderTimeout
	return t
}

// New returns a reverse proxy forwarding to target. The request path is
// forwarded unchanged -- the gateway routes on the same prefix each backend
// already serves (/auth, /market-data), so a request behaves identically
// whether it arrives through the gateway or directly at the backend.
//
// serviceName appears only in the 502 body and the error log, never in a
// forwarded header.
func New(target *url.URL, transport http.RoundTripper, serviceName string) *httputil.ReverseProxy {
	p := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			// Forwarding headers describe the client, so a client must not be
			// able to author them. X-Real-IP has no stdlib handling, so drop
			// it outright; SetXForwarded below replaces the X-Forwarded-*
			// family with values derived from the real connection.
			//
			// This matters for the same reason X-User-ID is stripped at the
			// edge: ReverseProxy's older Director path *appends* to an
			// inbound X-Forwarded-For, leaving an attacker-chosen value in
			// front -- and the usual way to read that header is to take the
			// first entry as the client IP.
			r.Out.Header.Del("X-Real-IP")

			r.SetURL(target)  // scheme + host; path is joined, not rewritten
			r.SetXForwarded() // X-Forwarded-For/-Host/-Proto from the connection

			// SetURL leaves Out.Host as the client sent it, which is what the
			// backends already saw before this package existed. Kept explicit
			// so a future change to it is a decision rather than a surprise.
			r.Out.Host = r.In.Host
		},
	}

	// ReverseProxy's default ErrorHandler writes a bare 502 with an empty
	// body, which breaks the JSON error contract every other QuantSim
	// endpoint honours. This is also the error a developer hits most often
	// (forgot to start a backend), so it is worth making legible.
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("gateway: %s proxy error for %s %s: %v", serviceName, r.Method, r.URL.Path, err)
		httperr.Write(w, http.StatusBadGateway, "upstream_unavailable", serviceName+" service is unavailable")
	}

	return p
}
