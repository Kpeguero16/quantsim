# Next session — state of play

Last updated **2026-08-14**, at the end of the session that shipped Step 11.

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## One thing is in flight

**Step 11 is complete and committed, but sits on a branch that has not been merged.**

| | |
|---|---|
| Branch | `step11-auth-rate-limiting`, clean, **6 commits ahead of `main`** |
| `main` | unchanged at `8a8dbb9` |
| Migrations | schema at version **5**, not dirty — Step 11 added none |
| Tests | green: `services/gateway` (incl. `-race`), `services/auth`, `pkg` |
| Not pushed | the branch is local only |

**The decision waiting for you: merge it, or review it first.** Nothing else
depends on the branch, so there is no rush and no conflict risk — `main` has
not moved.

`SPEC.md`, `tasks/plan.md`, and `tasks/todo.md` describe **Step 11, fully
checked off**. By convention they are archived to
`docs/archive/phase2-step11-auth-rate-limiting/` when the *next* spec is
drafted, not before.

---

## What Step 11 did

Closed `docs/security-backlog.md` item 1 — the largest remaining gap in the
auth surface. Per-IP limiting on `/auth/*` and per-account exponential backoff
on `/auth/login`, both at the gateway. No new module dependency, no migration,
no change to `services/auth/`. Full write-up in `PHASE2_CHECKLIST.md`.

**Two things are worth knowing before you touch this code:**

**The backlog was wrong about `X-Forwarded-For`, and the entry is now
corrected.** It claimed the gateway's `SetXForwarded()` call sanitises the
inbound header so a limiter could trust it. That call runs on `r.Out` inside
the proxy's `Rewrite`, after all middleware, and shapes only the upstream
request. Building to it would have produced a limiter bypassable with one
forged header per request. If you find yourself reaching for a forwarding
header anywhere in gateway middleware, that is the trap.

**A `429` must never distinguish a throttled account from a wrong password.**
The limiter keys on the submitted email and consults no database, so unknown
and real addresses throttle identically. This is what keeps it from undoing
the uniform-failure property Step 9 §2.12 built deliberately. There is a test
that fails if it is ever broken, and it is verified by mutation.

---

## What to do next, in order

### 1. Merge or review `step11-auth-rate-limiting`

Six commits, each one task with its own tests. Any point on the branch is a
clean rollback.

### 2. A store-layer integration harness — unchanged from last time

`internal/store/` still has **no tests at all**. Both the service and handler
suites run against `mock.UserStore`, a Go map — they would stay green with a
completely wrong SQL query. Step 10's central fix lives in exactly that layer
and was verified by hand.

Step 11 did not change this, and did not make it worse: the gateway work
touches no SQL.

`docs/TESTING_STRUCTURE.md` §4 sketches the shape (`services/auth/integration/`,
`-tags=integration`, a real Postgres). The open decision is the harness:
testcontainers vs. the existing docker-compose, and how CI gets a database.

**Do this before the trading engine**, which will add far more SQL than auth
ever had.

### 3. Refresh-token revocation and a real logout

`docs/security-backlog.md` item 2, now the **highest-priority open** security
item. Refresh tokens live 7 days with no kill switch; "sign out" is
client-side only. With a trading engine, a leaked refresh token is a week of
authenticated access to someone's positions.

Note the trap recorded there: if you choose rotation with reuse detection, the
frontend's shared in-flight refresh (`frontend/src/api/client.ts`) stops being
an efficiency measure and becomes a *correctness requirement* — seven parallel
refreshes would burn seven tokens and look exactly like token theft.

### 4. Phase 2 proper — the trading engine

Order execution, trade history, P/L tracking. Per `agents.md`, start with a
spec, get it reviewed, then build to checkpoints.

---

## Restarting the environment

```bash
make docker-up        # Postgres + Redis
make run-auth         # :8081
make run-gateway      # :8080
make run-frontend     # :5173
```

Each `run-*` target runs in the foreground, so they need separate terminals.

Rate limiting is **on by default** with generous limits (100 requests per 15
min per IP; backoff after 5 consecutive failed logins). If it gets in the way
during development, `RATE_LIMIT_ENABLED=false` turns it off — the gateway logs
a warning at boot whenever it is. All five knobs are documented in
`.env.example`; none needs setting for the stack to run.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty
database named `quantsim` also exists. Running `psql -d quantsim` connects
successfully and shows no `users` table, which reads like data loss and is
not. Use `-d postgres`, or better, `"$DATABASE_URL"`. This cost real confusion
once — a manual `DELETE` appeared to do nothing because it was aimed at the
wrong database.

**`migrate` lives at `~/go/bin/migrate` and is not on a non-interactive
shell's PATH.** Use `make migrate-up` from an interactive shell, or the full
path.

**A failed migration leaves the schema dirty.** Recovery is
`make migrate-force VERSION=<n>` at the last good version, then fix the cause
and re-run.

**Restart a service after changing its code.** Everything runs under `go run`,
so a live instance keeps serving the old binary. This silently happened for an
entire step once: `:8081` was still accepting one-character passwords while
three commits of validation sat on disk. If behaviour does not match the code,
check this first. It applies to the gateway now too.

**The unit suites cannot see store-layer changes.** See item 2 above. Do not
read a green suite as coverage of anything in `internal/store/`.

**Rate-limit counters are per-process.** Correct today, because one gateway
runs. The moment a second instance serves traffic the effective limit is
doubled — `docs/deferred-tuning.md` §4, with its trigger named. §5 records the
related one: `RemoteAddr` keying breaks behind a load balancer, where all
traffic would collapse onto a single key.

---

## Where things are written down

| | |
|---|---|
| `agents.md` | master context, working agreement, architecture |
| `PHASE1_CHECKLIST.md` | Phase 1 status, all 9 steps + Step 10 — **closed** |
| `PHASE2_CHECKLIST.md` | Phase 2 status, starting with Step 11 |
| `SPEC.md` | the current step's spec (Step 11) |
| `tasks/plan.md`, `tasks/todo.md` | the current step's breakdown and checkpoints |
| `docs/security-backlog.md` | 8 known gaps — **item 1 now closed**, item 2 is next |
| `docs/deferred-tuning.md` | performance defaults to revisit, §4/§5 added by Step 11 |
| `docs/TESTING_STRUCTURE.md` | how tests are meant to be laid out |
| `docs/archive/phase1-step*/` | every completed step's spec, plan, and todo |
| `docs/intent/quantsim-resume.md` | why the workflow changed in July 2026 |
