package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/backtesting/internal/client"
	"github.com/kpeguero/quantsim/services/backtesting/internal/handler"
	"github.com/kpeguero/quantsim/services/backtesting/internal/service"
	"github.com/kpeguero/quantsim/services/backtesting/internal/store"
)

// dbPingTimeout bounds the one connection attempt made at boot -- its only
// job is to tell "the database is there" from "it is not" before the
// service starts accepting requests.
const dbPingTimeout = 5 * time.Second

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	// Every /backtests/* route revalidates the caller's token rather than
	// trusting the gateway's X-User-ID header (SPEC.md §2.7), matching
	// trading-engine's precedent -- a missing or weak signing secret fails at
	// boot here for the same reason it does there.
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET is required")
	}
	if err := pkgauth.ValidateSecret([]byte(jwtSecret)); err != nil {
		log.Fatal(err)
	}

	// Reused as-is, the same value the gateway and trading-engine already use
	// to reach the same service. Called directly rather than through the
	// gateway (SPEC.md §2.2).
	marketDataURL := os.Getenv("MARKET_DATA_SERVICE_URL")
	if marketDataURL == "" {
		marketDataURL = "http://localhost:8082"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8084"
	}
	// Loopback by default, like every other engine service: the gateway is
	// the only thing meant to reach this. Set BIND_ADDR=0.0.0.0 only behind a
	// real network boundary.
	bindAddr := os.Getenv("BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	pingCtx, cancel := context.WithTimeout(context.Background(), dbPingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		log.Fatalf("database unreachable: %v", err)
	}

	backtestStore := store.NewPostgresBacktestStore(pool)
	historyClient := client.NewMarketDataClient(marketDataURL)
	svc := service.NewService(backtestStore, historyClient)
	backtestHandler := handler.NewBacktestHandler(svc)
	router := handler.NewRouter(backtestHandler, []byte(jwtSecret))

	addr := bindAddr + ":" + port
	log.Printf("backtesting service listening on %s (market-data=%s)", addr, marketDataURL)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}
