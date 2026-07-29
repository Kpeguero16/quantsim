# Plan — QuantSim Market Data Service (Phase 1, Step 6)

## Context

`SPEC.md` (this checkpoint's spec, awaiting review) defines live price polling: an Alpaca snapshot client, a Redis-backed price cache, a background poller tying them together, and `GET /market-data/prices/:symbol`. Per the working agreement in `agents.md`, checkpoints are vertical slices sized to "one logical piece," reviewed before the next starts. This plan assumes SPEC.md's decisions (§2.1–2.9) as currently drafted — if review changes any of them, the affected task's file list/acceptance criteria below need matching edits before that task starts.

Lesson carried over from Step 5's plan (`docs/archive/phase1-step5-market-data/plan.md`): order checkpoints so the lowest-dependency, most-reviewable piece goes first, and give a third-party API integration (there: Alpaca bars; here: Alpaca snapshots) its own isolated checkpoint rather than folding it into the feature that consumes it.

## Decided defaults (from SPEC.md §2, restated here for quick reference while implementing)

- **Redis client:** `github.com/redis/go-redis/v9` (not the checklist's stale `go-redis/redis/v9` path) — SPEC.md §2.1.
- **Poll interval:** `PollInterval = 15 * time.Second`, constant, not env-configurable — SPEC.md §2.3.
- **Batching:** one `GET /v2/stocks/snapshots?symbols=A,B,C,...` call per tick for the whole watchlist, not one call per symbol — SPEC.md §2.2.
- **Price source:** `latestTrade.p` only; a symbol with no `latestTrade` in a tick's response is skipped that tick (old cached value survives until TTL or the next successful tick) — SPEC.md §2.4.
- **Cache TTL:** `PriceTTL = 45 * time.Second` (3× poll interval) on every `SET price:{SYMBOL}` — SPEC.md §2.5.
- **Pub/sub:** publish to `prices:{SYMBOL}` on every successful per-symbol cache write, no change-dedup — SPEC.md §2.6.
- **Scope:** poller always uses the existing `DefaultWatchlist` (7 symbols, already in `internal/service/market_data.go`) — SPEC.md §2.7.
- **Cache-miss semantics:** `GET /market-data/prices/:symbol` → `404` + JSON error shape (`code: "price_not_cached"`) on any miss (never polled, TTL expired, unknown symbol) — SPEC.md §2.8.
- **Poller lifecycle:** `go poller.Run(context.Background())` in `main.go`, no cancellation/graceful shutdown — SPEC.md §2.9.
- **Required env at boot (new):** `REDIS_URL`, joining the existing `DATABASE_URL`/`ALPACA_API_KEY`/`ALPACA_API_SECRET` fail-fast checks in `main.go`.

## Fix needed before Task 1's verification works

None identified — `REDIS_URL` is already in `.env.example` (Step 1) and Redis is already in `docker-compose.yml` (Step 2). `make run-market-data` already targets `cmd/server` correctly (fixed during Step 5).

## Dependency graph

```
Task 1 (Read path: Redis cache + GET /prices/:symbol) — no Alpaca dependency.
  Seeded manually via redis-cli for verification; stands up PriceCache
  interface, RedisPriceCache impl, Price type, LatestPrice service method,
  handler, and REDIS_URL wiring in main.go.
        │
        │   Task 2 (Alpaca snapshot client) — standalone, no Redis dependency.
        │   Testable in isolation via httptest fixtures, like Step 5's bars client.
        │
        └──────────────┬─────────────────────────────────────────────────┘
                        ▼
        Task 3 (Poller) — needs Task 1's PriceCache AND Task 2's SnapshotClient.
        Wires client → poller → cache end-to-end; GET /prices/:symbol now
        serves live data instead of manually-seeded data.
```

Tasks 1 and 2 have no compile-time dependency on each other and could be built in either order; Task 3 hard-depends on both. Narrative order (1→2→3) mirrors "stand up the read path against fake data, build the write source, then connect them" — same shape as Step 5's "skeleton → client → wire together" progression, adapted since this feature's "skeleton" (the read path) has real acceptance criteria of its own rather than being a throwaway stub.

## Reuse — existing code this plan builds on

- `internal/service/market_data.go` — `DefaultWatchlist`, `Timeframe` consts; `Service` struct gains a `cache` field and `LatestPrice` method rather than becoming a new type
- `internal/alpaca/client.go` — `Client`, `StatusError`, header/timeout conventions from `GetBars`; `GetSnapshots` follows the same shape
- `internal/handler/errors.go` — `ErrorResponse`/`WriteError`/`WriteJSON`, unchanged
- `internal/service/mock/mock.go` — existing hand-written mock pattern, extended with `PriceCache` and `SnapshotClient` mocks
- `.env.example` — `REDIS_URL` already present
- `docs/TESTING_STRUCTURE.md` conventions — hand-written mocks, `*_test.go` co-located, store/cache-layer wrappers verified manually rather than unit-tested (Step 5 set this precedent for `historical_price_store.go`; `redis_price_cache.go` follows it)

## Tasks

### Task 1 — Redis price cache + `GET /market-data/prices/:symbol` (read path)

**New/modified files:**
- `services/market-data/internal/cache/redis_price_cache.go` — `RedisPriceCache{client *redis.Client}`; `NewRedisPriceCache(client *redis.Client) *RedisPriceCache`; `SetPrice(ctx, symbol string, p service.Price, ttl time.Duration) error` (`SET price:{SYMBOL} <json> EX ttl`); `GetPrice(ctx, symbol string) (service.Price, error)` (`GET price:{SYMBOL}`, unmarshal; `redis.Nil` → `cache.ErrNotFound`); `PublishPrice(ctx, symbol string, p service.Price) error` (`PUBLISH prices:{SYMBOL} <json>`)
- `services/market-data/internal/cache/errors.go` — `ErrNotFound` (cache-layer sentinel; service maps it to `ErrPriceNotCached`, same pattern as store errors mapping to service errors)
- `services/market-data/internal/service/types.go` (+`Price{Symbol string; Price float64; Timestamp time.Time}`, JSON tags `symbol`/`price`/`timestamp`)
- `services/market-data/internal/service/interfaces.go` (+`PriceCache{SetPrice, GetPrice, PublishPrice}`)
- `services/market-data/internal/service/errors.go` (+`ErrPriceNotCached`)
- `services/market-data/internal/service/market_data.go` (`Service` +`cache PriceCache` field; `NewService` signature +`cache PriceCache` param; +`LatestPrice(ctx, symbol string) (Price, error)` — uppercase/trim symbol per `History`'s existing pattern, delegate to `cache.GetPrice`, map `cache.ErrNotFound` → `ErrPriceNotCached`)
- `services/market-data/internal/service/market_data_test.go` (+`LatestPrice` cases: hit, miss)
- `services/market-data/internal/service/mock/mock.go` (+mock `PriceCache`)
- `services/market-data/internal/handler/market_data.go` (+`PricesHandler`: `chi.URLParam` symbol → `svc.LatestPrice` → `200`/`404`)
- `services/market-data/internal/handler/market_data_test.go` (+cases: `200` on hit, `404` + JSON error shape on miss)
- `services/market-data/internal/handler/router.go` (+mount `GET /market-data/prices/{symbol}`)
- `services/market-data/cmd/server/main.go` (+`REDIS_URL` fail-fast, `redis.NewClient(redis.ParseURL(...))`, pass into updated `NewService` call)
- `services/market-data/go.mod`/`go.sum` (+`github.com/redis/go-redis/v9`, via `go get` + `go mod tidy`)

**Acceptance criteria:**
- `go build ./...` and `go vet ./...` clean in `services/market-data`
- Server refuses to start without `REDIS_URL` (same fail-fast style as the existing three env checks)
- Manually seeded key (`redis-cli SET price:AAPL ...`) → `GET /market-data/prices/AAPL` → `200` + that JSON
- No cached key → `GET /market-data/prices/AAPL` → `404` + `{"code":"price_not_cached",...}`
- `go test ./...` passes (mocked `PriceCache`, no real Redis needed for unit tests)

**Verification:**
```
cd services/market-data && go test ./...
make docker-up
redis-cli -u "$REDIS_URL" SET price:AAPL '{"symbol":"AAPL","price":123.45,"timestamp":"2026-07-29T14:00:00Z"}' EX 45
JWT_SECRET=unused DATABASE_URL=<...> REDIS_URL=<...> ALPACA_API_KEY=x ALPACA_API_SECRET=y PORT=8082 make run-market-data

curl -i localhost:8082/market-data/prices/AAPL     # 200 + seeded JSON
curl -i localhost:8082/market-data/prices/ZZZZ     # 404, JSON error shape
```

---

### Task 2 — Alpaca snapshot client (`internal/alpaca`)

**New/modified files:**
- `services/market-data/internal/alpaca/types.go` (+`Snapshot{LatestTrade *Trade}`, `Trade{Price float64 \`json:"p"\`; Timestamp time.Time \`json:"t"\`}` — only the fields this spec actually reads, per SPEC.md §2.4's latest-trade-only decision; not the full `latestQuote`/`minuteBar`/`dailyBar` schema)
- `services/market-data/internal/alpaca/client.go` (+`GetSnapshots(ctx, symbols []string) (map[string]Snapshot, error)` — `GET /v2/stocks/snapshots?symbols=A,B,C&feed=iex`, same auth headers/timeout/`StatusError` pattern as `GetBars`)
- `services/market-data/internal/alpaca/client_test.go` (+cases: multi-symbol success, one symbol missing `latestTrade` in the response, non-2xx, malformed JSON, network error)

**Acceptance criteria:**
- Re-confirmed against current Alpaca docs at implementation time (per SPEC.md §7 boundary; already checked once while drafting SPEC.md §2, but the boundary calls for a check at implementation time too)
- Auth headers present; `symbols` sent as one comma-joined query param — one HTTP request for the whole watchlist, not N
- Response map keyed by symbol, matching Alpaca's per-symbol JSON keys
- A symbol with no `latestTrade` in Alpaca's response → `Snapshot.LatestTrade` is `nil`, not a zero-value struct that could be mistaken for a real $0.00 trade
- Non-2xx Alpaca response → `StatusError`, no panic, no partial-garbage return
- `go test ./...` passes with zero real network calls (`httptest.Server` fixtures only)

**Verification:**
```
cd services/market-data && go test ./... -run Snapshot -v
```
(No live-Alpaca check yet — Task 3's manual step exercises this end-to-end.)

---

### Task 3 — Poller: background tick loop, end-to-end

**New/modified files:**
- `services/market-data/internal/service/interfaces.go` (+`SnapshotClient{GetSnapshots(ctx, symbols []string) (map[string]alpaca.Snapshot, error)}` — separate interface from `AlpacaClient`, both implemented by the same `*alpaca.Client`)
- `services/market-data/internal/service/live.go` — `Poller{snapshots SnapshotClient; cache PriceCache; symbols []string; interval time.Duration}`; `NewPoller(snapshots SnapshotClient, cache PriceCache, symbols []string, interval time.Duration) *Poller`; `Run(ctx context.Context)` — `time.NewTicker(interval)` loop, calls `pollOnce` each tick, logs and continues on error (never crashes the process), returns on `ctx.Done()`; `pollOnce(ctx context.Context) error` — one `GetSnapshots` call for `symbols`; per symbol, skip if `LatestTrade == nil`, else `cache.SetPrice` (with `PriceTTL`) + `cache.PublishPrice`
- `services/market-data/internal/service/live_test.go` — mocked `SnapshotClient`/`PriceCache`: successful tick caches + publishes every symbol; one symbol missing `LatestTrade` is skipped, others still succeed; `SnapshotClient` error aborts the tick with zero cache writes (SPEC.md §2.2 — atomic per-tick failure, no per-symbol try/catch here)
- `services/market-data/internal/service/mock/mock.go` (+mock `SnapshotClient`)
- `services/market-data/cmd/server/main.go` (+construct `Poller` from the existing `alpacaClient` — already built in Task 1's `main.go` edits for `NewService` — and Task 1's Redis-backed cache, using `service.DefaultWatchlist` and `service.PollInterval`; `go poller.Run(context.Background())` before `http.ListenAndServe`)

**Acceptance criteria:**
- `go test ./...` passes (mocked client + cache, no real Alpaca/Redis required)
- With real Alpaca + Redis running: `price:{symbol}` keys appear and update in Redis within ~15–20s of service start, for all 7 watchlist symbols
- `redis-cli SUBSCRIBE prices:AAPL` shows a new message roughly every 15s
- `GET /market-data/prices/AAPL` now returns `200` with live (not manually seeded) data once the poller has ticked at least once
- A snapshot missing `latestTrade` for one symbol doesn't crash the tick or clobber other symbols' cached values
- A full-tick Alpaca failure (e.g. temporarily bad keys) doesn't crash the service; the next tick retries automatically
- `go vet ./...` clean

**Verification:**
```
cd services/market-data && go test ./...
make docker-up
JWT_SECRET=unused DATABASE_URL=<...> REDIS_URL=<...> ALPACA_API_KEY=<real> ALPACA_API_SECRET=<real> PORT=8082 make run-market-data

# separate terminal
redis-cli -u "$REDIS_URL" SUBSCRIBE prices:AAPL
```
```
# another terminal, a few times ~15s apart
redis-cli -u "$REDIS_URL" GET price:AAPL
curl -i localhost:8082/market-data/prices/AAPL
```

---

## What changed vs. a naive reading of `PHASE1_CHECKLIST.md`

- The checklist lists the poller before the endpoint in prose, but Task 1 builds the read path (`PriceCache` + `GET /prices/:symbol`) first, verified against a manually-seeded Redis key — same reasoning Step 5 used for starting with `symbols` before `ingest`: stand up and review the lowest-dependency, most reviewable piece first, not necessarily the first one prose-listed.
- The Alpaca snapshot client (Task 2) gets its own checkpoint, separate from the poller that consumes it (Task 3) — mirrors Step 5 giving the bars client its own checkpoint before `Ingest` wired it in. A third-party API integration deserves isolated tests and isolated review before something else depends on it.
- Unlike `Ingest` (Step 5), the poller has no per-symbol try/catch — SPEC.md §2.2 explains why: one batched request means one failure is atomic across the whole tick, so there's nothing per-symbol to isolate at the request level (only at the response-parsing level, for a missing `latestTrade`).

## Status

Draft — mirrors `SPEC.md`'s current (unconfirmed) decisions. Needs your review alongside SPEC.md §8's checklist; if any SPEC.md decision changes, the corresponding task above needs a matching edit before that task starts. `tasks/todo.md` tracks live checkpoint status once work begins.
