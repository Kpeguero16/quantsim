# Plan — QuantSim Market Data Service (Phase 1, Step 5)

## Context

`SPEC.md` (this checkpoint's spec, awaiting review) defines the historical-ingestion half of the Market Data Service: an Alpaca REST client, a Postgres store for `historical_prices`, and three endpoints (`ingest`, `history/:symbol`, `symbols`). Per the working agreement in `agents.md`, checkpoints are vertical slices sized to "one logical piece," reviewed before the next starts.

Lesson carried over from the Auth Service plan (`docs/archive/phase1-step4-auth/plan.md`): SPEC.md there originally proposed horizontal checkpoints (all JWT helpers, then all service methods, then all handlers) and had to be corrected mid-flight to vertical slices. This plan skips that detour and is vertical from the start.

## Module wiring (do first, before Task 1)

Add `./services/market-data` to the root `go.work` (`go work use ./services/market-data`) so it resolves `github.com/kpeguero/quantsim/pkg` locally, matching how `services/auth` is already wired.

## Decided defaults (flagged here for override at checkpoint review, not asked as separate blocking questions)

- **Port:** `PORT` env var, default `8082` if unset (auth uses `8081`).
- **Required env at boot:** `DATABASE_URL`, `ALPACA_API_KEY`, `ALPACA_API_SECRET` — `main.go` fails fast with a clear log line if any is empty.
- **HTTP client timeout:** 15s per Alpaca request (bars pages can be large; Alpaca itself is usually fast, but no request should hang the ingest handler indefinitely).
- **Pagination page size:** let Alpaca use its own default page size; just follow `next_page_token` until absent. No need to tune `limit` on the Alpaca request itself for Phase 1 symbol counts (7 watchlist symbols × 2 years of daily bars is a few hundred rows/symbol — one or two pages at most).
- **Alpaca error → HTTP mapping:** any non-2xx from Alpaca, or a transport error (timeout, DNS, connection refused), maps to service-level `ErrUpstreamUnavailable` → handler returns `502` with the JSON error shape. This keeps "Alpaca is down/misconfigured" clearly distinct from "your request was bad" (`400`).
- **Invalid symbol format:** reject empty strings and anything failing a simple `^[A-Z.]{1,10}$`-ish check at the service layer before calling Alpaca at all — cheap validation, avoids burning an API call on `"symbols": [""]`.

## Fix needed before Task 1's verification works

None identified yet — unlike auth's `Makefile` `run-auth` bug, `run-market-data`'s target already assumes `cmd/server`, matching this plan's layout (per `PHASE1_CHECKLIST.md` Step 1's note: "update the Makefile recipe to match the real entrypoint"). Will confirm the exact `Makefile` target text at Task 1 and fix in the same commit if it's still pointing at the wrong path.

## Dependency graph

```
Task 1 (Symbols) — root, stands up all shared skeleton:
  main.go (pgx pool, fail-fast env, chi router), handler/errors.go (JSON error helper),
  internal/service + internal/handler scaffolding. No Alpaca, no DB read/write beyond pool ping.
        │
        ├──► Task 2 (Alpaca client) — standalone, no DB. Testable in isolation via httptest fixtures.
        │           │
        │           └──► Task 3 (Ingest) — needs Task 2's client AND a new store method
        │                 (UpsertBars). Wires client → store → service → handler end-to-end.
        │
        └──► Task 4 (History) — only needs a new store method (GetHistory) + Task 1's skeleton.
                                  No hard dependency on Task 2 or 3, but ordered last because
                                  querying data is only demoable once Task 3 has put data in.
```

Tasks 2 and 4 have no compile-time dependency on each other and could be built in either order; Task 3 hard-depends on Task 2. Narrative order (2→3→4) mirrors "build the client, ingest with it, then query what you ingested."

## Reuse — existing code this plan builds on

- `infra/migrations/003_historical_prices.up.sql` — `historical_prices` table, `UNIQUE(symbol, timeframe, timestamp)` constraint already in place for the upsert
- `.env.example` — `ALPACA_API_KEY`, `ALPACA_API_SECRET` already present
- `docs/TESTING_STRUCTURE.md` conventions — hand-written mocks, `*_test.go` co-located
- Auth service's `internal/handler/errors.go` pattern (`ErrorResponse{Code,Message}`, `WriteError`) — copied, not imported (see SPEC.md §7 open question)

