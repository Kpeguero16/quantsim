# QuantSim — Project Structure for Testing

This document describes how to structure the repo so you can add **unit tests** (and later integration tests) without major refactors. It aligns with [agents.md](../agents.md) and [PHASE1_CHECKLIST.md](PHASE1_CHECKLIST.md): Phase 1 uses manual/curl verification; automated tests are optional or Phase 2+. The goal here is to **restructure once** so that when you add tests, the code is already test-friendly.

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
| Unit             | Next to code: `service/auth_test.go` | `make test` |
| Integration      | **`services/auth/integration/`** — implemented in Step 12 | `make test-integration` (see §6a) |
| Shared test util | `pkg/testutil/` or `pkg/testing/` — **not created; see below** | — |

**On `pkg/testutil/`:** deliberately not used for the integration harness. `pkg/go.mod` has zero requires today and is consumed by all three services; putting a Postgres harness there would drag `pgx` into every service's dependency graph to serve one package in one service. Extract only when a second service's store needs the same treatment.

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

**Implemented as of Step 12.** The root Makefile now has:

| Target | What it runs |
|---|---|
| `make test` | Unit tests across `pkg` and all three services. No Docker, no network |
| `make test-integration` | The store suite against a real Postgres. Skips if it is unreachable |
| `make test-all` | Both |
| `make test-db-drop` | Drops `quantsim_test` |
| `make vet` | `go vet` every module, **including a `-tags=integration` pass** |

Two details in there are load-bearing and should not be "tidied away":

- **`test-integration` runs with `-v`.** Without it `go test` prints only `ok` and suppresses the output of *skipped* tests — so a run where every test skipped looks identical to one where they all passed. This was verified the hard way: the first version printed a bare `ok` with Postgres stopped and hid the reason entirely.
- **`vet` includes the tagged pass.** Files behind a build tag are never type-checked by any default command, which is exactly how a tagged test file rots unnoticed.

---

## 6a. Integration tests: how they actually work

Implemented in Step 12 for `services/auth/internal/store`, which previously had no tests at all. See `SPEC.md` (Step 12) for the full reasoning.

**As of Step 16 the harness exists in three modules** — `services/auth/integration/`, `services/trading-engine/integration/`, and `services/backtesting/integration/` — as a near-verbatim copy. Everything in this section describes all three. Full up-to-date reasoning, including why the third copy landed in `backtesting` rather than the `market-data` this section originally predicted, is in `docs/deferred-tuning.md` §11 — that file is the one kept current session to session; treat the rest of this section as the original two-copy rationale, still correct in substance.

### Why it was copied rather than shared

The obvious move was to extract it to `pkg/testutil/` (§5) at the moment of the second use. It was copied instead, deliberately:

- **`pkg` has no test-only dependencies today, and this would have added them for every module.** Each service's `go.mod` requires `pkg`; a `testutil` that imports `pgx` puts that in the dependency graph of services that never touch Postgres. Keeping it out of `pkg` keeps that cost where it is actually paid.
- **Two copies is not yet evidence of the right abstraction.** The auth copy truncates `users CASCADE`; the trading-engine copy has to seed accounts and positions that no store method can create. A shared harness written against exactly two call sites tends to encode the first one's assumptions and then grow parameters for the second.
- **The one part that must not drift was verified rather than shared.** `assertTestDB`, its `protectedDatabases` denylist, and `resolveDSNs` are byte-identical in both copies, and both were mutation-checked — the guard is the whole reason this file says *"do not simplify that to a single comparison"*. A divergence there is the failure mode worth caring about, and `diff` is enough to catch it.

What legitimately *does* differ between the copies is the seeding (`insertUserRaw` vs `seedAccount`/`seedPosition`/`numeric`) and the post-migration schema assertions — auth checks migrations 004 and 005, trading-engine checks 006's columns and that `positions.avg_cost` is `NOT NULL`. Those are per-service by definition and would be parameters, not shared code, in any extraction.

### The trigger for extracting it

**A fourth service needing it.** `docs/deferred-tuning.md` §11 names `market-data`'s `historical_price_store.go` as that fourth use — it still has no store tests, and its idempotent upsert on `UNIQUE(symbol, timeframe, timestamp)` is exactly the kind of SQL that needs a real database. At four call sites the shape is demonstrated rather than guessed, and the duplication stops being a copy and starts being a policy nobody owns.

At that point extract to `pkg/testutil/` under §5's constraint (test files only, so the dependency never reaches a service binary), and port all four call sites in the same change — an extraction that leaves one copy behind is worse than four copies, because now there are five things and one of them lies.

Until then: **when you change the harness, change both copies, and diff them.**

### Running them

```bash
make docker-up          # Postgres must be running
make test-integration
```

With Docker stopped the suite **skips and exits 0**, so `make test` and a plain `go test ./...` stay green on a laptop with nothing running.

### The database, and the rule that matters most

