# Implementation Plan — Trading Engine MVP (Step 14)

## Overview

`SPEC.md` is **approved** (2026-08-17); §8 records all eleven decisions, every one resolved as recommended. This plan turns it into 17 tasks across 4 phases, on branch `step14-trading-engine-mvp`.

Build `services/trading-engine` — the fourth Go service and the first one written from an empty `go.mod` since `market-data` in Step 5. `POST /trading/orders` executes a market buy or sell synchronously against `market-data`'s live cached price, validates funds or holdings, mutates `accounts.balance` and `positions` inside one locked transaction, and records the trade. Three read endpoints expose the result. The gateway's `/trading/*` `501` placeholder is replaced with a proxy, and the gateway-wide 64 KiB body cap (`docs/security-backlog.md` item 4) lands alongside it.

**Backend only.** No frontend work under this spec (§1 Non-goals).

**Baseline, to re-check at every checkpoint** — the integration harness `DROP DATABASE`s, and the dev database holds real rows:

```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT 'users=' || count(*) FROM users"    # 20 as of 2026-08-17
```

---

## Architecture decisions

Restated from `SPEC.md` §2 — none of these are reopened here:

- **New `services/trading-engine`**, layered exactly like `market-data`: `cmd/server`, `internal/{service,store,client,handler}` — §2.1
- **Price comes from an internal HTTP call** to `market-data`'s `GET /market-data/prices/{symbol}`, not a shared Redis read. No Redis client, no `REDIS_URL` — §2.2
- **The write path fails closed; the read path fails open.** No price ⇒ no fill. A read with no price ⇒ `latest_price: null`, not an error — §2.3 / §2.9
- **Direct Postgres access, same database.** `trading-engine` owns `positions`/`orders`/`trades` and *shares* `accounts.balance` with `auth` — the first cross-service table write in the project — §2.4
- **Migration 006** adds `positions.avg_cost`, `orders.filled_price`, `orders.rejection_reason`, `trades.realized_pl`. Rejected orders are persisted — §2.5
- **`SELECT ... FOR UPDATE` on the account row** for the whole order transaction — §2.6
- **No symbol whitelist.** `market-data`'s `404 price_not_cached` becomes `symbol_unavailable` — §2.7
- **Long-only.** Selling more than held is `400 insufficient_position` — §2.8
- **`trading-engine` revalidates the JWT itself**, matching `auth`'s `/me` precedent, rather than trusting the gateway's `X-User-ID` — §2.11

---

## Four things the spec leaves for the plan to decide

**Resolved 2026-08-17 by Khalil — all four accepted as recommended.** They are decisions now, not proposals; the wording below is kept as written so the reasoning survives with the outcome.

These surfaced while reading the spec against the code. Each has a recommendation; **none blocks Task 1**, and every one is settled before the task that needs it.

### D1 — A rejected order commits; it does not roll back

§2.3 says "rollback, reject" for a DB error, and §2.5 says rejected orders are persisted. Those are two different failure classes and conflating them loses the audit trail — a rollback would erase the very row §2.5 wants kept. The split this plan implements:

| Rejection cause | Where it is detected | What happens to the order row |
|---|---|---|
| `invalid_request` (bad side, non-positive quantity, malformed JSON) | handler, before the service | **Nothing persisted** — never became an order |
| `symbol_unavailable`, `upstream_unavailable` | before the transaction opens (price fetch) | Single-statement insert, `status='rejected'`, reason set |
| `insufficient_balance`, `insufficient_position` | *inside* the transaction, after `FOR UPDATE` | Insert the rejected order, **`COMMIT`** — balance, position, and trades untouched |
| DB/infrastructure failure mid-transaction | inside the transaction | **`ROLLBACK`**, `500 internal_error`, nothing persisted — there is no working connection to persist with |

**Recommendation: as tabled.** A business rejection is a durable outcome and must commit. A rollback is reserved for the case where the database itself failed, which is the only case where losing the row is unavoidable rather than chosen.

### D2 — Validation reads the balance *inside* the lock, not before it

The order is: resolve account → fetch price (outside any transaction, it is a network call) → `BEGIN` → `SELECT balance ... FOR UPDATE` → validate → mutate → `COMMIT`. The balance used for validation must be the one read *by the locking select*, not a value read earlier. Reading first and locking second is the exact bug §2.6 exists to prevent, and it looks correct.

**Recommendation: the store exposes one method that owns the whole transaction** (`ExecuteOrder`), rather than the service orchestrating `Begin`/validate/`Commit` across store calls. The service cannot hold a transaction open across interface boundaries without leaking `pgx` into `internal/service`, which would break the layering every other service follows.

Consequence, stated so it is not discovered later: the balance/holding *checks* live in the store, not the service. The service still owns everything checkable without a lock — side, quantity, price acquisition, error mapping, and the P/L arithmetic — and the service-layer unit tests drive rejection through the mock store returning `ErrInsufficientBalance`.

