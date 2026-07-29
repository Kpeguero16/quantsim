package main

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/kpeguero/quantsim/services/gateway/internal/handler"
	"github.com/kpeguero/quantsim/services/gateway/internal/proxy"
)

// allowedOrigin is the Vite dev server. Hardcoded rather than env-driven:
// Phase 1 has exactly one frontend origin, and a CORS origin is not a knob
// worth exposing before something needs to turn it.
const allowedOrigin = "http://localhost:5173"

// readHeaderTimeout bounds how long a client may take to send its request
// headers. The gateway is the only service meant to accept outside
// connections, which makes it the one that needs Slowloris protection.
const readHeaderTimeout = 10 * time.Second

func main() {
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET is required")
	}
	authURL := mustParseURL("AUTH_SERVICE_URL", envOrDefault("AUTH_SERVICE_URL", "http://localhost:8081"))
	marketDataURL := mustParseURL("MARKET_DATA_SERVICE_URL", envOrDefault("MARKET_DATA_SERVICE_URL", "http://localhost:8082"))
	port := envOrDefault("PORT", "8080")

	// One transport shared by both proxies, so connections to the backends
	// are pooled rather than reopened per request.
	transport := proxy.NewTransport()
	authProxy := proxy.New(authURL, transport, "auth")
	marketDataProxy := proxy.New(marketDataURL, transport, "market-data")

	router := handler.NewRouter(authProxy, marketDataProxy, []byte(jwtSecret), allowedOrigin)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	log.Printf("gateway listening on :%s (auth=%s, market-data=%s)", port, authURL, marketDataURL)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustParseURL(name, raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		log.Fatalf("invalid %s: %v", name, err)
	}
	if u.Scheme == "" || u.Host == "" {
		log.Fatalf("invalid %s: %q needs a scheme and host (e.g. http://localhost:8081)", name, raw)
	}
	return u
}
