package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kpeguero/quantsim/services/market-data/internal/alpaca"
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

	alpacaClient := alpaca.NewClient(alpacaKey, alpacaSecret)
	priceStore := store.NewPostgresHistoricalPriceStore(pool)
	svc := service.NewService(alpacaClient, priceStore)
	marketDataHandler := handler.NewMarketDataHandler(svc)
	router := handler.NewRouter(marketDataHandler)

	log.Printf("market-data service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatal(err)
	}
}