### D3 — A user with no account row is `500`, not a new 4xx

Every user gets an account at registration (`auth`'s `CreateUserWithAccount`). A valid token whose user has no account is a broken invariant, not a client error, and §2.9's error list has no code for it. **Recommendation:** log it and return `500 internal_error`. Inventing a `404 account_not_found` would put a code in the contract that should never be reachable.

### D4 — No new indexes in migration 006

`GET /trading/orders` scans `orders` by `account_id`, which has no index. Adding one is a schema change beyond 006 as scoped, which `SPEC.md`'s Boundaries put behind "ask first". **Recommendation: do not add it.** The dev database has 20 accounts and zero orders; the index would be speculative. Record it in `docs/deferred-tuning.md` with a named trigger (first account exceeding ~1k orders, or the history endpoint appearing in a slow-query log) — the pattern Steps 11–13 already use.

---

## Dependency graph

```
T1 migration 006
   │
   └─> T2 module scaffold (go.mod, go.work, Makefile, /healthz)
          │
          ├─> T3 service contract (types, interfaces, errors, mocks)
          │      ├─> T4 PlaceOrder: buy ──────────────┐
          │      └─> T5 PriceClient (HTTP)            │
          │                                            │
          ├─> T6 Postgres store: buy transaction      │
          │      └─> T7 integration harness + guards   │
          │             └─> T8 store integration tests (incl. CONCURRENCY)
          │                                            │
          └────────────────────────────────────────> T9 handler + main wiring
                                                       │
                       ┌───────────────────────────────┤
                       ├─> T10 sell path
                       ├─> T11 GET /orders
                       ├─> T12 GET /positions (fail-open)
                       └─> T13 GET /portfolio  (needs T12)
                                     │
T14 gateway body cap (independent) ──┼─> T15 gateway /trading/* proxy
                                     │         │
                                     └─────────┴─> T16 adversarial review
                                                        └─> T17 docs close-out
```

**T7 gates T8, the way Step 12's Task 2 gated everything.** The harness drops a database; its guards get proven before a single destructive test runs.

**Safe to parallelize if work is ever split:** T5 (PriceClient) is independent of T4 and T6. T14 (body cap) touches only the gateway and depends on nothing in this step.

---

## Phase 1 — Foundation

### Task 1 — Migration 006

**Description:** Add the four columns P/L and the audit trail need. No data backfill — every table is empty.

**Files:** `infra/migrations/006_trading_cost_basis_and_order_audit.{up,down}.sql`

```sql
-- up
ALTER TABLE positions ADD COLUMN avg_cost NUMERIC(20,4) NOT NULL DEFAULT 0;
ALTER TABLE orders    ADD COLUMN filled_price NUMERIC(20,4);
ALTER TABLE orders    ADD COLUMN rejection_reason TEXT;
ALTER TABLE trades    ADD COLUMN realized_pl NUMERIC(20,4);
```

**Acceptance criteria:**
- [ ] `avg_cost` is `NOT NULL DEFAULT 0`; the other three are nullable (a pending order has no fill price; a buy has no realized P/L)
- [ ] The `.down.sql` drops exactly those four columns and nothing else
- [ ] A header comment states *why* `realized_pl` is captured at execution time rather than recomputed (§2.5: later buys move `avg_cost`, so a recomputed historical P/L would be wrong)

**Verification:**
- [ ] `make migrate-up` succeeds against the dev database; `make migrate-down` then `make migrate-up` succeeds again (the down file is real, not decorative)
- [ ] `\d positions` / `\d orders` / `\d trades` show the new columns
- [ ] Dev DB still `users=20, accounts=20`

**Dependencies:** None. **Scope:** S (2 files).

---

### Task 2 — Module scaffold, workspace, Makefile

**Description:** A buildable, runnable service with `/healthz` and nothing else. No trading logic. This exists so every later task lands in a module that already compiles, tests, and vets.

**Files:** `services/trading-engine/go.mod`, `services/trading-engine/cmd/server/main.go`, `services/trading-engine/internal/handler/{router.go,errors.go}`, `go.work`, `Makefile`, `.env.example`

- `go.mod`: `chi/v5`, `pgx/v5`, `google/uuid`, `kpeguero/quantsim/pkg` + `replace ../../pkg`. **No new project dependency** — every one is already used by `auth` or `market-data`, so `SPEC.md`'s "ask first: any new module dependency" is not triggered.
- `main.go`: require `DATABASE_URL` and `JWT_SECRET` (`log.Fatal` if absent, matching `market-data`), `PORT` default `8083`, `BIND_ADDR` default `127.0.0.1`. No `REDIS_URL` — §2.2.
- `handler/errors.go`: copy `market-data`'s `WriteJSON`/`WriteError` verbatim. Per-service duplication is the established convention (§6), not an oversight.
- `Makefile`: add to `GO_MODULES`; add `run-trading-engine`; add it to `help`.
- `.env.example`: a `trading-engine` block noting `PORT=8083` and that `MARKET_DATA_SERVICE_URL` is reused as-is.

**Acceptance criteria:**
- [ ] `cd services/trading-engine && go build ./...` succeeds
- [ ] `make run-trading-engine` boots and `curl localhost:8083/healthz` returns `ok`
- [ ] Booting without `JWT_SECRET` or `DATABASE_URL` exits with a named error, not a nil-pointer panic later
- [ ] `go.work` lists `./services/trading-engine`

**Verification:**
- [ ] `make test` — all 4 modules green
- [ ] `make vet` — clean, including the new module

**Dependencies:** T1. **Scope:** M (6 files).

### ⏸️ Checkpoint A — Foundation
- [ ] `make test`, `make vet` green across 4 modules; `make test-integration` still 18 PASS / 0 SKIP
- [ ] Migration applied and reversible; dev DB `users=20, accounts=20`
- [ ] The service boots and answers `/healthz`. Nothing trades yet.
- [ ] **Review with Khalil before proceeding**

---

## Phase 2 — The first vertical slice: a market buy fills, end to end

### Task 3 — Service contract: types, interfaces, errors, mocks

**Description:** The shapes and the seams, with no behaviour behind them. Defining the contract before any implementation is what lets T4, T5, and T6 be written against a fixed target.

**Files:** `internal/service/{types.go,interfaces.go,errors.go}`, `internal/service/mock/mock.go`

- `types.go` — `Order`, `Trade`, `Position`, `PlaceOrderRequest`, `PlaceOrderResult{Order,Trade,Balance}`, `PortfolioResponse`. `float64` for money, per §6. JSON tags matching §2.9's response shapes.
- `interfaces.go` — `AccountStore` (resolve account by user ID), `TradingStore` (`ExecuteOrder`, `RecordRejectedOrder`, `ListOrders`, `ListPositions`), `PriceClient` (`LatestPrice(ctx, symbol) (float64, error)`).
- `errors.go` — `ErrInsufficientBalance`, `ErrInsufficientPosition`, `ErrSymbolUnavailable`, `ErrUpstreamUnavailable`, `ErrInvalidOrder`, `ErrAccountNotFound`. Each with a doc comment naming the status a handler maps it to, matching `market-data/internal/service/errors.go`.
- `mock/` — in-memory doubles plus a fake `PriceClient`, with settable error fields (`auth`'s `mock.UserStore` is the template), and `var _ service.TradingStore = (*TradingStore)(nil)` compile-time assertions.

**Acceptance criteria:**
- [ ] Money fields are `float64`; no decimal library introduced (§6)
- [ ] Every error has a doc comment stating the HTTP status it maps to
- [ ] Compile-time interface assertions in the mock package
- [ ] `ExecuteOrder` takes an already-resolved price — the store never fetches one

**Verification:**
- [ ] `go build ./...`, `make vet` clean

**Dependencies:** T2. **Scope:** M (4 files).

---

### Task 4 — `PlaceOrder`, buy path, with unit tests

**Description:** The service-layer business rules for a market buy, against mocks. No Postgres, no network.

**Files:** `internal/service/trading.go`, `internal/service/trading_test.go`

Flow: validate request → `LatestPrice` → on `ErrSymbolUnavailable`/`ErrUpstreamUnavailable` call `RecordRejectedOrder` and return the error (fail closed, §2.3) → `ExecuteOrder` → map store errors.

**Acceptance criteria:**
- [ ] Rejected on: non-positive quantity, side other than `buy`/`sell`, empty symbol — before any price fetch
- [ ] A price-fetch failure **persists a rejected order** and returns the error; it never fills at a guessed or stale price
- [ ] `ErrInsufficientBalance` from the store propagates unchanged
- [ ] Weighted-average cost is asserted with a real number, not a tautology: 10 @ $100 then 10 @ $120 ⇒ `avg_cost == 110`; float comparisons use a tolerance
- [ ] A test proves the price fetch happens *before* the store call and is skipped entirely when validation fails

**Verification:**
- [ ] `cd services/trading-engine && go test ./...` green
- [ ] Mutation check: make `PlaceOrder` swallow the price error and fill at `0` — the fail-closed test must fail

**Dependencies:** T3. **Scope:** M (2 files).

---

### Task 5 — `PriceClient` over HTTP

**Description:** The internal call to `market-data`. Independent of T4 and T6 — can be built in any order relative to them.

**Files:** `internal/client/market_data_client.go`, `internal/client/market_data_client_test.go`

`GET {MARKET_DATA_SERVICE_URL}/market-data/prices/{symbol}`, decoding `market-data`'s `service.Price` shape (`{symbol, price, timestamp}`) into a `float64`. An explicit `http.Client{Timeout: ...}` — this sits on the synchronous order path, so an unbounded client turns a hung `market-data` into a hung order.

**Acceptance criteria:**
- [ ] `404` → `service.ErrSymbolUnavailable`; any other non-2xx, a network error, or a timeout → `service.ErrUpstreamUnavailable`
- [ ] A 2xx body that is malformed JSON, or a price of `0`/negative, is `ErrUpstreamUnavailable` — not a fill at zero
- [ ] Explicit client timeout, with a comment saying why
- [ ] Symbol is path-escaped
- [ ] Tests use `httptest.Server`; nothing requires the real `market-data` running (§5)

**Verification:**
- [ ] `go test ./...` green, all four error paths covered

**Dependencies:** T3. **Scope:** S (2 files).

---

### Task 6 — Postgres store: account resolution and the buy transaction

**Description:** The single most important code in this step — the locked, multi-table transaction. Written now, *proven* in T8.

**Files:** `internal/store/postgres_trading_store.go`

```
BEGIN
  SELECT id, balance FROM accounts WHERE user_id = $1 FOR UPDATE
  -- validate cost <= balance
  -- on failure: INSERT order (status='rejected', rejection_reason) ; COMMIT   (D1)
  INSERT INTO orders (..., status='filled', filled_price)
  INSERT INTO trades (..., price)
  INSERT INTO positions ... ON CONFLICT (account_id, symbol) DO UPDATE
      SET avg_cost = (positions.avg_cost * positions.quantity + EXCLUDED.avg_cost * EXCLUDED.quantity)
                     / (positions.quantity + EXCLUDED.quantity),
          quantity = positions.quantity + EXCLUDED.quantity
  UPDATE accounts SET balance = balance - $cost, updated_at = now()
COMMIT
```

**Acceptance criteria:**
- [ ] `FOR UPDATE` is on the `SELECT` whose balance is validated — one statement, not a read followed by a separate lock (D2)
- [ ] `defer tx.Rollback(ctx)` after `Begin`, matching `CreateUserWithAccount`'s no-op-once-committed pattern
- [ ] `var _ service.TradingStore = (*PostgresTradingStore)(nil)` and the same for `AccountStore` — the assertion Step 12 added for exactly this reason
- [ ] Business rejections **commit**; only infrastructure errors roll back (D1)
- [ ] No `service.ErrInsufficientBalance` produced anywhere outside the lock
- [ ] `pgx.ErrNoRows` on the account lookup → `ErrAccountNotFound` (D3)
- [ ] The `FOR UPDATE` carries a comment explaining that removing it is silently correct-looking and breaks the double-spend invariant

**Verification:**
- [ ] `go build ./...`, `make vet` clean (no test can prove this task — that is T8's job, and the ordering is deliberate)

**Dependencies:** T3. **Scope:** M (1 large file).

---

### Task 7 — Integration harness, and proving its guards before anything destructive runs

**Description:** `services/trading-engine/integration` needs the Step 12 harness, which lives in `services/auth/integration` as an untagged-but-test-only, package-private file in a *different module*. It cannot be imported. **This task copies it**, and re-proves the guards in their new home.

**Files:** `services/trading-engine/integration/{harness_test.go,main_test.go,harness_guard_test.go}`, `Makefile`

> **Deviation from `SPEC.md` §3, flagged rather than silently taken.** §3's file list names only `main_test.go` and `trading_store_test.go`. The ~350-line harness has to live somewhere; this plan adds `harness_test.go` and `harness_guard_test.go` alongside them. **Recommendation: copy, do not extract to `pkg/`.** `pkg` is a production module — putting a helper that runs `DROP DATABASE` into the dependency graph of every service is a worse trade than duplicating a test file. Revisit if a third service needs it.

Copy `assertTestDB` (both the `protectedDatabases` denylist *and* the exact match), `repoRoot`, `dotenv`, `resolveDSNs`, `ensureTestDatabase`, `applyMigrations`, `truncateAll`, and the `TestMain` skip/fail split. **No Redis setup** — `trading-engine` has no Redis. Add `insertAccountRaw(user, balance)` and `insertPositionRaw(account, symbol, qty, avgCost)` seed helpers.

**Acceptance criteria:**
- [ ] `//go:build integration` on every file; `go test ./...` in the module does **not** compile them
- [ ] `assertTestDB` rejects `postgres`, `quantsim`, and `""` — proven by a test, not by inspection
- [ ] The denylist is copied intact. It is what makes `testDBName` itself subject to the guard rather than the yardstick for it (`docs/TESTING_STRUCTURE.md` §6a: "do not simplify that to a single comparison")
- [ ] Postgres unreachable ⇒ **skip with a printed reason**; a guard violation or failed migration ⇒ **exit non-zero**
- [ ] No `t.Parallel()`, with the comment saying why
- [ ] `quantsim_test` has the migrated schema including 006's columns
- [ ] `Makefile`: `test-integration` and `vet`'s tagged pass both extended to `services/trading-engine/integration`, **alongside** `services/auth`'s, not replacing it

**Verification:**
- [ ] `make test-integration` — auth's 18 still PASS, trading-engine's guard tests PASS, `-v` output inspected for silent SKIPs
- [ ] With Docker down: skips, exits 0, prints the reason
- [ ] Point `TEST_DATABASE_URL` at `postgres` by hand → the suite **refuses**, non-zero, without truncating
- [ ] **Dev DB still `users=20, accounts=20`**

**Dependencies:** T2 (T6 not required — the harness is independent of the store). **Scope:** M (4 files, mostly copied).

---

### Task 8 — Store integration tests, including the concurrency proof

**Description:** The tests `SPEC.md` §5 calls load-bearing rather than optional polish. `FOR UPDATE` and a multi-table transaction are precisely what a mock cannot verify.

**Files:** `services/trading-engine/integration/trading_store_test.go`

**Acceptance criteria:**
- [ ] Happy-path buy: balance decreases by exactly `price × quantity`, one `orders` row `status='filled'` with `filled_price`, one `trades` row, one `positions` row with the right `avg_cost`
- [ ] Second buy at a different price moves `avg_cost` by the weighted formula, read back from Postgres `::text` (Step 12's precedent — reading `NUMERIC(20,4)` as text is how precision loss becomes visible instead of being rounded away by the assertion)
- [ ] Insufficient balance: **the order row exists with `status='rejected'` and a reason** (D1), balance unchanged, no trade, no position
- [ ] **Concurrency test:** an account with balance `1000`; two goroutines released from a common barrier each buy 1 share at `600`. Assert **exactly one** returns nil and one returns `ErrInsufficientBalance`; final balance is exactly `400`; exactly one `trades` row exists
- [ ] `ErrAccountNotFound` for a user with no account row

**Verification:**
- [ ] `make test-integration` green, `-v` inspected — zero unexpected SKIPs
- [ ] **Mutation check (required, `docs/TESTING_STRUCTURE.md` §6a):** delete `FOR UPDATE` from the query and re-run. The concurrency test **must fail** — at READ COMMITTED both transactions then read `1000`, both pass validation, and the balance lands wrong. A concurrency test that still passes without the lock proves nothing and would be trusted anyway
- [ ] Second mutation: make the rejection path `ROLLBACK` instead of `COMMIT` — the "rejected order is persisted" assertion must fail
- [ ] Dev DB still `users=20, accounts=20`

**Dependencies:** T6, T7. **Scope:** M (1 file, several tests).

---

### Task 9 — `POST /trading/orders`: handler, router, main wiring

**Description:** Close the slice. After this task a real HTTP request against the running service moves real money in a real database.

**Files:** `internal/handler/trading.go`, `internal/handler/trading_test.go`, `internal/handler/router.go`, `cmd/server/main.go`

- Router: `/healthz` public; `/trading/*` inside `pkgauth.RequireAuth(jwtSecret)` (§2.11).
- Handler: `pkgauth.UserIDFromContext` → parse UUID → service → map errors to §2.9's status/code table.
- `main.go`: build `pgxpool`, store, `PriceClient`, service, handler, router.

**Acceptance criteria:**
- [ ] `201` with `{"order","trade","balance"}` on a fill
- [ ] Error mapping exactly as §2.9: `400 invalid_request`, `400 insufficient_balance`, `400 insufficient_position`, `404 symbol_unavailable`, `502 upstream_unavailable`, `500 internal_error`
- [ ] A missing/unparseable context user ID is `401`, never a `500` or an empty-UUID query
- [ ] A rejection response carries only `code` + `message` — the order id is **not** duplicated into it (§2.9); it surfaces via `GET /trading/orders`
- [ ] Handler tests use a mocked service and assert JSON shape, not just status codes
- [ ] `http.MaxBytesReader` on the request body, matching `auth`'s per-request cap

**Verification:**
- [ ] `make test`, `make vet` green
- [ ] **Manual, end to end:** `make docker-up`, run `auth` + `market-data` + `trading-engine`; register a user, log in, `POST /trading/orders` directly at `:8083` with the bearer token; confirm `201`, then confirm the `orders`/`trades`/`positions` rows and the reduced `accounts.balance` in `psql`
- [ ] Stop `market-data` and re-send: `502 upstream_unavailable`, **no fill**, and a `rejected` order row exists

**Dependencies:** T4, T5, T6. **Scope:** M (4 files).

### ⏸️ Checkpoint B — A buy works end to end
- [ ] `make test` green (4 modules), `make test-integration` green (auth 18 + trading-engine), `make vet` clean
- [ ] Both mutation checks from T8 performed and reverted
- [ ] The manual buy and the `market-data`-down rejection both verified by hand, not inferred from tests
- [ ] Dev DB `users=20, accounts=20`
- [ ] **Review with Khalil before proceeding**

---

## Phase 3 — Sell, and the read endpoints

### Task 10 — The sell path

**Description:** The mirror of the buy, plus realized P/L. The one genuinely new rule: `avg_cost` is *read* on a sell, never recomputed.

**Files:** `internal/service/trading.go`, `internal/store/postgres_trading_store.go`, `internal/service/trading_test.go`, `services/trading-engine/integration/trading_store_test.go`

`realized_pl = (fill_price − avg_cost) × quantity`, captured on the trade row at execution time. Position quantity decreases; `avg_cost` is untouched. A position reaching quantity `0` **keeps its row** — the weighted-average formula then makes the next buy's `avg_cost` equal that buy's price with no special case, and `GET /trading/positions` filters on `quantity > 0` anyway.

**Acceptance criteria:**
- [ ] Selling more than held, or with no position row at all, is `ErrInsufficientPosition` — checked inside the same locked transaction as the balance check
- [ ] No path can produce a negative position quantity (long-only, §2.8)
- [ ] `realized_pl` is written from the `avg_cost` in effect **at execution**; an integration test buys at 100, buys at 200, sells, then buys again — and asserts the earlier sell's stored `realized_pl` is unchanged by the later buy
- [ ] Balance increases by exactly `price × quantity`
- [ ] A sell of exactly the full position leaves `quantity = 0` and the row present

**Verification:**
- [ ] `make test`, `make test-integration` green
- [ ] Manual: buy 10, sell 4, confirm balance, position quantity, and `trades.realized_pl` in `psql`

**Dependencies:** T9. **Scope:** M (4 files).

---

### Task 11 — `GET /trading/orders`

**Description:** Order history, newest first, **rejected orders included** — an audit trail missing its failures would undercut the whole point of §2.5.

**Files:** `internal/store/postgres_trading_store.go`, `internal/service/trading.go`, `internal/handler/trading.go`, plus tests

**Acceptance criteria:**
- [ ] Scoped to the authenticated user's account — a second user's orders are never returned (asserted by an integration test with two accounts, not by reading the `WHERE` clause)
- [ ] `ORDER BY created_at DESC`
- [ ] Rejected orders appear, carrying `rejection_reason`; filled orders carry `filled_price`
- [ ] Empty history is `200` with an empty array, never `404` or `null`

**Verification:**
- [ ] `make test`, `make test-integration` green; cross-account isolation test passes

**Dependencies:** T9. **Scope:** S/M (3 files + tests).

---

### Task 12 — `GET /trading/positions`, and the fail-open read path

**Description:** Open positions with live P/L. **This is where the failure posture inverts** (§2.9) — the single easiest place in the step to violate the spec by reflex.

**Files:** `internal/store/postgres_trading_store.go`, `internal/service/trading.go`, `internal/handler/trading.go`, plus tests

**Acceptance criteria:**
- [ ] Returns positions with `quantity > 0` only
- [ ] `unrealized_pl = (latest_price − avg_cost) × quantity` when a price is available
- [ ] **`market-data` unreachable ⇒ `200` with `latest_price: null` and `unrealized_pl: 0`** — the endpoint does not fail, and the position does not vanish
- [ ] `latest_price` is a true JSON `null`, not `0` — a `*float64`, since `0` is a plausible price and would read as "worthless" rather than "unknown"
- [ ] One symbol failing to price does not blank out the others
- [ ] A service-layer test asserts the fail-open behaviour explicitly, with the fake `PriceClient` erroring

**Verification:**
- [ ] `make test`, `make vet` green
- [ ] **Manual:** stop `market-data`, call `GET /trading/positions` — `200` with nulls. Then call `POST /trading/orders` — `502`. **Both, in the same session**, because the two postures are what §2.3/§2.9 are about and verifying only one proves nothing about the split

**Dependencies:** T9. **Scope:** M (3 files + tests).

---

### Task 13 — `GET /trading/portfolio`

**Description:** The one-call rollup, so a future dashboard does not compose three requests.

**Files:** `internal/service/trading.go`, `internal/handler/trading.go`, plus tests

**Acceptance criteria:**
- [ ] `{"balance", "positions": [...], "total_equity", "total_unrealized_pl"}`
- [ ] `total_equity = balance + Σ(quantity × latest_price)`, falling back to `avg_cost` for any position with no live price (§2.9) — an unpriceable position is valued at cost, never dropped and never zero
- [ ] Reuses the positions code path rather than issuing a second, divergent query
- [ ] A test covers the mixed case: one priced position and one unpriceable, in the same response

**Verification:**
- [ ] `make test` green; manual `curl` against a portfolio with two positions

**Dependencies:** T12. **Scope:** S (2 files + tests).

### ⏸️ Checkpoint C — The API is complete
- [ ] All four endpoints work against the running service
- [ ] `make test`, `make test-integration`, `make vet` all green
- [ ] Fail-closed write / fail-open read demonstrated in one session with `market-data` stopped
- [ ] Dev DB `users=20, accounts=20`
- [ ] **Review with Khalil before proceeding**

---

## Phase 4 — Gateway, review, close-out

### Task 14 — Gateway-wide request body cap (security-backlog item 4)

**Description:** 64 KiB across **every** proxied route, replacing the login-only slice that has been carrying a comment pointing at this exact backlog item since Step 9.

**Files:** `services/gateway/internal/middleware/bodylimit.go`, `bodylimit_test.go`, `services/gateway/internal/handler/router.go`, `services/gateway/cmd/server/main.go`

**Design note, decided here:** reject early on `r.ContentLength > limit` with `413 payload_too_large` *and* wrap with `http.MaxBytesReader` for chunked bodies with unknown length. The wrap alone cannot produce a clean `413` — it surfaces as a copy error inside `ReverseProxy` and comes back as the proxy's `502`. The length check is what makes the common case a correct status; the wrapper is what makes the uncapped case actually capped.

Placement: inside `CORS` (a `413` without CORS headers reaches a browser as an opaque network error — the same reasoning that put `RequireAuth` and `RateLimitByIP` inside CORS), outside the rate limiters.

**Acceptance criteria:**
- [ ] Applies to every route: `/auth/*`, `/market-data/*`, `/trading/*` — proven per-prefix by tests, since "covers new services automatically" is the shape the backlog item asks for
- [ ] Over-limit ⇒ `413` with the standard `{"error":{"code","message"}}` shape via `httperr.Write`
- [ ] A chunked over-limit body is truncated rather than proxied whole
- [ ] `GET`/`HEAD` with no body are unaffected
- [ ] 64 KiB, matching what `auth` already applies per-request — so the gateway is **not stricter than the service it protects**, the rule `loginEmail`'s comment establishes
- [ ] `maxLoginBodyBytes` and `RateLimitLoginByAccount` still behave identically for bodies under the cap; `ratelimit_test.go:325`'s "an oversized body is the auth service's call" test still passes unchanged (it exercises the middleware directly, not the router)

**Verification:**
- [ ] `make test` green; `services/gateway` tests cover all three prefixes
- [ ] Manual: `curl` a 100 KiB body at `/auth/login` through the gateway → `413`, and confirm `auth` never logged the request

**Dependencies:** None (independent of the whole trading engine). **Scope:** M (4 files).

---

### Task 15 — Replace the gateway's `501` with a real proxy

**Files:** `services/gateway/internal/handler/router.go`, `router_test.go`, `services/gateway/cmd/server/main.go`, `.env.example`

**Acceptance criteria:**
- [ ] `/trading/*` proxies to `TRADING_ENGINE_SERVICE_URL` (default `http://localhost:8083`) via `proxy.New(..., "trading-engine")`, sharing the existing transport
- [ ] It stays inside the `RequireAuth` + `InjectUserID` group — unauthenticated requests still get `401` before reaching the backend
- [ ] `router_test.go:234`'s `not_implemented` assertion is **replaced**, not deleted: the new test asserts the request reaches a stub backend
- [ ] `router_test.go:147`'s "requires auth" case still covers `/trading/orders`
- [ ] `trading-engine` down ⇒ the proxy's `502 upstream_unavailable` — the same code its own price-fetch failure uses, which is coherent rather than confusing
- [ ] The boot log names all three backends

**Verification:**
- [ ] `make test`, `make vet` green
- [ ] **Manual, the full path:** log in through the gateway on `:8080`, place an order through `:8080/trading/orders`, confirm the fill — the first time a trade goes through the real edge
- [ ] Repeat with a stale/absent token → `401` from the gateway, and with `trading-engine` stopped → `502`

**Dependencies:** T13, T14. **Scope:** M (4 files).

### ⏸️ Checkpoint D — Wired to the edge
- [ ] Full stack up; a trade placed through the gateway lands in Postgres
- [ ] `make test`, `make test-integration`, `make vet` green
- [ ] **Review with Khalil before proceeding**

---

### Task 16 — Adversarial review before merge

**Description:** The project's standing practice, and `docs/NEXT_SESSION.md`'s explicit instruction for this step: green tests are not evidence. Run things; try to break them.

**Acceptance criteria — each is an attempt to break something, with the result written down:**
- [ ] **Re-run both T8 mutations** on the final code (they were run against an earlier version of the store)
- [ ] Remove `FOR UPDATE`, run the concurrency test 10× — record what actually happens to the balance, not just that a test went red
- [ ] Fire ~20 concurrent orders at one account through the running service (not the store directly) and assert the balance never goes negative and `Σ trades = starting − ending balance`
- [ ] Try to sell into a short: sell more than held, sell with no position, sell a symbol never bought
- [ ] Send `quantity` as `0`, negative, a huge float, a string, and `null`; send an unknown `side`; send an empty body
- [ ] Confirm a second user cannot read or trade against the first user's account by any endpoint — including by passing a forged `X-User-ID` header at the gateway (`StripUserID` should make this a non-event; confirm it *is*)
- [ ] Kill `market-data` mid-session and verify the write/read posture split once more on the final build
- [ ] `gofmt -l` across the new module — zero drift (the pre-existing `services/auth/internal/service` drift stays out of scope)

**Verification:**
- [ ] Findings written into `PHASE2_CHECKLIST.md`, including anything found *and fixed* — Step 13's write-up of its own pre-push finding is the template
- [ ] Dev DB `users=20, accounts=20`

**Dependencies:** T15. **Scope:** M (review; fixes as found).

---

### Task 17 — Documentation and close-out

**Files:** `PHASE2_CHECKLIST.md`, `docs/security-backlog.md`, `docs/TESTING_STRUCTURE.md`, `docs/deferred-tuning.md`, `docs/NEXT_SESSION.md`, `docs/archive/phase2-step14-trading-engine-mvp/{SPEC.md,plan.md,todo.md}`

**Acceptance criteria:**
- [ ] `PHASE2_CHECKLIST.md`: Step 14 written up, including T16's findings and the mutation-check results
- [ ] `docs/security-backlog.md`: item 4 **closed**, with what shipped; item 3 noted as next in line
- [ ] `docs/TESTING_STRUCTURE.md` §6a: the harness now exists in two modules — say so, say why it was copied rather than shared, and name the trigger for extracting it (a third service)
- [ ] `docs/deferred-tuning.md`: the `orders(account_id)` index (D4) with its trigger; the `float64` money representation with its trigger
- [ ] `docs/NEXT_SESSION.md` **rewritten**, not appended: dev DB counts, migration version 6, what Step 15 should be (the trading frontend, per §1's non-goals)
- [ ] Spec, plan, and todo archived under `docs/archive/phase2-step14-trading-engine-mvp/`

**Verification:**
- [ ] `make test`, `make test-integration`, `make vet` green on the final commit
- [ ] Branch merged to `main` (branch-per-step, resumed after Step 13's recorded one-off exception)

**Dependencies:** T16. **Scope:** M (6 files, docs only).

### ⏸️ Checkpoint E — Step 14 complete
- [ ] All acceptance criteria met; all four endpoints work through the gateway
- [ ] `make test` green with Docker down; `make test-integration` green with it up; `make vet` clean
- [ ] Dev DB `users=20, accounts=20`; migrations at version 6
- [ ] **Ready for merge review**

---

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| `FOR UPDATE` is written but does not actually serialize (wrong statement, wrong isolation, read-then-lock) | **High** — silent double-spend, the exact bug the step is built around | T8's concurrency test, plus the mandatory mutation check. The lock is not trusted because it reads correctly |
| The harness copy drops the `protectedDatabases` denylist during the port to a new module | **High** — the dev database has 20 real users and the harness runs `DROP DATABASE` | T7 gates T8; guards are re-proven in their new home, including a hand-forced `TEST_DATABASE_URL=postgres` refusal |
| Fail-open/fail-closed reversed between write and read paths | **High** — a fill at a guessed price is an integrity violation; `NEXT_SESSION.md` calls this the easiest way to violate the spec | Separate acceptance criteria in T4 and T12, plus one manual session that exercises both with `market-data` stopped |
| A rejected order is rolled back along with the transaction | Medium — loses the audit trail §2.5 exists for | D1 decided up front; T8 asserts the row survives; second mutation check covers it |
| `float64` rounding on `avg_cost` / balances | Medium | Postgres `NUMERIC(20,4)` stays authoritative; integration assertions read money as `::text` (Step 12 precedent); tolerance-based float comparisons in unit tests. Representation change is out of scope (§6) |
| Two services writing `accounts.balance` | Medium — first time in the project | `auth` writes it only at registration; `trading-engine` only inside the locked transaction. Named in T6's comments and in `PHASE2_CHECKLIST.md` so it is not rediscovered |
| A failed migration leaves the dev schema dirty | Medium | `make migrate-force VERSION=5`, fix, re-run. The test database is rebuilt from `001` every run, so it is unaffected |
| The new gateway body cap breaks login proxying | Low/Medium | 64 KiB matches what `auth` already enforces, so the gateway is not stricter than the service behind it; T14 re-runs the existing login rate-limit suite unchanged |
| Scope creep into the frontend | Low | §1 non-goals and `SPEC.md`'s "Never" boundary. The trading UI is its own step |

## Decisions resolved before implementation

Resolved 2026-08-17, all as recommended — the same form `SPEC.md` §8 uses.

| # | Decision | Resolution |
|---|---|---|
| D1 | A rejected order's fate | **Business rejections `COMMIT`** the order row; only infrastructure failures `ROLLBACK` |
| D2 | Where balance/holding validation lives | **In the store, inside the lock.** The service keeps everything checkable without one |
| D3 | Authenticated user with no account row | **`500 internal_error`** — a broken invariant, not a client error |
| D4 | An `orders(account_id)` index | **Deferred**, with a trigger recorded in `docs/deferred-tuning.md` |
| — | T7's harness | **Copied** into `services/trading-engine/integration`, not extracted to `pkg/`. Revisit at a third service |

**Remaining note, not a question:** 17 tasks against a 3–5 hr/week budget is a multi-session step. Checkpoints A–E are the natural session boundaries; Phase 2 (T3–T9) is the largest and least divisible block.
