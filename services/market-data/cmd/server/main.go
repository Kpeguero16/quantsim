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

	log.Printf("market-data service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatal(err)
	}
}
