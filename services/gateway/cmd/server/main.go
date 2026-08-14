package main

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/gateway/internal/handler"
	"github.com/kpeguero/quantsim/services/gateway/internal/limiter"
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
	// The gateway is where a forged token pays off, so a weak signing secret
	// fails at boot rather than quietly weakening every token it accepts.
	if err := pkgauth.ValidateSecret([]byte(jwtSecret)); err != nil {
		log.Fatal(err)
	}
	authURL := mustParseURL("AUTH_SERVICE_URL", envOrDefault("AUTH_SERVICE_URL", "http://localhost:8081"))
	marketDataURL := mustParseURL("MARKET_DATA_SERVICE_URL", envOrDefault("MARKET_DATA_SERVICE_URL", "http://localhost:8082"))
	port := envOrDefault("PORT", "8080")
	// Loopback by default. In Phase 1 the frontend runs on the same machine,
	// so nothing needs to reach the gateway from off-box; set BIND_ADDR
	// explicitly when deploying it somewhere that does.
	bindAddr := envOrDefault("BIND_ADDR", "127.0.0.1")

	// One transport shared by both proxies, so connections to the backends
	// are pooled rather than reopened per request.
	transport := proxy.NewTransport()
	authProxy := proxy.New(authURL, transport, "auth")
	marketDataProxy := proxy.New(marketDataURL, transport, "market-data")

	rateLimit := rateLimitConfig()

	// Eviction runs for the life of the process. Without it the counter map
	// grows by one entry per distinct source address seen -- which under the
	// distributed attack this exists to blunt is one entry per bot, turning
	// the defence into a memory-exhaustion vector.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if store, ok := rateLimit.Store.(*limiter.MemoryStore); ok && rateLimit.Enabled {
		go store.Run(ctx, rateLimit.IPWindow)
	}

	router := handler.NewRouter(authProxy, marketDataProxy, []byte(jwtSecret), allowedOrigin, rateLimit)

	addr := bindAddr + ":" + port
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	log.Printf("gateway listening on %s (auth=%s, market-data=%s)", addr, authURL, marketDataURL)
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

// rateLimitConfig reads the authentication rate-limiting policy from the
// environment. Every knob has a working default, so nothing new is required
// in .env for the stack to run.
func rateLimitConfig() handler.RateLimitConfig {
	enabled := envOrDefault("RATE_LIMIT_ENABLED", "true") != "false"
	if !enabled {
		// Loud on purpose. This switch exists so a limiter bug cannot become
		// an outage with no way out, which also makes it the sort of thing
		// that gets flipped during an incident and quietly left off.
		log.Print("gateway: WARNING -- RATE_LIMIT_ENABLED=false, authentication rate limiting is DISABLED")
		return handler.RateLimitConfig{Enabled: false}
	}

	ipLimit := mustParsePositiveInt("RATE_LIMIT_IP_REQUESTS", envOrDefault("RATE_LIMIT_IP_REQUESTS", "100"))
	ipWindow := mustParsePositiveDuration("RATE_LIMIT_IP_WINDOW", envOrDefault("RATE_LIMIT_IP_WINDOW", "15m"))

	log.Printf("gateway: auth rate limiting enabled (%d requests per %s per IP)", ipLimit, ipWindow)

	return handler.RateLimitConfig{
		Enabled:  true,
		Store:    limiter.NewMemoryStore(time.Now),
		IPLimit:  ipLimit,
		IPWindow: ipWindow,
	}
}

// mustParsePositiveInt refuses zero and negatives rather than clamping them.
// A limit of 0 would refuse every authentication request -- an outage caused
// by a typo in an env var, which is worth failing loudly at boot instead of
// discovering when nobody can log in.
func mustParsePositiveInt(name, raw string) int {
	v, err := strconv.Atoi(raw)
	if err != nil {
		log.Fatalf("invalid %s: %q is not a number", name, raw)
	}
	if v <= 0 {
		log.Fatalf("invalid %s: %d must be greater than 0 (a limit of 0 refuses every request)", name, v)
	}
	return v
}

func mustParsePositiveDuration(name, raw string) time.Duration {
	d, err := time.ParseDuration(raw)
	if err != nil {
		log.Fatalf("invalid %s: %q is not a duration (e.g. 15m, 1h)", name, raw)
	}
	if d <= 0 {
		log.Fatalf("invalid %s: %s must be greater than 0", name, d)
	}
	return d
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
