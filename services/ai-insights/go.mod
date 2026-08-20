module github.com/kpeguero/quantsim/services/ai-insights

go 1.25.0

require (
	github.com/go-chi/chi/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/kpeguero/quantsim/pkg v0.0.0-00010101000000-000000000000
	github.com/redis/go-redis/v9 v9.21.0
	golang.org/x/sync v0.17.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
)

replace github.com/kpeguero/quantsim/pkg => ../../pkg
