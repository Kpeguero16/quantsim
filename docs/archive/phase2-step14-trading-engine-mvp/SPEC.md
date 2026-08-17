# SPEC — Trading Engine MVP: Order Execution, Positions & Trade History (Step 14)

Status: **Approved 2026-08-17.** All eleven decisions resolved as recommended; §8 records them. Implementation is unblocked — not started.
Scope: new service `services/trading-engine` (`cmd/server`, `internal/service`, `internal/store`, `internal/client`, `internal/handler`, `integration`, `go.mod`), `go.work`, a new migration (`006_...`), `services/gateway` (router, `cmd/server/main.go`, a new body-cap middleware), `Makefile`, `.env.example`. No frontend changes — see Non-goals.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase2-step13-refresh-token-revocation/`.

---

## 1. Objective

`agents.md` §2, "Simulated Trading Engine": execute paper trades, validate account balances, track positions, maintain trade history, calculate profit/loss. Today `/trading/*` returns a hardcoded `501` from the gateway (`services/gateway/internal/handler/router.go`), and `services/trading-engine` is an empty `go.mod` stub — nothing has been built. The schema has been waiting since migration 002: `positions`, `orders`, and `trades` tables exist and are unused.

**Why now.** The only item that was sequenced ahead of this (`docs/security-backlog.md` item 2, refresh-token revocation) closed in Step 13. `PHASE2_CHECKLIST.md`'s own roadmap note under "Then the engine itself" already lists this step's scope: order execution, trade storage and history, position tracking and P/L, and the gateway-wide request-body cap (security-backlog item 4) that's explicitly tied to `/trading/*` going live.

**Objective:** `POST /trading/orders` executes a market buy or sell synchronously against `market-data`'s live cached price, validates the account can afford it (or holds enough to sell), updates balance and position atomically, and records the trade. `GET /trading/orders`, `GET /trading/positions`, and `GET /trading/portfolio` expose the resulting state, including unrealized and realized P/L.

**Non-goals:**
- **Limit orders, stop-loss, take-profit.** `agents.md`'s own MVP/Advanced split puts these in "Advanced" — deferred to a later trading-engine step.
- **Short selling.** Long-only. Selling more than a position holds is rejected, not filled negative.
- **Multi-account-per-user.** Both the schema and `auth`'s `CreateUserWithAccount` already assume exactly one account per user; not revisited here.
- **Async execution / an order book / a matching engine.** Paper trading fills synchronously at the latest cached price. `agents.md`'s MVP is "Market buy/sell," not an exchange simulator.
- **Frontend UI.** Order form, positions table, portfolio dashboard — none of it exists yet and none is built here. This mirrors how Step 11 shipped the entire auth *backend* before Step 13 was the first to touch `frontend/`. The trading UI is sized as its own step once this API exists to build against.
- **WebSocket streaming of fills or positions.** Not part of this step's scope.

---

## 2. Design decisions

### 2.1 New service scaffolding, following the existing pattern exactly

`services/trading-engine` gets `cmd/server/main.go`, `internal/service`, `internal/store`, `internal/handler` — the same layering `market-data` already uses (store implements interfaces the service depends on; handlers are thin decode/dispatch/encode). Added to `go.work`. Not a two-option decision — this is established convention.

### 2.2 Price source: an internal HTTP call to `market-data`, not a shared Redis read

**(a)** A small HTTP client inside `trading-engine` calls `market-data`'s existing `GET /market-data/prices/{symbol}` directly (service-to-service, not through the gateway — the gateway requires an end-user JWT, which has no meaning for one internal service calling another).
**(b)** `trading-engine` connects to the same Redis instance and reads `market-data`'s cache key format itself.

**Recommendation: (a).** It keeps `market-data`'s cache format a private implementation detail behind its own API — the same boundary every other cross-service call in this project already respects (gateway→auth, gateway→market-data are the only two HTTP hops today; this adds a third). It also means `trading-engine` takes on zero new infrastructure — no Redis client, no `REDIS_URL` — which fits, since Redis isn't among `trading-engine`'s own responsibilities in `agents.md`. Reuses the existing `MARKET_DATA_SERVICE_URL` env var (same value the gateway already uses to reach the same service).

### 2.3 Fail **closed** on price-fetch or DB errors — the opposite of Step 13's fail-open

Step 13 fails open on a Redis error because fail-closed there (rejecting every refresh) was a worse outcome than a brief window where a revoked token still works. The calculus flips here: if `market-data` is unreachable, `trading-engine` has no way to know the current price, and filling an order at an unknown or stale price is a direct integrity violation in a system whose whole premise is realistic financial data (`agents.md`: "Financial data modeling," "fintech realism" over "simple CRUD"). **Recommendation:** fail closed on the order-write path — no price, no fill. The order is still persisted, as `rejected` (§2.5), with reason `upstream_unavailable`, reusing `market-data`'s own error code for the same condition (its `Ingest` handler already returns `502 upstream_unavailable` when Alpaca is down). Same posture for a DB error mid-transaction: rollback, reject, no silent retry.

Read paths (`GET /trading/positions`, `GET /trading/portfolio`) are different — see §2.9.

### 2.4 Direct shared-table Postgres access, same database, new `PostgresStore`

`trading-engine` gets its own store hitting the same `postgres` database via `DATABASE_URL`, exactly as `auth` and `market-data` already do (`agents.md`'s architecture diagram already draws every engine service straight to Postgres). It shares the `accounts` table with `auth` — `auth` creates the row at registration and sets the starting balance; `trading-engine` reads and mutates `balance` from here on — and owns `positions`, `orders`, `trades` outright. All four tables have existed, unused, since migration 002.

Flagging this rather than treating it as obvious: it's the first time two services write the same table. No migration needed for `accounts` itself, just naming the shared ownership explicitly so it isn't discovered as a surprise later.

### 2.5 New migration 006: cost basis on positions, fill price and rejection reason on orders, realized P/L on trades

Two gaps in the existing schema block P/L and the audit trail this step wants:

- `positions.avg_cost NUMERIC(20,4) NOT NULL DEFAULT 0` — weighted-average cost basis, updated on every buy fill: `new_avg = (old_avg*old_qty + fill_price*fill_qty) / (old_qty + fill_qty)`. Read (never recomputed) on every sell.
- `orders.filled_price NUMERIC(20,4)` (nullable) and `orders.rejection_reason TEXT` (nullable) — a filled order's execution price lives on the order itself, not only inferable via its trade; a rejected order stays visible in history with why. `orders.status` already exists (`'pending'` default) and moves to `'filled'` or `'rejected'`.
- `trades.realized_pl NUMERIC(20,4)` (nullable, sell trades only) — captured at execution time. Recomputing a historical sell's P/L from the position's *current* `avg_cost` later would be wrong once later buys move that average.

**Recommendation:** persist rejected orders, not just successful ones. One extra nullable column, and it's exactly the audit-trail realism the project is optimizing for. The alternative — only persist orders that fill — is simpler but throws that trail away for no real savings.

### 2.6 Row-level locking for balance/position mutation

Order execution: (1) resolve `account_id` from the authenticated `user_id`; (2) fetch the live price from `market-data` — outside any transaction, since it's a network call; (3) open one DB transaction: `SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`, validate funds (buy) or held quantity (sell), upsert the position, insert the trade, update the order, update the balance, commit. `FOR UPDATE` serializes concurrent orders on the same account so two simultaneous buys can't both read the same pre-trade balance and double-spend it. Not an alternative-bearing decision — this is the standard, necessary shape for the invariant "never spend money you don't have," stated here as a requirement rather than a choice.

### 2.7 Symbol validity: no separate whitelist — the price-fetch 404 is the rejection

**(a)** Validate `symbol` against `market-data`'s `DefaultWatchlist` before even requesting a price (would need exporting or duplicating a list that today is private to `market-data/internal/service`).
**(b)** Just request the price; `market-data`'s existing `404 price_not_cached` becomes the order's rejection reason directly (`symbol_unavailable`).

**Recommendation: (b).** No duplicated symbol list to drift out of sync between two services, one fewer coupling point, and the caller sees an equivalent rejection either way.

### 2.8 Long-only: selling more than is held is rejected, not shorted

`quantity > position.quantity` (or no position row at all) on a sell → `400 insufficient_position`, no trade, no partial fill. `agents.md` never mentions short-selling as in-scope (only options trading appears, under "Future Expansion Possibilities") — a scope boundary, not a tradeoff.

### 2.9 API surface — `/trading/*`, mounted in the gateway's existing authenticated group

- **`POST /trading/orders`** — body `{"symbol", "side": "buy"|"sell", "quantity"}`, `order_type` implicitly `"market"`. Executes synchronously. `201` with `{"order", "trade", "balance"}` on fill. On rejection, the standard `WriteError` shape (`code` + `message`) with the matching status: `400 invalid_request` (malformed body, non-positive quantity, bad side), `400 insufficient_balance`, `400 insufficient_position`, `404 symbol_unavailable`, `502 upstream_unavailable`. The order row is persisted either way (§2.5); a rejected order's id/reason is visible via `GET /trading/orders`, not duplicated into the rejection response body.
- **`GET /trading/orders`** — the authenticated user's order history, newest first, rejected orders included.
- **`GET /trading/positions`** — open positions (`quantity > 0`): `symbol`, `quantity`, `avg_cost`, `latest_price`, `unrealized_pl`.
- **`GET /trading/portfolio`** — one rollup: `{"balance", "positions": [...], "total_equity", "total_unrealized_pl"}`, so a future frontend dashboard needs one call instead of composing three.

**Read-path price fetch fails open**, unlike the write path (§2.3): if `market-data` is unreachable while computing `latest_price`/`unrealized_pl`, the position is still returned with `latest_price: null` and `unrealized_pl` falls back to `0` against `avg_cost` (i.e. `total_equity` treats an unpriceable position at cost rather than going missing or making the whole endpoint fail). A read degrading to "no live P/L available" is not the integrity violation a write at an unknown price would be.

### 2.10 Gateway wiring: replace the `501` placeholder, land the body cap it's been waiting on

`/trading/*` becomes a proxy to `trading-engine` (`services/gateway/internal/proxy`, same pattern as `auth`/`market-data`), inside the existing `RequireAuth` + `InjectUserID` group. New `TRADING_ENGINE_SERVICE_URL` env var, default `http://localhost:8083` (auth=8081, market-data=8082, next free port).

`docs/security-backlog.md` item 4: add a request-body-size-cap middleware at the gateway covering **every** proxied route, not just the login-specific one that already exists (`maxLoginBodyBytes` in `services/gateway/cmd/server/main.go`, scoped narrowly to `/auth/login` per that file's own comment pointing at this exact backlog item). **Recommendation:** 64 KiB, matching the per-request cap `auth` already applies via `http.MaxBytesReader` since Step 9. Order payloads are tiny; the point of this control is coverage, not tightness — new services inherit it automatically, which is the shape the backlog item itself asks for.

### 2.11 `trading-engine` authenticates its own routes independently, matching `auth`'s precedent

`services/auth/internal/handler/router.go` runs `pkgauth.RequireAuth(jwtSecret)` on `/me` even though the gateway already validated the same token and injected `X-User-ID` — each service revalidates rather than trusting the proxy header. **Recommendation:** `trading-engine` follows the same precedent (needs its own `JWT_SECRET`, already shared config) instead of trusting `X-User-ID` alone. The trading engine is at least as sensitive a surface as `/me`; diverging here would make it the one place in the codebase that trusts the header.

---

## 3. Project structure

```
services/trading-engine/
  cmd/server/main.go
  internal/
    service/
      interfaces.go   # AccountStore, TradingStore, PriceClient
      trading.go       # Service.PlaceOrder / .Orders / .Positions / .Portfolio
      types.go          # Order, Trade, Position, PlaceOrderRequest, PortfolioResponse...
      errors.go
      mock/              # in-memory test doubles, mirroring auth's service/mock
    store/
      postgres_trading_store.go   # TradingStore + AccountStore against Postgres
    client/
      market_data_client.go        # PriceClient over HTTP to market-data
    handler/
      trading.go
      router.go
      errors.go          # WriteJSON/WriteError, copied per-service like auth/market-data already do
  integration/
    main_test.go
    trading_store_test.go
  go.mod / go.sum
infra/migrations/
  006_trading_cost_basis_and_order_audit.up.sql
  006_trading_cost_basis_and_order_audit.down.sql
```

---

## 4. Configuration

New for `trading-engine` (`.env.example` additions / reused vars):

| Var | Default | Notes |
|---|---|---|
| `DATABASE_URL` | — | existing, shared |
| `JWT_SECRET` | — | existing, shared — needed for `RequireAuth` (§2.11) |
| `MARKET_DATA_SERVICE_URL` | `http://localhost:8082` | existing var, reused as-is |
| `PORT` | `8083` | new |
| `BIND_ADDR` | `127.0.0.1` | existing pattern |

Gateway additions:

| Var | Default |
|---|---|
| `TRADING_ENGINE_SERVICE_URL` | `http://localhost:8083` |

`Makefile`: add `run-trading-engine`, add `services/trading-engine` to `GO_MODULES`, extend `test-integration` and `vet`'s tagged pass to include `services/trading-engine/integration` (alongside, not replacing, `services/auth`'s — `market-data`'s own integration gap stays a separately tracked, out-of-scope item per `PHASE2_CHECKLIST.md`).

---

## 5. Testing strategy

- **Service-layer unit tests** against mocked `AccountStore`/`TradingStore`/`PriceClient` — the business rules: insufficient balance, insufficient position, symbol unavailable, upstream unavailable, weighted-avg-cost math, realized-P/L math. No Postgres, no network.
- **Store-layer integration tests** (Step 12's harness, tagged `integration`, real Postgres) — required, not optional, for this store specifically: `FOR UPDATE` locking and a multi-table transaction are exactly what a mock cannot meaningfully verify. Include one **concurrency test**: fire two concurrent buy orders that together exceed the account balance, assert exactly one succeeds and the other is rejected `insufficient_balance` — proving the lock actually serializes rather than merely reading correct on paper.
- **Handler-layer tests** — HTTP contract: status codes, JSON shapes, error codes, using a mocked `Service`.
- **`PriceClient` in tests** is a fake in service/handler tests and an `httptest.Server` in integration tests, so nothing in the suite requires the real `market-data` service running.

---

## 6. Code style

Reuse what's already established: WHY-only doc comments (`auth.go`'s style — no restating what the code already says), the existing `WriteJSON`/`WriteError` JSON error shape (`{"error": {"code", "message"}}`), `float64` for money fields matching existing code (`StartingBalance = 100000.00` in `auth`) rather than introducing a decimal library — Postgres holds the authoritative `NUMERIC(20,4)`, Go's `float64` is the existing convention project-wide, and fixing that precision gap (if ever needed) is a cross-cutting change out of scope here.

---

## 7. Commands

```bash
cd services/trading-engine && go build ./...
cd services/trading-engine && go test ./...
make test-integration       # extended to include trading-engine's suite
make run-trading-engine     # new Makefile target
```

---

## Boundaries

- **Always:** run the full test suite and `go vet` (including the tagged integration pass) before each task's commit; keep the order-execution transaction as the only place that mutates `balance`/`positions`/`orders`/`trades` together.
- **Ask first:** any new module dependency; any change to the `accounts`/`positions`/`orders`/`trades` schema beyond migration 006 as scoped here; touching `pkg/auth` or the gateway's existing `/auth/*`/`/market-data/*` wiring.
- **Never:** implement limit/stop/take-profit orders, short-selling, or any frontend change under this spec — all explicitly out of scope (§1); fail open on the order-write path (§2.3).

---

## 8. Decisions resolved before implementation

Resolved 2026-08-17, all as recommended:

| # | Decision | Resolution |
|---|---|---|
| 1 | Service scaffolding | **New `services/trading-engine`, layered like `market-data`** — §2.1 |
| 2 | Price source | **Internal HTTP call to `market-data`**, not a shared Redis read — §2.2 |
| 3 | Failure posture on order-write path | **Fail closed** — no price, no fill, order recorded as rejected — §2.3 |
| 4 | Data ownership | **Direct Postgres access**, same database; shares `accounts.balance` with `auth` — §2.4 |
| 5 | Schema for cost basis / audit trail | **Migration 006** — `positions.avg_cost`, `orders.filled_price`/`rejection_reason`, `trades.realized_pl`; rejected orders persisted — §2.5 |
| 6 | Concurrency control | **`SELECT ... FOR UPDATE`** on the account row for the whole order transaction — §2.6 |
| 7 | Symbol validity | **No separate whitelist** — `market-data`'s `404` becomes the rejection — §2.7 |
| 8 | Selling more than held | **Rejected (`insufficient_position`)**, long-only, no shorting — §2.8 |
| 9 | API surface / read-path failure posture | **`POST /trading/orders`, `GET /trading/orders`\`/positions`\`/portfolio`**; reads fail open on price lookup, unlike writes — §2.9 |
| 10 | Gateway wiring | **Replace the `501` placeholder; add a 64 KiB gateway-wide body cap** (`security-backlog` item 4) — §2.10 |
| 11 | Auth trust model | **`trading-engine` revalidates the JWT itself**, matching `auth`'s `/me` precedent, rather than trusting `X-User-ID` alone — §2.11 |

---

## 9. Implementation

Not started. `tasks/plan.md` and `tasks/todo.md` get created once planning begins, per the gated workflow (`agents.md`: spec reviewed → plan → checkpoints). Per Khalil, the plan and build phases will run under a different model — this session stops here.
