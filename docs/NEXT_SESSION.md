# Next session — state of play

Last updated **2026-08-17**, at the close of Step 14 (trading engine MVP).

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Step 14 is merged

| | |
|---|---|
| Merge | **Done.** `step14-trading-engine-mvp` merged to `main` at `ce526a7`, one commit per task, and pushed to `origin/main`. The merge branch is deleted, locally and on the remote. |
| Migrations | version **6**. `006_trading_cost_basis_and_order_audit` added `positions.avg_cost`, `orders.filled_price`, `orders.rejection_reason`, `trades.realized_pl`. |
| `services/trading-engine` | a real fourth Go module — in `go.work`, in the Makefile's `GO_MODULES`, with `run-trading-engine`, and its own `integration/` suite wired into `test-integration` and `vet`. Listens on **:8083**. |
| Tests | `make test` **13 packages ok** (no Docker needed); `make test-integration` **43 PASS / 0 FAIL / 0 SKIP** with Docker up; `make vet` clean |
| Dev database | `users=20`, `accounts=20`, and the trading tables **empty** — `orders=0`, `trades=0`, `positions=0` |
| Local branches | `main` (current) |

`docs/archive/phase2-step14-trading-engine-mvp/{SPEC.md,plan.md,todo.md}` hold this step's spec, plan and todo.

### The trading tables are empty, and that is deliberate

Verifying this step meant registering real users and placing real orders through the real edge, so three sessions of end-to-end checks left four throwaway accounts (`step14manual`, `step14gateway`, `step14adva`, `step14advb`) behind — including **31 `quantity = 0, status = filled` rows**, the artifact of the money-minting bug the adversarial review found and fixed.

Those four accounts owned *every* order, trade and position in the dev database — the twenty pre-existing users have never traded. They were deleted before the merge, which returns the database to `users=20, accounts=20` and the plan's Checkpoint E criterion to what it always said, at the cost of leaving the trading tables with nothing in them.

**So an empty `orders` table is the expected state, not a sign something failed to write.** The first real order placed after this step is the first row.

---

## What Step 14 shipped

`POST /trading/orders` executes a market buy or sell synchronously: it fetches the live price from `market-data` over HTTP, then hands everything to a store that opens one transaction holding `SELECT ... FOR UPDATE` on the account row for its whole duration, validates funds or holdings inside that lock, and writes the order, the trade, the position and the balance together. `GET /trading/orders`, `/positions` and `/portfolio` expose the result, with weighted-average cost basis, realized P/L captured at execution time, and unrealized P/L priced live.

The gateway's `/trading/*` `501` placeholder is gone, replaced by a real proxy to `TRADING_ENGINE_SERVICE_URL`. **`docs/security-backlog.md` item 4 is closed**: a gateway-wide 64 KiB body cap on every route, open since Step 9.

**Backend only.** No frontend work, per `SPEC.md` §1's non-goals — the same shape as Step 11, which shipped the whole auth backend before Step 13 was the first step to touch `frontend/`.

---

## What to do next

**1. Step 15: the trading frontend.** This is the obvious next step and the one the engine was built to be built against — `SPEC.md` §1 listed the UI as a non-goal specifically so it could be its own step. The API it consumes is stable and documented; four endpoints, all under `/trading/*` through the gateway on `:8080`:

| endpoint | shape |
|---|---|
| `POST /trading/orders` | `{symbol, side, quantity}` → `201` with the order, the trade and the new balance |
| `GET /trading/orders` | `{"orders": [...]}` newest first, **rejections included** |
| `GET /trading/positions` | `{"positions": [...]}` with `latest_price` and `unrealized_pl` |
| `GET /trading/portfolio` | cash, positions, `total_equity`, `total_unrealized_pl` in one call |

Two things the UI has to get right, both of which the backend deliberately made visible rather than hiding:

- **`latest_price` is nullable and `null` is meaningful.** It means market-data could not price that holding right now — the position is real and is valued at cost in the totals. Rendering `null` as `0`, or dropping the row, reports the user as having lost everything they hold because one HTTP call failed. There is a test on both the service and the handler pinning this; do not let the frontend undo it.
- **Rejected orders are part of the history, not errors to hide.** Each carries a `rejection_reason` (`insufficient_balance`, `insufficient_position`, `symbol_unavailable`, `upstream_unavailable`). An order list that shows only fills would look complete while being wrong.

**2. The two long-standing small items**, both still open and both still lower priority:

