# Next session — state of play

Last updated **2026-08-17**, right after Step 14's spec was approved and pushed.

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Step 14 is spec'd, approved, and pushed — nothing else has been built yet

| | |
|---|---|
| Branch | `step14-trading-engine-mvp`, pushed to `origin`, tracking. `main` is untouched at `75d050b` (Step 13). |
| Process | **Branch-per-step is back in effect.** Step 13 was recorded as a deliberate one-off exception (built directly on `main`); this step lives entirely on its own branch, the way Steps 11 and 12 did. |
| Spec | `SPEC.md` at repo root: **Approved 2026-08-17**, all eleven design decisions resolved as recommended (§8). Nothing has been implemented against it. |
| Plan | **Not started.** No `tasks/plan.md` or `tasks/todo.md` exist yet. Khalil is deliberately switching to a different model for the plan and implementation phases — do not assume the person picking this up next is continuing the same conversation. |
| Migrations | still at version **5**, unchanged. The spec's migration 006 (`positions.avg_cost`, `orders.filled_price`/`rejection_reason`, `trades.realized_pl`) is designed but not written. |
| `services/trading-engine` | still the empty `go.mod` stub it has always been. Not in `go.work` yet. |
| Tests | unchanged since Step 13: `make test` 10 packages ok; `make test-integration` 18 PASS / 0 SKIP |
| Dev database | unchanged: `users=20`, `accounts=20` |
| Local branches | `main`, `step14-trading-engine-mvp` (current) |

`docs/archive/phase2-step13-refresh-token-revocation/{SPEC.md,plan.md,todo.md}` hold Step 13's spec, archived when Step 14's was drafted, per convention.

---

## What the Step 14 spec covers

`agents.md` §2's "Simulated Trading Engine" — the last major system before Phase 3. `POST /trading/orders` executes a market buy/sell synchronously against `market-data`'s live price, validates funds/holdings, updates balance and position atomically, records the trade. `GET /trading/orders`, `/positions`, `/portfolio` expose the result, including realized and unrealized P/L. The gateway's `/trading/*` `501` placeholder gets replaced, and a gateway-wide request-body cap (`docs/security-backlog.md` item 4) lands with it.

**Explicitly out of scope, per the spec's non-goals:** limit/stop-loss/take-profit orders, short-selling, multi-account-per-user, async/queued execution, and — deliberately — **any frontend work**. This step is backend-only, the same way Step 11 shipped the whole auth backend before Step 13 was the first to touch `frontend/`.

### What to know before picking this up

- **The write path fails closed; the read path fails open.** If `market-data` is unreachable, `POST /trading/orders` rejects the order (`502 upstream_unavailable`) rather than filling at a guessed price. `GET /trading/positions`/`/portfolio` do the opposite — they degrade to `latest_price: null` rather than erroring, because a missing live quote on a read isn't the integrity violation a blind fill would be. Getting this reversed is the single easiest way to violate the spec's intent. See `SPEC.md` §2.3 and §2.9.
- **`trading-engine` will write to `accounts.balance`, a table `auth` also writes.** This is deliberate (§2.4), not an oversight — but it's the first cross-service table write in the project and worth remembering before touching either service's account logic.
- **Order execution needs `SELECT ... FOR UPDATE` on the account row for the whole transaction**, not just balance validation followed by a separate update — §2.6 explains why (concurrent orders on the same account must not double-spend). The spec calls for a dedicated concurrency integration test proving this, not just trusting the code looks right.
- **Rejected orders get persisted**, not just filled ones (`orders.rejection_reason`, migration 006) — an empty order history minus its failures would undercut the audit-trail realism the spec is optimizing for.

---

## What to do next

