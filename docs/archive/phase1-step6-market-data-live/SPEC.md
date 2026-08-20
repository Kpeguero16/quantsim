# SPEC — QuantSim Market Data Service (Phase 1, Step 6)

Status: **Approved 2026-07-29 — implemented, Step 6 complete**
Scope: live price polling + Redis for the Market Data Service. Not a whole-project spec — see `agents.md` for that context. Prior specs archived at `docs/archive/phase1-step4-auth/` (Auth Service) and `docs/archive/phase1-step5-market-data/` (historical ingestion — same service, complete).

---

## 1. Objective

Per `PHASE1_CHECKLIST.md` Step 6, add the live half of the Market Data Service, so that:

- A background process polls Alpaca for the watchlist's latest prices every 10–15s
- Latest prices land in Redis, keyed per symbol, so any HTTP request can read them without hitting Alpaca
- Each update is also published to a per-symbol Redis pub/sub channel, for a future WebSocket fan-out (gateway, later phase) to consume — this spec only needs the publish side to be correct and independently verifiable
- Any client (frontend later, curl/redis-cli now) can read the latest cached price for one symbol

This unblocks Step 8's dashboard (symbol list with live prices) and the future WebSocket work mentioned in the checklist. It does **not** unblock anything in Step 7 (gateway) beyond having another `/market-data/*` route to proxy.

**Out of scope for this spec:** WebSocket fan-out itself, gateway routing (Step 7), frontend (Step 8), any symbol outside the curated watchlist, historical data (Step 5, complete).

---

## 2. Decisions

### 2.1 Redis client: `github.com/redis/go-redis/v9` (checklist's import path is stale)

