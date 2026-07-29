package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/kpeguero/quantsim/services/market-data/internal/alpaca"
	"github.com/kpeguero/quantsim/services/market-data/internal/cache"
	"github.com/kpeguero/quantsim/services/market-data/internal/handler"
	"github.com/kpeguero/quantsim/services/market-data/internal/service"
	"github.com/kpeguero/quantsim/services/market-data/internal/store"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	alpacaKey := os.Getenv("ALPACA_API_KEY")
	if alpacaKey == "" {
		log.Fatal("ALPACA_API_KEY is required")
	}
	alpacaSecret := os.Getenv("ALPACA_API_SECRET")
	if alpacaSecret == "" {
		log.Fatal("ALPACA_API_SECRET is required")
	}
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		log.Fatal("REDIS_URL is required")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}
	// Loopback by default: this service has no authentication of its own, so
	// only the gateway is meant to reach it. Set BIND_ADDR=0.0.0.0 when it
	// runs in a container behind a real network boundary.
	bindAddr := os.Getenv("BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("invalid REDIS_URL: %v", err)
	}
	redisClient := redis.NewClient(redisOpts)
	defer redisClient.Close()

	alpacaClient := alpaca.NewClient(alpacaKey, alpacaSecret)
	priceStore := store.NewPostgresHistoricalPriceStore(pool)
	priceCache := cache.NewRedisPriceCache(redisClient)
	svc := service.NewService(alpacaClient, priceStore, priceCache)
	marketDataHandler := handler.NewMarketDataHandler(svc)
	router := handler.NewRouter(marketDataHandler)

	poller := service.NewPoller(alpacaClient, priceCache, service.DefaultWatchlist, service.PollInterval)
	go poller.Run(context.Background())

	addr := bindAddr + ":" + port
	log.Printf("market-data service listening on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}
