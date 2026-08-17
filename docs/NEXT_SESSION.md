# Next session — state of play

Last updated **2026-08-17**, at the end of the session that shipped Step 13.

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Nothing is in flight

Step 13 is committed and pushed to `main`. There is no work to recover.

| | |
|---|---|
| Branch | `main`, clean, in sync with `origin/main` |
| Process | **Departure from Steps 11/12:** built and committed directly on `main`, five commits, no feature branch. There is nothing to merge and no branch to delete. If a branch-per-step process is wanted going forward, say so explicitly next session — nothing here restores it automatically. |
| Migrations | schema at version **5**, unchanged — Step 13 added none |
| Tests | `make test` 10 packages ok; `make test-integration` **18 PASS / 0 SKIP** (15 from Step 12 + 3 new Redis tests) |
| Dev database | `users=19`, `accounts=19` — **up from Step 12's baseline of 15.** Two of those four are this session's own manual verification (`e2e-*@test.com`, `browser-e2e@test.com`, both real `POST /auth/register` calls against the live dev stack); the other two are unaccounted for by this session and are presumably Khalil's own testing between 2026-08-14 and now. Left in place — deleting rows from the dev database is not a step to take casually, and none of it is destructive to anything real. |
| Local branches | only `main` |

`SPEC.md`, `tasks/plan.md`, and `tasks/todo.md` describe **Step 13, fully
checked off**. By convention they are archived to
`docs/archive/phase2-step13-refresh-token-revocation/` when the *next* spec is
drafted, not before.

---

## What Step 13 did

Closed `docs/security-backlog.md` item 2 — refresh tokens lived 7 days with no
server-side kill switch, and "sign out" only cleared local state.

