module github.com/kpeguero/quantsim/services/gateway

go 1.25.0

replace github.com/kpeguero/quantsim/pkg => ../../pkg

require (
	github.com/go-chi/chi/v5 v5.3.1
	github.com/kpeguero/quantsim/pkg v0.0.0-00010101000000-000000000000
)

require github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