## Tasks

### Task 1 — Skeleton + `GET /market-data/symbols`

**New/modified files:**
- `services/market-data/cmd/server/main.go` — env loading (fail-fast on `DATABASE_URL`/`ALPACA_API_KEY`/`ALPACA_API_SECRET`), pgx pool, chi router, `http.ListenAndServe`
- `services/market-data/internal/handler/router.go` — mounts `/healthz`, `/market-data/symbols`
- `services/market-data/internal/handler/errors.go` — `ErrorResponse{Code,Message}` + `WriteError`
- `services/market-data/internal/handler/market_data.go` — `SymbolsHandler` (returns hardcoded watchlist)
- `services/market-data/internal/handler/market_data_test.go` — 200 + body shape
- `go.work` (add market-data), `Makefile` (`run-market-data` fix if needed)

**Acceptance criteria:**
- `go build ./...` and `go vet ./...` clean in `services/market-data`
- Server refuses to start without `DATABASE_URL`/`ALPACA_API_KEY`/`ALPACA_API_SECRET`
- `GET /healthz` → 200
- `GET /market-data/symbols` → 200, `{"symbols":["AAPL","MSFT","GOOGL","AMZN","TSLA","SPY","QQQ"]}`
- `go test ./...` passes

**Verification:**
```
cd services/market-data && go test ./...
make docker-up
JWT_SECRET=unused DATABASE_URL=<...> ALPACA_API_KEY=x ALPACA_API_SECRET=y PORT=8082 make run-market-data
curl -i localhost:8082/healthz                  # 200
curl -i localhost:8082/market-data/symbols      # 200 + watchlist
```

---

### Task 2 — Alpaca client (`internal/alpaca`)

**New/modified files:**
- `services/market-data/internal/alpaca/types.go` — Alpaca bars response shape (bar fields, `next_page_token`)
- `services/market-data/internal/alpaca/client.go` — `Client.GetBars(ctx, symbol, timeframe string, start, end time.Time) ([]Bar, error)`; sets `APCA-API-KEY-ID`/`APCA-API-SECRET-KEY` headers, `feed=iex` query param (per SPEC.md §2.1), follows `next_page_token` pagination
- `services/market-data/internal/alpaca/client_test.go` — success (single + multi-page), Alpaca 401/500, malformed JSON, network error