`POST /auth/logout` now revokes the presented refresh token; `Refresh` rejects
a revoked one before issuing a new pair. Every token (access and refresh
alike) carries a `jti`; only refresh tokens are ever checked against the
revocation store. Redis-backed (`services/auth`'s first Redis dependency),
denylist rather than rotation-with-reuse-detection — full reasoning in
`SPEC.md` §2.1 and the upgrade path with its trigger in
`docs/deferred-tuning.md` §8.

**Run the new tests:**

```bash
make docker-up
make test-integration
```

The three new Redis tests run on logical DB 15, independent of the existing
Postgres skip path — Redis down skips just those three, Postgres down skips
the rest, and either can be down without affecting the other.

### Three things to know before touching this code

**Access tokens are not revoked, only refresh tokens.** A stolen access token
is valid for its full 15-minute life regardless of a logout call. This is a
deliberate, documented residual risk (`docs/security-backlog.md` item 2,
"Residual risk, accepted"), not a gap — revoking access tokens would mean
pulling a revocation check into `pkg/auth.RequireAuth`, which the gateway also
calls and which is explicitly documented as having no dependency on any
service's internals. Don't add that check there without re-reading `SPEC.md`
§2.4 first.

**Both revocation paths fail open on a Redis error, by design.** If Redis is
unreachable, `Refresh` treats "can't confirm revoked" as "not revoked," and
`Logout` returns success either way. This is not a bug to "fix" by making it
fail closed — fail-closed would turn any Redis hiccup into a second way to log
out every active session every 15 minutes, which is a worse failure mode than
the bounded, self-healing window fail-open accepts. Both paths `log.Printf`
so an outage stays visible. See `SPEC.md` §2.3.

**`Logout` returns `200 {}`, never `204`.** `frontend/src/api/client.ts`'s
generic `request<T>` helper calls `response.json()` unconditionally on
success — a `204` has no body and that call throws. This was caught before it
shipped (`SPEC.md` §2.5); don't "clean up" the response shape to a 204 later
without checking that helper first.

---

## What to do next

### Phase 2 proper — the trading engine

The only item left before the trading engine (refresh-token revocation) is
now closed. Order execution, trade history, position tracking, P/L. Per
`agents.md`, start with a spec, get it reviewed, then build to checkpoints.

`/trading/*` currently returns `501` from a placeholder in the gateway
(`services/gateway/internal/handler/router.go`) — replacing that is also the
natural moment to add the gateway-wide request-body cap
`docs/security-backlog.md` item 4 has been waiting on.

Two smaller items are also open, lower priority than the engine itself:

- **market-data's store has the same gap Step 12 closed for auth** —
  `historical_price_store.go` has no tests, and its idempotent upsert
  (`UNIQUE(symbol, timeframe, timestamp)`) is exactly the kind of SQL worth
  covering. Step 12's harness is the template.
- **Pre-existing `gofmt` drift** in `services/auth/internal/service/`
  (`interfaces.go`, `types.go`). Step 13 added to `interfaces.go` but matched
  its existing unformatted style rather than fixing it as a drive-by — still
  worth a one-line cleanup commit before any `fmt` check lands anywhere.

---

## Restarting the environment

```bash
make docker-up        # Postgres + Redis
make run-auth         # :8081
make run-gateway      # :8080
make run-frontend     # :5173
```

Each `run-*` target runs in the foreground, so they need separate terminals.

`make help` lists the test targets too.

Auth rate limiting is **on by default** (100 requests / 15 min per IP; backoff
after 5 consecutive failed logins). `RATE_LIMIT_ENABLED=false` turns it off if
it gets in the way during development — the gateway logs a warning at boot
whenever it is off.

`services/auth` now requires `REDIS_URL` to boot (`log.Fatal` if unset) —
not a new variable, `.env.example` already has it for `market-data`, but
`make run-auth` will fail loudly if Redis isn't reachable at all, distinct
from the fail-open behavior for a Redis that drops mid-session.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty
database named `quantsim` also exists. `psql -d quantsim` connects successfully
and shows no `users` table, which reads like data loss and is not. The user is
**`quantsim`** and the database is **`postgres`** — that combination catches
people out:

```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT count(*) FROM users"     # 19, as of this session
```

**`migrate` lives at `~/go/bin/migrate` and is not on a non-interactive
shell's PATH.** Use `make migrate-up` from an interactive shell, or the full
path. The integration harness does not use it — it execs the `.up.sql` files
directly, recorded in `docs/deferred-tuning.md` §7.

**A failed migration leaves the schema dirty.** Recovery is
`make migrate-force VERSION=<n>` at the last good version, then fix the cause
and re-run. This applies to the dev database only; the test database is
recreated from scratch on every run and cannot be left dirty.

**Restart a service after changing its code.** Everything runs under `go run`,
so a live instance keeps serving the old binary. `services/auth`'s binary
specifically needs a restart after any change to `main.go`, `Service`, or the
handler layer — Step 13 changed all three. Killing the `go run` wrapper alone
may not be enough; check `lsof -i :8081` and kill the actual server binary too
if the port is still held.

**A green `go test ./...` still says nothing about Redis or Postgres.**
`make test-integration` covers both now, on independent skip paths. The
tagged suite is invisible to default tooling — `make vet` includes a
`-tags=integration` pass so it cannot rot silently; keep it that way.

**Rate-limit counters are per-process.** Correct while one gateway runs. A
second instance doubles the effective limit — `docs/deferred-tuning.md` §4, and
§5 for why `RemoteAddr` keying breaks behind a load balancer.

**`gofmt` reports drift in `services/auth/internal/service/interfaces.go` and
`types.go`.** Pre-existing, deliberately left alone for scope discipline
across Steps 11-13. Worth a one-line cleanup commit before any `fmt` check
lands in a Makefile target or CI.

---

## Where things are written down

| | |
|---|---|
| `agents.md` | master context, working agreement, architecture |
| `PHASE1_CHECKLIST.md` | Phase 1, all 9 steps + Step 10 — **closed** |
| `PHASE2_CHECKLIST.md` | Phase 2 — Steps 11-13, and what remains |
| `SPEC.md` | the current step's spec (Step 13) |
| `tasks/plan.md`, `tasks/todo.md` | the current step's breakdown and checkpoints |
| `docs/TESTING_STRUCTURE.md` | test layout; §6a is the integration-test guide |
| `docs/security-backlog.md` | 8 known gaps — items 1 and 2 closed, item 3 next in line but not urgent |
| `docs/deferred-tuning.md` | deferred decisions with triggers; §8 added by Step 13 |
| `docs/archive/phase*/` | every completed step's spec, plan, and todo |
| `docs/intent/quantsim-resume.md` | why the workflow changed in July 2026 |