`PHASE1_CHECKLIST.md` says `go-redis/redis/v9`. The module renamed — current canonical import is `github.com/redis/go-redis/v9` (verified against the package's current docs, not memory). Using the old path would fail to resolve.

### 2.2 One batched Alpaca call per tick, not one call per symbol

Alpaca's snapshot endpoint (`GET /v2/stocks/snapshots?symbols=...`) accepts a comma-separated symbol list and returns all of them in one response. The poller makes **one** request per tick for the full watchlist, not seven. This also means a single Alpaca failure is atomic across the whole tick — unlike `Ingest` (Step 5), there's no per-symbol try/catch here, because there's no per-symbol request to fail independently.

### 2.3 Poll interval: 15s, fixed

Middle-to-upper end of the checklist's "10-15 seconds," as a constant (`PollInterval`), not env-configurable — Phase 1 doesn't need that knob yet. One batched request every 15s is trivially within Alpaca's free-tier rate limit (the snapshot endpoint reports `X-RateLimit-*` headers; a request every 15s is nowhere near typical per-minute limits).

### 2.4 "Latest price" = `latestTrade.p` only, no `dailyBar` fallback

Alpaca's snapshot payload includes `latestTrade`, `latestQuote`, `minuteBar`, `dailyBar`, and `prevDailyBar`. This spec uses `latestTrade.p` (the actual last executed trade price) as "the" price — it's what a trading dashboard means by "latest price." If a symbol's snapshot has no `latestTrade` for a tick (e.g., pre-market on a thin feed), that symbol is **skipped for that tick** — old cached value stays until TTL (§2.5) or the next successful tick — rather than substituting `dailyBar.c` or similar. Keeps the poller's logic to "cache what's real, skip what isn't," matching `Ingest`'s existing "skip and report" instinct without adding a fallback chain nobody asked for.

### 2.5 Redis key TTL: 45s (3× poll interval)

`price:{symbol}` is written with a 45s TTL on every successful tick. If the poller stalls or Alpaca is down for more than ~3 ticks, the key expires and `GET /market-data/prices/:symbol` starts returning 404 instead of silently serving an arbitrarily stale price forever. Cheap correctness win, no extra moving parts (Redis does the expiry).

### 2.6 Pub/sub: publish every tick, no change-dedup

Every successful per-symbol cache write also publishes the same JSON payload to `prices:{symbol}`. No "only publish if price changed" logic — that's an optimization for a consumer that doesn't exist yet in Phase 1 (the WebSocket fan-out is explicitly a later phase per the checklist). Verified this spec by `redis-cli SUBSCRIBE`, not by a real consumer.

### 2.7 Polling scope: fixed to `DefaultWatchlist`, not request-configurable

The poller always polls the existing `DefaultWatchlist` (7 symbols, already defined in `internal/service/market_data.go`). No endpoint to add/remove symbols from live polling in Phase 1 — matches the checklist's "for the watchlist" wording and keeps this step's scope to what Step 8's dashboard actually needs.

### 2.8 `GET /market-data/prices/:symbol` on a cache miss: `404`, not `200` with a null price

This is a deliberate contrast with `History` (Step 5, §2.6 of the archived spec), which treats "no data yet" as a valid empty result. Here the watchlist is small and fixed, and a cache miss means one of: the service just started and hasn't ticked yet, Redis had an issue, the TTL expired because polling stalled, or the symbol isn't in the watchlist at all. All four are worth surfacing as "not available right now," not silently returning a null/zero price a chart could plot as real data.

### 2.9 Poller lifecycle: goroutine + `context.Background()`, no graceful shutdown

Started once in `main.go` alongside the HTTP server; runs until the process exits. No cancellation, no drain-on-SIGTERM. This matches the HTTP server's own current lifecycle (also no graceful shutdown) — adding one only for the poller, ahead of doing it for the server too, would be inconsistent scope creep for a Phase 1 checklist item. Flagging so it's a conscious call, not an oversight.

---

## 3. Commands

| Command | Purpose |
|---|---|
| `make docker-up` / `make docker-down` | Start/stop Postgres + Redis |
| `cd services/market-data && go run ./cmd/server` (or `make run-market-data`) | Run the service locally |
| `cd services/market-data && go test ./...` | Run unit tests |
| `cd services/market-data && go mod tidy` | Sync deps after adding `go-redis` |

Manual verification (real Alpaca + Redis running, per checklist "verify Redis keys are populated and updating"):
```
JWT_SECRET=unused DATABASE_URL=<...> REDIS_URL=<...> ALPACA_API_KEY=<real> ALPACA_API_SECRET=<real> PORT=8082 make run-market-data

redis-cli -u "$REDIS_URL" GET price:AAPL              # JSON price payload, updates every ~15s
redis-cli -u "$REDIS_URL" SUBSCRIBE prices:AAPL       # (separate terminal) watch publishes roll in
curl -i localhost:8082/market-data/prices/AAPL         # 200 once the poller has ticked at least once
curl -i localhost:8082/market-data/prices/ZZZZ         # 404 (not in watchlist, never cached)
```

---

## 4. Project structure

Existing (committed, unchanged by this spec except where noted):
```
services/market-data/
  internal/alpaca/client.go, types.go      # GetBars (Step 5)
  internal/service/market_data.go          # Timeframe, DefaultWatchlist, Ingest, History
  internal/service/interfaces.go           # AlpacaClient, HistoricalPriceStore
  internal/handler/router.go               # mounts /market-data/{symbols,ingest,history/{symbol}}
  internal/handler/errors.go               # ErrorResponse, WriteError, WriteJSON
```

New, to be created by this spec:
```
services/market-data/
  internal/alpaca/
    client.go                    # + GetSnapshots(ctx, symbols []string) (map[string]Snapshot, error)
    types.go                     # + Snapshot{LatestTrade *Trade; DailyBar, PrevDailyBar *Bar} wire shape
    client_test.go               # + snapshot cases (httptest fixtures, no real Alpaca calls)
  internal/cache/
    redis_price_cache.go         # RedisPriceCache: SetPrice, GetPrice, PublishPrice (wraps go-redis/v9)
  internal/service/
    interfaces.go                # + SnapshotClient (added to existing AlpacaClient) or new SnapshotClient interface; + PriceCache
    types.go                     # + Price{Symbol, Price, Timestamp}
    errors.go                    # + ErrPriceNotCached (maps to 404)
    live.go                      # Poller{snapshots, cache, symbols, interval}; Run(ctx) ticks + calls pollOnce; LatestPrice(ctx, symbol) reads cache
    live_test.go                 # unit tests, mocked SnapshotClient/PriceCache
    mock/mock.go                 # extend hand-written mocks with the new interface methods
  internal/handler/
    market_data.go                # + PricesHandler
    market_data_test.go           # + cases (cache hit, cache miss)
    router.go                     # + mount GET /market-data/prices/{symbol}
  cmd/server/main.go              # + REDIS_URL fail-fast load, go-redis client, wire cache + Poller, `go poller.Run(ctx)`
```

`services/market-data/go.mod` gains `github.com/redis/go-redis/v9` as a direct dependency (via `go get` + `go mod tidy`).

---

## 5. Code style / conventions

- **Layering:** unchanged — handler → service → client/cache, service depends on interfaces only (`SnapshotClient`, `PriceCache`), never on `*alpaca.Client` or `*redis.Client` directly.
- **Errors:** `ErrPriceNotCached` (service-level sentinel) → handler maps to `404` with the existing JSON error shape (`{"code": "...", "message": "..."}`). No new error shape.
- **Redis key/channel naming:** `price:{SYMBOL}` (cache key), `prices:{SYMBOL}` (pub/sub channel) — symbol uppercased, matching `History`'s existing normalization.
- **JSON payload (cache value = pub/sub message = `GET /prices/:symbol` response body):** `{"symbol": "AAPL", "price": 123.45, "timestamp": "2026-07-29T14:32:01Z"}`.
- **No polling-specific HTTP client tuning beyond what `alpaca.Client` already has** (15s request timeout, from Step 5).
- **New dependency beyond `github.com/redis/go-redis/v9` requires sign-off first** — none else expected.

---

## 6. Testing strategy

- **Alpaca client (`GetSnapshots`):** same `httptest.Server` fixture pattern as `GetBars` (Step 5) — verifies auth headers, multi-symbol response parsing, a symbol missing `latestTrade`, non-2xx status, malformed JSON. No real network calls.
- **Service (`Poller`, `live_test.go`):** table-driven, mocked `SnapshotClient`/`PriceCache`. Cover: successful tick caches + publishes every symbol; one symbol missing `latestTrade` is skipped, others still succeed (§2.4); `SnapshotClient` error aborts the tick with no cache writes (§2.2, atomic failure); `LatestPrice` cache hit vs. `ErrPriceNotCached` on miss.
- **Handlers:** `httptest`-based, mocked service — `200` on cache hit, `404` + JSON error shape on miss.
- **`internal/cache` (Redis wrapper itself):** no dedicated unit test file, same call as Step 5 made for the Postgres store — it's a thin wrapper over `go-redis`, exercised for real via the manual `redis-cli`/curl steps in §3, not mocked-and-unit-tested. If that call turns out wrong, easy to add later.
- **Not in scope:** real-Redis integration tests, load/concurrency testing, testing the 15s ticker's actual timing (tests call `pollOnce` directly, not `Run`'s ticker loop).
- `go test ./...` passes before any checkpoint is marked done.

