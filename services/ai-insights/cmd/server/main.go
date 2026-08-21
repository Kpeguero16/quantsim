package main

import (
	"log"
	"net/http"
	"os"

	"github.com/redis/go-redis/v9"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/cache"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/client"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/handler"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/llm"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/service"
)

// This service has no DATABASE_URL, and that is by design rather than an
// omission: it owns no tables (SPEC.md §2.9). Its inputs are two HTTP
// services and a cache it can run without, so there is no schema to
// coordinate with and nothing here it can corrupt.
func main() {
	// Every /insights/* route revalidates the caller's token rather than
	// trusting the gateway's X-User-ID header, matching trading-engine's and
	// backtesting's precedent -- so a missing or weak signing secret fails at
	// boot here for the same reason it does there.
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET is required")
	}
	if err := pkgauth.ValidateSecret([]byte(jwtSecret)); err != nil {
		log.Fatal(err)
	}

	// Both reused as-is: the same values the gateway already uses to reach
	// the same services. Called directly rather than through the gateway,
	// since a gateway JWT has no meaning between two internal services --
	// though trading-engine authenticates its own routes, so the caller's
	// token is forwarded on that hop (SPEC.md §6.5).
	tradingURL := envOrDefault("TRADING_ENGINE_SERVICE_URL", "http://localhost:8083")
	marketDataURL := envOrDefault("MARKET_DATA_SERVICE_URL", "http://localhost:8082")

	port := envOrDefault("PORT", "8085")
	// Loopback by default, like every other engine service: the gateway is
	// the only thing meant to reach this. Set BIND_ADDR=0.0.0.0 only behind a
	// real network boundary.
	bindAddr := envOrDefault("BIND_ADDR", "127.0.0.1")

	// REDIS_URL is optional here, unlike in market-data where prices depend on
	// it. This service's cache is an optimization it can run without
	// (SPEC.md §2.8, §2.9), so an unset URL is a supported configuration --
	// but a loud one, because running uncached in production is a performance
	// surprise nobody asked for rather than a decision anyone made.
	var insightsCache service.InsightsCache
	var narrativeCache narrative.Cache
	var generationCounter narrative.Counter
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		redisOpts, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Fatalf("invalid REDIS_URL: %v", err)
		}
		redisClient := cache.NewClient(redisOpts)
		defer redisClient.Close()
		insightsCache = cache.NewRedisInsightsCache(redisClient)
		narrativeCache = cache.NewRedisNarrativeCache(redisClient)
		generationCounter = cache.NewRedisGenerationCounter(redisClient)
	} else {
		log.Print("REDIS_URL is not set: every request will be computed from scratch, " +
			"and GET /insights/portfolio/narrative will return no prose -- uncached and " +
			"uncapped generation is the one combination with no cost ceiling")
	}

	svc := service.NewService(
		client.NewTradingClient(tradingURL),
		client.NewMarketDataClient(marketDataURL),
		insightsCache,
	)

	// ANTHROPIC_API_KEY is optional here for the same reason REDIS_URL is:
	// this service answers correctly without it. The narrative is an
	// enhancement over a report that is already complete and already served
	// at GET /insights/portfolio, so an unset key degrades one endpoint's
	// prose rather than stopping the service -- but it is a loud degradation,
	// because running without narrative generation in production is a
	// surprise nobody chose rather than a decision somebody made.
	//
	// generator stays nil until the client lands; the handler treats nil as
	// "not configured" and says so in the response.
	var generator narrative.Generator
	model := envOrDefault("ANTHROPIC_MODEL", defaultModel)
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		generator = llm.NewClient(apiKey, model)
	} else {
		log.Print("ANTHROPIC_API_KEY is not set: GET /insights/portfolio/narrative will return no prose")
	}

	router := handler.NewRouter(
		handler.NewInsightsHandler(svc),
		handler.NewNarrativeHandler(svc, generator, narrativeCache, generationCounter, model),
		[]byte(jwtSecret),
	)

	addr := bindAddr + ":" + port
	log.Printf("ai-insights service listening on %s (trading-engine=%s market-data=%s)",
		addr, tradingURL, marketDataURL)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}

// defaultModel is SPEC.md §2.9's choice, overridable for experimentation and
// echoed in every response so a narrative can be attributed to what wrote it.
const defaultModel = "claude-opus-5"

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