- `market-data`'s store has no tests (`historical_price_store.go`). The harness now exists in **two** modules; a third use is the recorded trigger for extracting it to `pkg/testutil/` — see `docs/TESTING_STRUCTURE.md` §6a.
- Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`, untouched since Step 11. Worth a one-line cleanup commit before any `fmt` check lands in CI.

**3. Security backlog:** items 1, 2 and 4 are closed. Item **8** (Unicode-normalise passwords) is the cheap one left from the Phase 2 set and gets more expensive as real accounts accumulate. Item **3** (Argon2id) is the next substantive one and wants its own step, since it carries a migration strategy.

---

## Restarting the environment

```bash
make docker-up            # Postgres + Redis
make run-auth             # :8081
make run-market-data      # :8082
make run-trading-engine   # :8083
make run-gateway          # :8080
make run-frontend         # :5173
```

Each `run-*` target runs in the foreground, so they need separate terminals. **This step is the first that needs four backend services running at once** — the trading engine calls market-data directly for the fill price, and a stopped market-data turns every order into a `502` (correctly, but confusingly if you forgot to start it).

`make help` lists the test targets too.

Auth rate limiting is **on by default** (100 requests / 15 min per IP; backoff after 5 consecutive failed logins). `RATE_LIMIT_ENABLED=false` turns it off if it gets in the way during development.

`services/auth` requires `REDIS_URL` to boot (`log.Fatal` if unset). `services/trading-engine` needs no new variables of its own — `DATABASE_URL`, `JWT_SECRET`, `BIND_ADDR` and `MARKET_DATA_SERVICE_URL` are all shared. **Do not put `PORT=8083` in `.env`**: the Makefile exports that file to every target, so it would move all four services onto the same port. Override per process instead.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty database named `quantsim` also exists. `psql -d quantsim` connects successfully and shows no `users` table, which reads like data loss and is not. The user is **`quantsim`** and the database is **`postgres`**:

```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT count(*) FROM users"     # 20, as of this session
```

**Money is `float64` in Go and `NUMERIC(20,4)` in Postgres, and Postgres is the authority.** Read money as `::text` in tests — scanning straight into a `float64` lets a value that lost precision on the way in come back looking exactly like the number you expected. `total_equity` can legitimately come back as `99999.99999999999`; `docs/deferred-tuning.md` §10 has the measured numbers and the trigger for fixing it properly.

**Order quantities have a floor of `0.0001`, and it is load-bearing.** It is the ledger's own tick. Anything smaller was charged for in full and then rounded to zero shares, which minted money on the sell side — the bug this step's adversarial review found. Do not relax that check without reading `PHASE2_CHECKLIST.md` Step 14 first.

**The write path fails closed; the read path fails open.** No price means no fill (`502`, order persisted as rejected). No price on a read means `latest_price: null` and still a `200`. Reversing these is the single easiest way to violate the spec's intent, and there are tests on both.

**`migrate` lives at `~/go/bin/migrate` and is not on a non-interactive shell's PATH.** Use `make migrate-up` from an interactive shell, or the full path. The integration harness execs the `.up.sql` files directly instead — `docs/deferred-tuning.md` §7.

**A failed migration leaves the schema dirty.** Recovery is `make migrate-force VERSION=<n>` at the last good version, then fix the cause and re-run. Dev database only — the test database is recreated from scratch every run.

**Restart a service after changing its code.** Everything runs under `go run`, so a live instance keeps serving the old binary. Killing the `go run` wrapper alone may not release the port — check `lsof -i :<port>` and kill the actual server binary too if it's still held. With four services up this is easy to get wrong; building to a temporary binary and running that is less error-prone than `go run` for a long verification session.

**A green `go test ./...` says nothing about Redis or Postgres.** `make test-integration` covers both, on independent skip paths. `make vet` includes a `-tags=integration` pass so a tagged suite cannot rot invisibly.

**The integration harness now exists in two copies** (`services/auth/integration/`, `services/trading-engine/integration/`). The guard machinery is byte-identical on purpose. **Change one, change both, and `diff` them** — `docs/TESTING_STRUCTURE.md` §6a explains why it was copied rather than shared, and what triggers extracting it.

**Rate-limit counters are per-process.** Correct while one gateway runs; a second instance doubles the effective limit — `docs/deferred-tuning.md` §4–§5.

**`gofmt` reports drift in `services/auth/internal/service/interfaces.go` and `types.go`.** Pre-existing, deliberately left alone since Step 11.

---

## Where things are written down

| | |
|---|---|
| `agents.md` | master context, working agreement, architecture |
| `PHASE1_CHECKLIST.md` | Phase 1, all 9 steps + Step 10 — **closed** |
| `PHASE2_CHECKLIST.md` | Phase 2 — Steps 11–14 written up, including Step 14's review findings and mutation checks |
| `SPEC.md` | the current step's spec — **Step 14's is archived; there is no active spec until Step 15 is drafted** |
| `tasks/plan.md`, `tasks/todo.md` | archived with Step 14; recreated when the next step is planned |
| `docs/TESTING_STRUCTURE.md` | test layout; §6a is the integration-test guide |
| `docs/security-backlog.md` | 8 known gaps — items 1, 2 and 4 **closed**; item 8 cheapest next, item 3 the next substantive one |
| `docs/deferred-tuning.md` | deferred decisions with triggers; §9 and §10 are Step 14's |
| `docs/archive/phase*/` | every completed step's spec, plan, and todo |
| `docs/intent/quantsim-resume.md` | why the workflow changed in July 2026 |
