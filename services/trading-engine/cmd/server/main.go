package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/cache"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/client"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/handler"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/service"
	"github.com/kpeguero/quantsim/services/trading-engine/internal/store"
)

// dbPingTimeout bounds the one connection attempt made at boot. Short, because
// its only job is to tell "the database is there" from "it is not" before the
// service starts accepting orders.
const dbPingTimeout = 5 * time.Second

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	// Every /trading/* route revalidates the caller's token rather than
	// trusting the gateway's X-User-ID header (SPEC.md §2.11), which makes a
	// missing or weak signing secret a boot-time failure here for the same
	// reason it is one at the gateway -- not a surprise discovered once orders
	// are already flowing.
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET is required")
	}
	if err := pkgauth.ValidateSecret([]byte(jwtSecret)); err != nil {
		log.Fatal(err)
	}

	// Reused as-is, the same value the gateway already uses to reach the same
	// service. The trading engine calls market-data directly rather than
	// through the gateway (SPEC.md §2.2).
	marketDataURL := os.Getenv("MARKET_DATA_SERVICE_URL")
	if marketDataURL == "" {
		marketDataURL = "http://localhost:8082"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}
	// Loopback by default, like every other engine service: the gateway is the
	// only thing meant to reach this. Set BIND_ADDR=0.0.0.0 only behind a real
	// network boundary.
	bindAddr := os.Getenv("BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	// pgxpool.New parses the DSN but does not dial, so without this a wrong
	// DATABASE_URL first surfaces on the request that needs it -- and here the
	// request that needs it is one that moves money.
	pingCtx, cancel := context.WithTimeout(context.Background(), dbPingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		log.Fatalf("database unreachable: %v", err)
	}

	// REDIS_URL is optional here, and it buys one thing only: dropping a
	// user's cached portfolio report when their fill makes it stale
	// (SPEC.md Step 24 §2.1). Orders place identically without it -- the
	// service degrades to the behaviour that existed before that step, where a
	// report could lag a trade by up to the cache TTL.
	//
	// So an unset URL is a supported configuration and NOT a fatal one. It is
	// still worth a line, because a reader seeing their own trade missing from
	// their report has no way to tell that from a bug.
	var insightsInvalidator service.InsightsInvalidator
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		redisOpts, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Fatalf("invalid REDIS_URL: %v", err)
		}
		redisClient := cache.NewClient(redisOpts)
		defer redisClient.Close()
		insightsInvalidator = cache.NewRedisInsightsInvalidator(redisClient)
	} else {
		log.Print("REDIS_URL is not set: a fill will not invalidate the placing user's " +
			"cached portfolio report, so GET /insights/portfolio can lag a trade by up " +
			"to its cache TTL")
	}

	tradingStore := store.NewPostgresTradingStore(pool)
	priceClient := client.NewMarketDataClient(marketDataURL)
	svc := service.NewService(tradingStore, tradingStore, priceClient, insightsInvalidator)
	tradingHandler := handler.NewTradingHandler(svc)
	router := handler.NewRouter(tradingHandler, []byte(jwtSecret))

	addr := bindAddr + ":" + port
	log.Printf("trading-engine service listening on %s (market-data=%s)", addr, marketDataURL)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}