The suite runs against a dedicated **`quantsim_test`** database that the harness **drops and recreates on every run**, then migrates from `001`.

**It must never touch the dev database.** That environment is genuinely confusing, so it is worth stating flatly:

| | |
|---|---|
| `POSTGRES_USER` | `quantsim` |
| `POSTGRES_DB` | `quantsim` — **empty**, a long-standing decoy |
| `DATABASE_URL` database | `postgres` — **this is where the real rows live** |

Both names a careless harness would reach for are wrong, and one of them is wrong destructively. So `assertTestDB` fails closed twice over — an absolute `protectedDatabases` denylist first, then an exact match on `quantsim_test` — and is called on every path that can write: when the DSN is derived, before the `DROP`, after the pool connects, and again immediately before every `TRUNCATE`. The last two ask the server `SELECT current_database()` rather than trusting a string parsed at startup.

**Do not simplify that to a single comparison against the constant.** That was the first version, and it defended only against a wrong DSN: editing the constant to `postgres` would have satisfied every check while the suite truncated real data. The denylist is what makes the constant itself subject to the guard instead of the yardstick for it.

`DATABASE_URL` is never used as-is; its path component is replaced with `/quantsim_test`.

### Skip vs. fail

Exactly one condition skips: **Postgres is unreachable**. Everything else — a guard violation, a failed migration, an unparseable DSN — exits non-zero.

That distinction is not decoration. An earlier version of the harness skipped on any setup error; migrations then turned out not to be idempotent, and the whole suite reported a green `ok` while running zero tests. A harness that cannot tell *"nothing to test against"* from *"the harness is broken"* protects nothing.

### Overriding the connection

`TEST_DATABASE_URL` points the suite at a different host or user. It may **not** change the database *name* — the guard applies to it too.

If neither `TEST_DATABASE_URL` nor `DATABASE_URL` is in the environment, the harness reads `DATABASE_URL` from the repo-root `.env` without exporting it. This exists because `make migrate-up` only works thanks to the Makefile's `-include .env` + `export`; running `go test -tags=integration ./integration/...` by hand from a plain shell would otherwise always skip.

### Conventions

- **No `t.Parallel()` in that package, ever.** Every test shares one database and truncates it. Parallelism there needs a database per test, not a flag.
- Isolation is `TRUNCATE TABLE users CASCADE` **before** each test — before, so a failing test's rows survive for inspection with `psql`.
- Transaction-per-test is deliberately not used: `CreateUserWithAccount` calls `pool.Begin` itself, so an outer transaction would reduce the rollback test — the most valuable one in the suite — to a savepoint release.
- Migrations are applied by exec'ing `infra/migrations/*.up.sql` in filename order, **not** via golang-migrate as a library, which keeps `services/auth/go.mod` unchanged. Valid only while no migration needs a migrate directive.

### Writing a new test there

Start with `newStore(t)`. It skips when the database is unavailable, truncates, and hands back the real store:

```go
func TestSomething(t *testing.T) {
    s, pool, ctx := newStore(t)
    // s    -- *store.PostgresUserStore against a clean schema
    // pool -- for raw SQL: seeding rows the store cannot create, and counting
}
```

Use `insertUserRaw` when a row needs a shape the store cannot produce — a mixed-case stored email, for instance, since `service.Register` lowercases before the store ever sees it.

The trading-engine copy has the same `newStore(t)` entry point and three helpers of its own: `seedAccount` and `seedPosition` write rows the store cannot create (a sell must be testable without first trusting the buy path), and **`numeric` reads a money column as `::text` and parses it** rather than scanning into a `float64`. That last one is not a style preference — the columns are `NUMERIC(20,4)` and Postgres is the authority on what was stored. Scanning straight into a `float64` lets a value that lost precision on the way in come back looking exactly like the number the test expected, so the assertion ends up checking Go's arithmetic against itself.

**Pair every new test with a mutation check.** Break the query it covers and confirm *that* test fails. A store test that passes against a broken query is worse than no test, because it is trusted.

---

## 7. Summary Checklist (When You Implement Code)

- [ ] **Per service**: Separate `cmd/` (or main) from handlers, service layer, and store.
- [ ] **Service layer** depends on **interfaces** (UserStore, AccountStore, AlpacaClient, etc.), not concrete DB or HTTP clients.
- [ ] **Stores** implement those interfaces with real DB/Redis; keep handlers thin.
- [ ] **Unit tests** live in `*_test.go` next to the code; use mocks or in-memory implementations for stores/clients.
- [ ] **Optional**: `pkg/testutil/` for shared test helpers; `services/<name>/integration/` for integration tests with real dependencies.
- [ ] **Makefile**: `make test` runs unit tests across `pkg` and all services.

This keeps your current repo structure intact, matches AGENTS.md and Phase 1’s “manual verification first” approach, and sets you up so that adding unit tests in Phase 2+ is straightforward.