---

## 7. Boundaries

**Always do:**
- Keep handler → service → client/cache layering; service depends on interfaces only
- Use the existing JSON error shape for `404`
- Run `go test ./...` before flagging a checkpoint done
- Verify the snapshot endpoint's request/response shape against current official Alpaca docs before implementing `GetSnapshots` (already done for this spec — see §2 — but re-verify at implementation time in case the docs changed)

**Ask first:**
- Any dependency beyond `github.com/redis/go-redis/v9`
- Any change to poll interval, TTL, or watchlist scope (§2.3, §2.5, §2.7) once implementation starts
- Adding graceful shutdown for the poller and/or HTTP server (§2.9), if that becomes a priority

**Never do:**
- Commit `.env` or real Redis/Alpaca credentials
- Log API keys or the full `REDIS_URL` (may embed a password) in plaintext
- Fabricate a price when `latestTrade` is missing (§2.4) — skip instead

---

## 8. Confirm before I start

- [x] Redis client + import path (§2.1)
- [x] Batched single-call polling, 15s fixed interval (§2.2, §2.3)
- [x] `latestTrade`-only price, skip-on-missing, no `dailyBar` fallback (§2.4)
- [x] 45s TTL (§2.5)
- [x] Publish-every-tick pub/sub, no dedup (§2.6)
- [x] Watchlist-only polling scope (§2.7)
- [x] 404-on-miss for `/prices/:symbol`, contrasted with `History`'s empty-is-fine (§2.8)
- [x] No graceful shutdown for the poller (§2.9)
- [x] Project structure / checkpoint granularity in §4 — or would you rather see this broken into `tasks/plan.md` checkpoints (Alpaca snapshot client → Redis cache → poller → handler, mirroring Step 5's task order) before any code starts?