**Acceptance criteria:**
- Confirmed against current Alpaca docs before writing (per SPEC.md §8 boundary) — request/response field names verified, not assumed from memory
- Auth headers present on every request
- Multi-page responses fully drained (test asserts all pages' bars returned, not just page 1)
- Non-2xx Alpaca response → typed error, no panic, no partial-garbage return
- `go test ./...` passes with zero real network calls (`httptest.Server` fixtures only)

**Verification:**
```
cd services/market-data && go test ./... -run Alpaca -v
```
(No live-Alpaca check yet — that happens in Task 3's manual step, where a real ingest exercises this client end-to-end.)

---

### Task 3 — `POST /market-data/ingest` end-to-end

**New/modified files:**
- `services/market-data/internal/store/historical_price_store.go` — `PostgresHistoricalPriceStore.UpsertBars(ctx, []Bar) error` (batched `ON CONFLICT (symbol, timeframe, timestamp) DO UPDATE`)
- `services/market-data/internal/store/historical_price_store_test.go` — mock-store unit test per `docs/TESTING_STRUCTURE.md` (real-Postgres exercised only via the manual curl step)
- `services/market-data/internal/service/types.go` — `Bar`, `IngestRequest{Symbols []string; Start, End *time.Time}`, `IngestResult{Symbol string; BarsIngested int; Error string}`
- `services/market-data/internal/service/interfaces.go` — `AlpacaClient`, `HistoricalPriceStore`
- `services/market-data/internal/service/errors.go` — `ErrInvalidSymbol`, `ErrUpstreamUnavailable`
- `services/market-data/internal/service/market_data.go` — `Service.Ingest(ctx, IngestRequest) ([]IngestResult, error)`: default symbols to watchlist if empty, default date range per SPEC.md §2.2, per-symbol try/catch so one failure doesn't abort the batch
- `services/market-data/internal/service/market_data_test.go` — success (single + multi-symbol), partial failure, invalid symbol format, Alpaca upstream error
- `services/market-data/internal/handler/market_data.go` (+`IngestHandler`), `router.go` (+ mount)
- `services/market-data/internal/handler/market_data_test.go` (+ cases)

**Acceptance criteria:**
- `POST` with explicit symbols → 200, per-symbol `IngestResult[]`, upserted rows visible in Postgres
- `POST` with no body / empty `symbols` → defaults to watchlist
- Re-running the same ingest → same row count (idempotent, no duplicates, `updated` not `inserted twice`)
- One bad symbol among several → that symbol's result carries an error message, others still succeed (200 overall, not 4xx/5xx for a partial failure)
- Alpaca fully unreachable (bad keys, network down) → 502, JSON error shape
- Malformed JSON body → 400
- `go test ./...` passes (mocked client + store, no live Alpaca/DB required)

**Verification:**
```
cd services/market-data && go test ./...
make docker-up && make migrate-up
JWT_SECRET=unused DATABASE_URL=<...> ALPACA_API_KEY=<real> ALPACA_API_SECRET=<real> PORT=8082 make run-market-data

curl -i -X POST localhost:8082/market-data/ingest -H 'Content-Type: application/json' \
  -d '{"symbols":["AAPL","MSFT"]}'                                   # 200, per-symbol results
curl -i -X POST localhost:8082/market-data/ingest -d '{}'            # 200, watchlist defaulted
psql "$DATABASE_URL" -c "SELECT symbol, count(*) FROM historical_prices GROUP BY symbol;"
curl -i -X POST localhost:8082/market-data/ingest -d 'not-json'      # 400
```

---

### Task 4 — `GET /market-data/history/:symbol`

**New/modified files:**
- `services/market-data/internal/store/historical_price_store.go` (+`GetHistory(ctx, symbol, timeframe string, limit int) ([]Bar, error)`, ascending by timestamp)
- `services/market-data/internal/store/historical_price_store_test.go` (+ cases)
- `services/market-data/internal/service/market_data.go` (+`History(ctx, symbol string, limit int) ([]Bar, error)`, default/max limit per SPEC.md §2.6)
- `services/market-data/internal/service/market_data_test.go` (+ cases: found, empty/unfetched symbol, limit clamping)
- `services/market-data/internal/handler/market_data.go` (+`HistoryHandler`), `router.go` (+ mount `GET /market-data/history/{symbol}`)
- `services/market-data/internal/handler/market_data_test.go` (+ cases)

**Acceptance criteria:**
- Symbol with ingested data → 200, bars ascending by timestamp
- Symbol never ingested → 200, `{"symbol":"...","timeframe":"1Day","bars":[]}` (not 404)
- `?limit=` respected, clamped to max (2000) if caller asks for more
- `go test ./...` passes

**Verification:**
```
cd services/market-data && go test ./...
curl -i "localhost:8082/market-data/history/AAPL?limit=50"     # 200 + bars (after Task 3's ingest)
curl -i "localhost:8082/market-data/history/ZZZZ"              # 200, empty bars
```

---

## What changed vs. a naive reading of `PHASE1_CHECKLIST.md`

- Checklist lists ingest before symbols in prose, but Task 1 starts with `symbols` because it's the zero-dependency endpoint that stands up the skeleton — same reasoning auth's plan used for starting with Register (the endpoint that stands up the most shared scaffolding first, not necessarily the first one prose-listed).
- Alpaca client (Task 2) gets its own checkpoint separate from ingest (Task 3), unlike the checklist's flat bullet list — a third-party API integration with pagination and auth headers is exactly the kind of thing that deserves isolated review and isolated tests before it's wired into a handler.

## Status

Approved 2026-07-29. See `tasks/todo.md` for live checkpoint status.
