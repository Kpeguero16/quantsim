package main

import (
	"log"
	"net/http"
	"os"

	"github.com/kpeguero/quantsim/services/market-data/internal/handler"
)

func main() {
	if os.Getenv("DATABASE_URL") == "" {
		log.Fatal("DATABASE_URL is required")
	}
	if os.Getenv("ALPACA_API_KEY") == "" {
		log.Fatal("ALPACA_API_KEY is required")
	}
	if os.Getenv("ALPACA_API_SECRET") == "" {
		log.Fatal("ALPACA_API_SECRET is required")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	router := handler.NewRouter()

	log.Printf("market-data service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatal(err)
	}
}
