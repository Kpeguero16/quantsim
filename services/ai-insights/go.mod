module github.com/kpeguero/quantsim/services/ai-insights

go 1.25.0

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/anthropics/anthropic-sdk-go v1.66.0
	github.com/go-chi/chi/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/kpeguero/quantsim/pkg v0.0.0-00010101000000-000000000000
	github.com/redis/go-redis/v9 v9.21.0
	golang.org/x/sync v0.17.0
)

require (
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
)

replace github.com/kpeguero/quantsim/pkg => ../../pkg
