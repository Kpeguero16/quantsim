# QuantSim — Project Structure for Testing

This document describes how to structure the repo so you can add **unit tests** (and later integration tests) without major refactors. It aligns with [AGENTS.md](../AGENTS.md) and [PHASE1_CHECKLIST.md](../PHASE1_CHECKLIST.md): Phase 1 uses manual/curl verification; automated tests are optional or Phase 2+. The goal here is to **restructure once** so that when you add tests, the code is already test-friendly.

---

## 1. Keep the Current Repo Layout

Your existing layout is already test-friendly:

```
/services
  /auth
  /gateway
  /market-data
  /trading-engine
  /backtesting
  /ai-insights
/pkg          ← shared code + shared test helpers
/frontend
/infra
  /docker
  /migrations
/docs
```

No need to move services or rename top-level folders. Tests will live **inside** each service and inside `pkg/`.

---

## 2. Per-Service Layout (So Logic Is Testable)

For each Go service, use a layout that separates **entrypoints** from **logic** so you can unit-test logic without running HTTP or DB.

Recommended pattern:

```
services/auth/
  go.mod
  cmd/
    server/
      main.go          ← wire dependencies, start HTTP server only
  internal/             ← or just top-level packages if you prefer
    handler/            ← HTTP handlers (thin: parse request, call service, write response)
    service/            ← business logic (register, login, refresh)
    store/              ← DB/Redis access (implement interfaces)
  *.go                  ← if you keep it flat: handlers, service, store in same module
```

- **`cmd/server/main.go`**: Build dependencies (DB pool, JWT config, stores), inject them into handlers, then `http.ListenAndServe`. No business logic here.
- **Handlers**: Parse input, call `authService.Register(ctx, ...)`, return JSON. Easy to test by calling the service layer with a fake store.
- **Service layer**: Contains “register user”, “validate password”, “issue tokens”. Accept **interfaces** (e.g. `UserStore`, `AccountStore`) so you can pass mocks in tests.
- **Store layer**: Implements those interfaces with real Postgres/Redis. You can unit-test the service with in-memory or mock implementations and add **integration** tests for the store later.

If you prefer a flatter layout (no `internal/`), keep the same idea: **handlers → service → store**, with the service depending on interfaces, not concrete DB types.

---

## 3. Use Interfaces for External Dependencies

Define small interfaces in the **service** (or handler) package and implement them in **store** (or a separate package). Then unit tests can inject fakes.

Example for auth:

```go
// services/auth/internal/service/types.go (or store/interfaces.go)
package service

type UserStore interface {
    CreateUser(ctx context.Context, email, username string, passwordHash []byte) (uuid.UUID, error)
    GetUserByEmail(ctx context.Context, email string) (*User, error)
}

type AccountStore interface {
    CreateAccount(ctx context.Context, userID uuid.UUID, balance decimal.Decimal) (uuid.UUID, error)
}
```

- Production: `main.go` builds `pgx` pools and passes `store.NewPostgresUserStore(pool)` as `UserStore`.
- Tests: pass a **mock** or simple in-memory implementation (e.g. map by email) so tests don’t need Postgres.

Same idea for **market-data** (Alpaca client interface), **trading-engine** (order store, price source), etc.

---

## 4. Where Tests Live

| Test type        | Location                          | How to run                    |
|------------------|-----------------------------------|-------------------------------|
| Unit             | Next to code: `service/auth_test.go`, `store/store_test.go` | `go test ./...` in that service |
| Integration      | Optional: `services/auth/integration/` or `services/auth/test/` | `go test ./integration/...` (often with `-tags=integration`) |
| Shared test util | `pkg/testutil/` or `pkg/testing/` | Used by other packages via import |

- **Unit**: Same package (e.g. `package service`) or external tests (`package service_test`) with only exported API. Use `*_test.go` in the same directory as the code.
- **Integration**: Separate directory or build tag so they don’t run on every `go build`/`go test` unless requested. Use real DB/Redis (e.g. Docker) or testcontainers.
- **pkg**: Put shared helpers (e.g. `testutil.NewTestDB(t)`, `testutil.AssertJSONCode(t, w, 400)`) in `pkg/testutil/` so auth, market-data, gateway can reuse them.

---

## 5. Shared Test Helpers in `pkg/`

Add a small package for things you’ll use across services:

```
pkg/
  auth/           ← JWT middleware (existing)
  testutil/       ← (new) shared test helpers
    db.go          ← e.g. connect to test DB or skip if not available
    http.go        ← e.g. assert JSON response code/body
    jwt.go         ← e.g. build a valid test token
```

- Keep `testutil` minimal: only what multiple services need. Avoid pulling in heavy deps in `pkg` if only one service needs them.
- Each service already has its own `go.mod`; they can `require github.com/kpeguero/quantsim/pkg` and use `testutil` in `*_test.go` only (so test deps don’t bloat the main binary).

---

## 6. Makefile Targets for Tests

Add to your root **Makefile**:

```makefile
# Run unit tests in all Go modules (services + pkg)
test:
	go test ./pkg/...
	cd services/auth    && go test ./...
	cd services/gateway && go test ./...
	cd services/market-data && go test ./...
	# add other services as you build them

# Optional: integration tests (require Docker / env)
test-integration:
	cd services/auth && go test -tags=integration ./integration/...
```

Use `test` for quick feedback; use `test-integration` only when you have integration tests and want to run them explicitly.

---

## 7. Summary Checklist (When You Implement Code)

- [ ] **Per service**: Separate `cmd/` (or main) from handlers, service layer, and store.
- [ ] **Service layer** depends on **interfaces** (UserStore, AccountStore, AlpacaClient, etc.), not concrete DB or HTTP clients.
- [ ] **Stores** implement those interfaces with real DB/Redis; keep handlers thin.
- [ ] **Unit tests** live in `*_test.go` next to the code; use mocks or in-memory implementations for stores/clients.
- [ ] **Optional**: `pkg/testutil/` for shared test helpers; `services/<name>/integration/` for integration tests with real dependencies.
- [ ] **Makefile**: `make test` runs unit tests across `pkg` and all services.

This keeps your current repo structure intact, matches AGENTS.md and Phase 1’s “manual verification first” approach, and sets you up so that adding unit tests in Phase 2+ is straightforward.
