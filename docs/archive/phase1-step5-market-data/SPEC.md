# SPEC — QuantSim Market Data Service (Phase 1, Step 5)

Status: **Approved 2026-07-29**
Scope: Alpaca REST client + historical ingestion for the Market Data Service. Not a whole-project spec — see `agents.md` for that context. Prior spec/plan/todo for the Auth Service (Step 4, complete) archived at `docs/archive/phase1-step4-auth/`.

---

## 1. Objective

Per `PHASE1_CHECKLIST.md` Step 5, build the historical half of the Market Data Service, so that:

- An operator can trigger ingestion of daily OHLC bars for one or more symbols from Alpaca into Postgres (`historical_prices`, already migrated in `003_historical_prices.up.sql`)
- Ingestion is idempotent — re-running it for the same symbol/timeframe/timestamp updates rather than duplicates
- Any client (frontend later, curl now) can query stored historical candles for a symbol
- Any client can fetch the curated watchlist QuantSim supports

This unblocks Step 6 (live polling + Redis, same service) and Step 8 (frontend price chart), both of which read from this service.

**Out of scope for this spec:** live/streaming prices, Redis, WebSocket fan-out (Step 6); gateway routing (Step 7); frontend (Step 8); trading engine, backtesting, AI insights.

---

## 2. Decisions

### 2.1 Alpaca data feed: `iex` (confirm your plan tier)

Alpaca's free market data plan only entitles you to the `iex` feed (real-time IEX exchange only), not `sip` (full consolidated tape, paid). This spec defaults every Alpaca request to `feed=iex`. **If you're on a paid Alpaca data plan, tell me and I'll default to `sip` instead** — this is a one-line change but changes what data quality to expect (IEX is a single exchange's view, thinner volume than SIP).

### 2.2 Ingestion date range: last 2 years by default, overridable per request

