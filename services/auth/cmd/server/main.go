package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	pkgauth "github.com/kpeguero/quantsim/pkg/auth"
	"github.com/kpeguero/quantsim/services/auth/internal/handler"
	"github.com/kpeguero/quantsim/services/auth/internal/service"
	"github.com/kpeguero/quantsim/services/auth/internal/store"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET is required")
	}
	if err := pkgauth.ValidateSecret([]byte(jwtSecret)); err != nil {
		log.Fatal(err)
	}
	// Not a new variable -- services/market-data already requires this same
	// REDIS_URL. Required here too, at boot, rather than optional: no
	// service in this repo degrades gracefully without a configured
	// dependency, and inventing that pattern for one small feature isn't
	// worth it. This is separate from the fail-open behavior at request time
	// for a Redis that's configured but transiently unreachable (SPEC.md
	// Step 13, 2.3, 2.5).
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		log.Fatal("REDIS_URL is required")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
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

	userStore := store.NewPostgresUserStore(pool)
	revocationStore := store.NewRedisTokenStore(redisClient)
	svc := service.NewService(userStore, revocationStore, []byte(jwtSecret))
	authHandler := handler.NewAuthHandler(svc)
	router := handler.NewRouter(authHandler, []byte(jwtSecret))

	addr := bindAddr + ":" + port
	log.Printf("auth service listening on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}
