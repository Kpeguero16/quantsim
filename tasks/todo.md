# Trading Engine MVP — Task Checklist (Step 14)

> **`SPEC.md` is APPROVED** — all eleven design decisions resolved 2026-08-17, every
> one as recommended (§8). **Implementation is unblocked.**
>
> `agents.md` §2's Simulated Trading Engine — the last major system before Phase 3.
> Backend only; the trading UI is its own step.

Full detail (acceptance criteria, verification, dependency graph, risks, the four
decisions the spec left open) in `tasks/plan.md`.

Branch: `step14-trading-engine-mvp`. Branch-per-step is back in effect after Step 13's
recorded one-off exception.

Each checkpoint is a stop-for-review point per `agents.md`: implement, verify, **stop**.

---

## ⚠️ The three things that will go wrong if they go wrong quietly

**1. `FOR UPDATE` that does not actually serialize.** The whole step is built around
"never spend money you don't have." A read-then-lock, or a missing `FOR UPDATE`, reads
perfectly correct and double-spends under concurrency. **Task 8's mutation check is not
optional** — delete the lock, confirm the concurrency test goes red, put it back.

**2. Fail-open and fail-closed reversed.** Writes fail **closed** (no price ⇒ no fill,
`502`). Reads fail **open** (no price ⇒ `latest_price: null`, still `200`). Getting these
backwards is the single easiest way to violate the spec's intent. Verify **both in one
session** with `market-data` stopped.

**3. The integration harness runs `DROP DATABASE`.** The dev database holds real rows and
the environment misleads: `POSTGRES_DB=quantsim` is an **empty decoy**, `DATABASE_URL`
points at **`postgres`**, where the rows live. Copy `assertTestDB` intact — denylist
*and* exact match. Do not reduce it to one comparison.

**Baseline measured 2026-08-17 — `users=20`, `accounts=20`.** Re-check at every checkpoint:

```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT 'users=' || count(*) FROM users"
```

---

### Phase 1: Foundation
- [ ] **Task 1** — Migration `006`: `positions.avg_cost` (NOT NULL DEFAULT 0), `orders.filled_price`, `orders.rejection_reason`, `trades.realized_pl` (all nullable). Real `.down.sql`, verified by down-then-up

- [ ] **Task 2** — Module scaffold: `go.mod` (chi, pgx, uuid, `replace ../../pkg` — **no new project dependency**), `go.work`, `cmd/server` with `/healthz`, `handler/errors.go` copied from `market-data`, `Makefile` (`GO_MODULES`, `run-trading-engine`), `.env.example`. No `REDIS_URL`

- [ ] ⏸️ **Checkpoint A: Foundation** — 4 modules build/test/vet clean, service answers `/healthz`, migration reversible, dev DB at 20