`POST /market-data/ingest` accepts optional `start`/`end` (ISO 8601 dates). If omitted: `end = yesterday` (today's daily bar may not exist yet mid-session), `start = end - 2 years`. Matches `agents.md`'s "multi-year historical price history" goal without hardcoding "all history," which Alpaca doesn't offer a clean "since IPO" query for anyway.

### 2.3 Symbol input: any symbol accepted, watchlist is just the default

`POST /market-data/ingest` accepts an arbitrary `symbols` list (not restricted to the curated watchlist) — useful later for backtesting on symbols outside the dashboard's default view. If `symbols` is omitted/empty, ingest the curated watchlist (`AAPL, MSFT, GOOGL, AMZN, TSLA, SPY, QQQ`) as a convenience default.

### 2.4 Idempotency: Postgres `ON CONFLICT` upsert

`historical_prices` already has `UNIQUE(symbol, timeframe, timestamp)` (migration 003). Ingestion upserts on that constraint — re-running ingestion for an overlapping range updates OHLCV in place, never errors, never duplicates.

### 2.5 Partial failure: per-symbol result, not all-or-nothing

`POST /market-data/ingest` with multiple symbols processes each independently; one symbol's Alpaca error (bad ticker, rate limit) doesn't abort the others. Response reports a per-symbol result so the caller can see exactly what succeeded.

### 2.6 History query defaults

`GET /market-data/history/:symbol` defaults `timeframe=1Day` (the only timeframe this spec ingests), `limit=500` bars (most recent first... actually chronological ascending, see §5), max `limit=2000`. No data for a valid-looking symbol → `200` with an empty `bars` array, not `404` — an unfetched symbol isn't a client error.

---

## 3. Commands

| Command | Purpose |
|---|---|
| `make docker-up` / `make docker-down` | Start/stop Postgres + Redis |
| `cd services/market-data && go run ./cmd/server` (or `make run-market-data` once wired) | Run the service locally |
| `cd services/market-data && go test ./...` | Run unit tests |
| `cd services/market-data && go mod tidy` | Sync deps after adding imports |

Manual verification (real Alpaca account + keys required, per checklist "run ingestion once to populate the database"):
```
curl localhost:8082/market-data/symbols
curl -X POST localhost:8082/market-data/ingest -d '{"symbols":["AAPL","MSFT"]}'
curl "localhost:8082/market-data/history/AAPL?limit=50"
```
(Port `8082` proposed — auth used `8081`, gateway will route both later. Confirm or override.)

---

## 4. Project structure

New, to be created by this spec:
```
services/market-data/
  cmd/server/main.go                     # env loading (fail-fast), pgx pool, wire client→store→service→handlers
  internal/alpaca/client.go              # AlpacaClient: GetBars(ctx, symbol, timeframe, start, end) ([]Bar, error), pagination
  internal/alpaca/client_test.go         # unit tests against httptest fixture server (no real Alpaca calls)
  internal/alpaca/types.go               # Alpaca API response shapes (bars, next_page_token)
  internal/service/types.go              # Bar, IngestRequest, IngestResult, HistoryResponse
  internal/service/interfaces.go         # AlpacaClient, HistoricalPriceStore
  internal/service/market_data.go        # Ingest, History, Symbols (business logic)
  internal/service/errors.go             # sentinel errors mapped to HTTP status in handlers
  internal/service/market_data_test.go   # unit tests, mocked AlpacaClient/HistoricalPriceStore
  internal/store/historical_price_store.go      # PostgresHistoricalPriceStore: UpsertBars, GetHistory
  internal/store/historical_price_store_test.go # mirrors auth's mock-store-is-enough-for-Phase-1 convention; real-Postgres check is the manual curl step
  internal/handler/router.go             # chi router; mounts /healthz, /market-data/*
  internal/handler/errors.go             # ErrorResponse{Code,Message} + WriteError (copy of auth's pattern — no shared pkg for this yet, see §7)
  internal/handler/market_data.go        # IngestHandler, HistoryHandler, SymbolsHandler
  internal/handler/market_data_test.go   # httptest-based handler tests
```

`go.work` at repo root gains `./services/market-data` (currently only `./pkg` and `./services/auth`).

---

## 5. Code style / conventions

- **Layering:** handler → service → store/client, one direction — same rule as the Auth Service. Service depends on `AlpacaClient`/`HistoricalPriceStore` interfaces, never on the concrete Postgres or HTTP types.
- **Errors:** service returns sentinels (`ErrInvalidSymbol`, `ErrUpstreamUnavailable` for Alpaca failures → maps to `502`); handlers map to `{"code": "...", "message": "..."}` JSON, per `agents.md`'s standing rule.
- **No Alpaca SDK** — plain `net/http`, per `PHASE1_CHECKLIST.md` Step 5. Base URL `https://data.alpaca.markets/v2`, headers `APCA-API-KEY-ID` / `APCA-API-SECRET-KEY` from `ALPACA_API_KEY`/`ALPACA_API_SECRET` env vars.
- **Alpaca pagination:** the bars endpoint returns `next_page_token`; loop until it's empty/absent. Confirm exact request/response field names against Alpaca's current docs before implementing the client (Task 2) — don't guess from memory, their API has changed shape across versions.
- **Bar ordering:** store and return bars in ascending timestamp order (oldest → newest) — matches how a chart would consume them (Step 8) and how Alpaca returns them natively, so no re-sort needed if the client behaves.
- **Router:** `go-chi/chi/v5`, routes mounted under `/market-data` via `r.Route("/market-data", ...)`, same convention as auth's `/auth`.
- **New dependencies beyond `chi`/`pgx` require sign-off first** — none expected; no HTTP client library, no Alpaca SDK.

---

## 6. Testing strategy

- **Alpaca client:** unit tests using `httptest.Server` to fake Alpaca's responses (fixture JSON) — verifies auth headers sent, pagination looped correctly, non-200 mapped to an error. No real network calls in `go test`.
- **Service layer:** table-driven unit tests, mocked `AlpacaClient`/`HistoricalPriceStore` (hand-written, no mocking library — matches `docs/TESTING_STRUCTURE.md`). Cover: successful ingest (single + multi-symbol), partial failure (one symbol errors, others succeed), empty history query, invalid `start`/`end`.
- **Handlers:** `httptest`-based tests hitting the chi router, asserting status + JSON body.
- **Not in scope:** real-Postgres integration tests, real-Alpaca integration tests, load testing — the manual curl checklist step is the real-dependency proof, per Phase 1's "manual/curl verification is sufficient" (`agents.md`).
- `go test ./...` passes before any checkpoint is marked done.

---

## 7. Open question: JSON error helper duplication

Auth's `internal/handler/errors.go` (`ErrorResponse`/`WriteError`) is service-local, not in `pkg/`. This spec repeats that same small file in market-data rather than extracting a shared `pkg/httputil` — it's ~15 lines, and premature sharing across 2 data points isn't worth a new `pkg/` package yet. **Flagging in case you'd rather extract it now** while there's a clean second caller to validate the abstraction against; happy to do either.

---

## 8. Boundaries

**Always do:**
- Follow the checkpoint order in `tasks/plan.md` — stop after each for review
- Keep handler → service → store/client layering; service depends on interfaces only
- Use the JSON error shape for every 4xx/5xx response
- Run `go test ./...` before flagging a checkpoint done
- Verify Alpaca's bars endpoint contract against current official docs before writing the client (don't implement from memory/training data alone)

**Ask first:**
- Any dependency beyond `go-chi/chi/v5`, `jackc/pgx/v5`
- Any DB schema/migration change (none expected — `historical_prices` already fits)
- Switching the default feed from `iex` to `sip` (§2.1) — needs to know your Alpaca plan tier
- Extracting the JSON error helper into `pkg/` (§7)

**Never do:**
- Commit `.env` or real Alpaca keys
- Log API keys in plaintext (headers included)
- Bypass the JSON error shape or interface-based dependency

---

## Confirm before I start

- [x] Alpaca feed tier (§2.1): free tier only — `iex` confirmed
- [x] Port `8082` (§3) — confirmed
- [x] Checkpoint plan in `tasks/plan.md` (companion doc) — confirmed
- [x] JSON error helper duplication (§7) — leave duplicated, confirmed