1. `git checkout step14-trading-engine-mvp` (already pushed, tracking `origin`).
2. Generate `tasks/plan.md` and `tasks/todo.md` from `SPEC.md` (the `/plan` skill, or equivalent) — the gated workflow is spec → plan → checkpoints, and the plan step hasn't happened yet.
3. Implement to checkpoints, one commit per task, following `SPEC.md` §5's testing strategy — the store-layer integration tests (including the concurrency test) are load-bearing here, not optional polish, because §2.6's locking claim can't be verified by a mock.
4. Before merging to `main`: adversarial review per the standing practice (green tests aren't evidence — run things, and specifically try to break the `FOR UPDATE` locking and the fail-closed/fail-open split before trusting either).

No other Phase 2 item is queued ahead of this. The two smaller items noted at the end of Step 13's handoff are still open and still lower priority than the engine:

- `market-data`'s store has no tests (`historical_price_store.go`) — Step 12's harness is the template.
- Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}` — untouched since Steps 11–13, still worth a one-line cleanup commit before any `fmt` check lands in CI.

---

## Restarting the environment

```bash
make docker-up        # Postgres + Redis
make run-auth         # :8081
make run-gateway      # :8080
make run-frontend     # :5173
```

Each `run-*` target runs in the foreground, so they need separate terminals. `services/trading-engine` has no `run-trading-engine` target yet — the spec calls for adding one (§7) once the service exists to run.

`make help` lists the test targets too.

Auth rate limiting is **on by default** (100 requests / 15 min per IP; backoff after 5 consecutive failed logins). `RATE_LIMIT_ENABLED=false` turns it off if it gets in the way during development.

`services/auth` requires `REDIS_URL` to boot (`log.Fatal` if unset) — see Step 13's entry in `PHASE2_CHECKLIST.md` if the reason isn't obvious.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty database named `quantsim` also exists. `psql -d quantsim` connects successfully and shows no `users` table, which reads like data loss and is not. The user is **`quantsim`** and the database is **`postgres`**:

```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT count(*) FROM users"     # 20, as of this session
```

**`migrate` lives at `~/go/bin/migrate` and is not on a non-interactive shell's PATH.** Use `make migrate-up` from an interactive shell, or the full path. The integration harness execs the `.up.sql` files directly instead — `docs/deferred-tuning.md` §7.

**A failed migration leaves the schema dirty.** Recovery is `make migrate-force VERSION=<n>` at the last good version, then fix the cause and re-run. Dev database only — the test database is recreated from scratch every run.

**Restart a service after changing its code.** Everything runs under `go run`, so a live instance keeps serving the old binary. Killing the `go run` wrapper alone may not release the port — check `lsof -i :<port>` and kill the actual server binary too if it's still held.

**A green `go test ./...` says nothing about Redis or Postgres.** `make test-integration` covers both, on independent skip paths. `make vet` includes a `-tags=integration` pass so a tagged suite can't rot invisibly — when `services/trading-engine/integration` exists, it needs to be added to both `test-integration` and `vet` in the `Makefile`, per `SPEC.md` §4.

**Rate-limit counters are per-process.** Correct while one gateway runs; a second instance doubles the effective limit — `docs/deferred-tuning.md` §4–§5.

**`gofmt` reports drift in `services/auth/internal/service/interfaces.go` and `types.go`.** Pre-existing, deliberately left alone across Steps 11–13. Still worth a one-line cleanup commit before any `fmt` check lands in CI.

---

## Where things are written down

| | |
|---|---|
| `agents.md` | master context, working agreement, architecture |
| `PHASE1_CHECKLIST.md` | Phase 1, all 9 steps + Step 10 — **closed** |
| `PHASE2_CHECKLIST.md` | Phase 2 — Steps 11–13 written up; Step 14 not yet added (add it once the step closes) |
| `SPEC.md` | the current step's spec (Step 14) — **approved**, plan/implementation not started |
| `tasks/plan.md`, `tasks/todo.md` | do not exist yet — created when planning begins |
| `docs/TESTING_STRUCTURE.md` | test layout; §6a is the integration-test guide |
| `docs/security-backlog.md` | 8 known gaps — items 1 and 2 closed; item 4 (gateway body cap) closes with this step; item 3 next in line after that, not urgent |
| `docs/deferred-tuning.md` | deferred decisions with triggers |
| `docs/archive/phase*/` | every completed step's spec, plan, and todo |
| `docs/intent/quantsim-resume.md` | why the workflow changed in July 2026 |