### Phase 2: The first vertical slice — a market buy, end to end
- [ ] **Task 3** — Service contract only, no behaviour: `types.go`, `interfaces.go` (`AccountStore`, `TradingStore`, `PriceClient`), `errors.go` (each error's doc comment names its HTTP status), `mock/` with compile-time interface assertions

- [ ] **Task 4** — `PlaceOrder` buy path + unit tests. Price-fetch failure **persists a rejected order and returns the error** — never a fill at a guessed price. Weighted-avg asserted with real numbers (10@100 then 10@120 ⇒ 110)

- [ ] **Task 5** — `PriceClient` over HTTP to `market-data`. `404` → `symbol_unavailable`; everything else → `upstream_unavailable`; explicit client timeout (this is on the synchronous order path). `httptest.Server` only — no real service required

- [ ] **Task 6** — 🔴 **The transaction.** `SELECT ... FOR UPDATE` on the account row, validate *inside* the lock, upsert position with the weighted-avg formula, insert order + trade, update balance. Business rejections **COMMIT**; only infrastructure errors **ROLLBACK**. Compile-time `var _ service.TradingStore = ...`

- [ ] **Task 7** — 🔴 **Harness + guard proof, before anything destructive runs.** Copy Step 12's harness into `services/trading-engine/integration` (Postgres only, no Redis). Prove `assertTestDB` rejects `postgres`, `quantsim`, `""`. Force `TEST_DATABASE_URL` at `postgres` by hand and confirm it **refuses**. Extend `test-integration` and `vet`'s tagged pass — alongside auth's, not replacing it

- [ ] **Task 8** — Store integration tests **including the concurrency proof**: balance 1000, two goroutines from a common barrier each buy 1 @ 600 → exactly one fills, one gets `insufficient_balance`, balance is exactly 400, exactly one trade row. **Mutation check: remove `FOR UPDATE`, confirm it fails.** Second mutation: rejection path rolls back instead of committing → the persisted-rejection test must fail

- [ ] **Task 9** — `POST /trading/orders`: handler + router (`RequireAuth` on the service's own routes, §2.11 — never trust `X-User-ID`) + `main.go` wiring. §2.9's exact status/code table. **Manual: place a real order at `:8083` and see the rows in `psql`**

- [ ] ⏸️ **Checkpoint B: A buy works end to end** — both mutations run and reverted, manual fill verified, `market-data`-down rejection verified, dev DB at 20

### Phase 3: Sell, and the read endpoints
- [ ] **Task 10** — Sell path. `avg_cost` is **read, never recomputed**; `realized_pl = (price − avg_cost) × qty` captured at execution. Test: buy 100, buy 200, sell, buy again — the earlier sell's stored `realized_pl` must be unchanged. Position at qty 0 keeps its row

- [ ] **Task 11** — `GET /trading/orders`, newest first, **rejected orders included**. Cross-account isolation proven by a two-account test, not by reading the `WHERE` clause

- [ ] **Task 12** — 🔴 `GET /trading/positions`, **the fail-open path**. `market-data` down ⇒ `200`, `latest_price: null` (a `*float64` — `0` is a plausible price and would read as "worthless"), `unrealized_pl: 0`. One unpriceable symbol must not blank the others

- [ ] **Task 13** — `GET /trading/portfolio` rollup. Unpriceable positions valued at `avg_cost` — never dropped, never zeroed. Reuses the positions path rather than a second divergent query

- [ ] ⏸️ **Checkpoint C: The API is complete** — all four endpoints work; write-closed/read-open demonstrated **in the same session**; dev DB at 20

### Phase 4: Gateway, review, close-out
- [ ] **Task 14** — Gateway-wide 64 KiB body cap (`security-backlog` item 4, waiting since Step 9). `ContentLength` check → `413 payload_too_large`, plus `MaxBytesReader` for chunked bodies. Inside CORS. Covers `/auth/*`, `/market-data/*`, `/trading/*` — tested per prefix. Independent of everything else in this step

- [ ] **Task 15** — Replace the gateway's `501` with a real proxy to `TRADING_ENGINE_SERVICE_URL` (`:8083`), inside the existing auth group. `router_test.go:234`'s `not_implemented` assertion gets **replaced, not deleted**. **Manual: trade through `:8080` for the first time**

- [ ] ⏸️ **Checkpoint D: Wired to the edge** — a trade placed through the gateway lands in Postgres

- [ ] **Task 16** — 🔴 **Adversarial review.** Green tests are not evidence. Re-run both mutations on final code; 20 concurrent orders against one account (balance never negative); sell into a short; garbage quantities and sides; forged `X-User-ID` at the gateway; kill `market-data` mid-session. Write findings down, including ones found *and* fixed

- [ ] **Task 17** — Close-out: Step 14 in `PHASE2_CHECKLIST.md` with the mutation results; `security-backlog` item 4 **closed**; `TESTING_STRUCTURE.md` §6a on the now-duplicated harness and the trigger for extracting it; `deferred-tuning.md` entries for the `orders(account_id)` index and the `float64` money representation; **rewrite** `NEXT_SESSION.md`; archive spec/plan/todo

- [ ] ⏸️ **Checkpoint E: Step 14 complete** — `make test` green with Docker down, `make test-integration` green with it up, `make vet` clean, migrations at version 6, dev DB at 20. Ready to merge to `main`

---

## Decisions this plan makes that the spec did not

**Resolved 2026-08-17 — all four accepted as recommended.** Reasoning in
`tasks/plan.md` "Four things the spec leaves for the plan to decide":

1. **A rejected order COMMITs; it does not roll back.** §2.3 says "rollback, reject";
   §2.5 says rejected orders are persisted. A rollback would erase the row §2.5 wants
2. **Validation lives in the store, not the service** — it has to read the balance
   inside the lock, and the service cannot hold a transaction without leaking `pgx`
   across the layer boundary
3. **A user with no account row is `500`**, not a new 4xx code that should be unreachable
4. **No `orders(account_id)` index in 006** — beyond the scoped migration, which is an
   "ask first" boundary. Defer with a recorded trigger

---

## Reminders that have cost time before

**`DATABASE_URL` points at `postgres`, not `quantsim`.** `psql -d quantsim` connects
fine and shows no `users` table, which reads like data loss and is not. The user is
`quantsim`; the database is `postgres`.

**Restart a service after changing its code.** Everything runs under `go run`, so a live
instance keeps serving the old binary. Killing the wrapper may not release the port —
check `lsof -i :8083` and kill the server binary too. **This step runs four services
at once**, which makes it more likely than usual, not less.

**A green `go test ./...` says nothing about any SQL.** The store lives behind the
`integration` tag. Until Task 8 lands, nothing in this module has touched Postgres.

**`make vet` includes a `-tags=integration` pass**, and Task 7 must extend it. Files
behind a build tag are type-checked by no default command — which is exactly how a
tagged suite rots unnoticed until someone finally runs it.

**Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`**
is untouched across Steps 11–13 and stays out of scope here. The **new** module must
have zero drift.
