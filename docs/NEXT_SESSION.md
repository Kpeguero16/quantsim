# Next session — state of play

Last updated **2026-08-17**, at the end of the session that shipped Step 12.

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Nothing is in flight

Step 12 is reviewed, merged, and pushed. There is no work to recover.

| | |
|---|---|
| Branch | `main`, clean, in sync with `origin/main` |
| Migrations | schema at version **5**, not dirty — Step 12 added none |
| Tests | `make test` 10 packages ok; `make test-integration` **15 PASS / 0 SKIP** |
| Dev database | verified `users=15`, `accounts=15` — unchanged throughout |
| Stale branch | `step12-store-integration-harness` exists locally and on `origin`, fully merged — safe to delete |

**Pre-merge review found a guard that read as protective but could never
fire**, fixed on the branch before it merged (`dfe6ba3`). Together with three problems found
while building, that makes four, and all four had the same symptom: a green
`ok` while testing nothing. All are written up in `PHASE2_CHECKLIST.md`,
and they are the reason to distrust `ok` from this suite without counting
`--- PASS` lines.

`SPEC.md`, `tasks/plan.md`, and `tasks/todo.md` describe **Step 12, fully
checked off**. By convention they are archived to
`docs/archive/phase2-step12-store-integration-harness/` when the *next* spec is
drafted, not before.

---

## What Step 12 did

`services/auth/internal/store/` had **no tests at all**. Every auth suite ran
against a Go map, so all 18 test files would have stayed green against a
completely wrong SQL query — including Step 10's `lower(email)` fix, which
lives in that layer and was verified only by hand.

There are now 15 tests against a real Postgres, in `services/auth/integration/`
behind the repo's first build tag. Full write-up in `PHASE2_CHECKLIST.md`.

**Run them:**

```bash
make docker-up
make test-integration
```

With Docker stopped the suite skips and exits 0, so `make test` stays green on
a laptop with nothing running.

### Three things to know before touching this code

**The suite must never touch the dev database, and the environment actively
misleads.** `POSTGRES_DB=quantsim` is an **empty decoy**; `DATABASE_URL` points
at `postgres`, which is where the 15 real users live. Both names a careless
harness would grab are wrong, and one is wrong destructively. The target is
`quantsim_test`, and `assertTestDB` fails closed twice over: an absolute
`protectedDatabases` denylist first, then an exact match on the constant. It
runs when the DSN is derived, before the `DROP`, after the pool connects, and
immediately before every `TRUNCATE`.

**Do not simplify that to one comparison against the constant.** That was the
first version, and pre-merge review found it defended only against a wrong
DSN — editing the constant to `postgres` would have passed every check while
truncating real data. The denylist is what makes the constant itself subject to
the guard.

**Exactly one condition may skip the suite: Postgres unreachable.** Everything
else fails. This is not fussiness — an earlier version skipped on any setup
error, migrations turned out not to be idempotent, and the whole suite reported
a green `ok` while running zero tests. `make test-integration` also runs with
`-v` for the same reason: without it `go test` hides skipped-test output, so an
all-skip run looks exactly like an all-pass one.

**Pair any new store test with a mutation check.** Break the query it covers
and confirm *that* test fails. A store test that passes against a broken query
is worse than none, because it gets trusted. `docs/TESTING_STRUCTURE.md` §6a
has the conventions, including why there is no `t.Parallel()` in that package.

---

## What to do next, in order

### 1. Refresh-token revocation and a real logout

`docs/security-backlog.md` item 2, and now the **highest-priority open**
security item. Refresh tokens live 7 days with no kill switch; "sign out" is
client-side only. With a trading engine, a leaked refresh token is a week of
authenticated access to someone's positions.

It uses Redis rather than Postgres, so it adds no SQL — but the store harness
now exists either way, and the gateway already has the middleware patterns from
Step 11.

Note the trap recorded in the backlog: if you choose rotation with reuse
detection, the frontend's shared in-flight refresh
(`frontend/src/api/client.ts`) stops being an efficiency measure and becomes a
*correctness requirement* — seven parallel refreshes would burn seven tokens
and look exactly like token theft.

### 2. Phase 2 proper — the trading engine

Order execution, trade history, P/L tracking. The reason Step 12 came first:
this is where the SQL volume arrives, and it now lands against a working safety
net. Per `agents.md`, start with a spec, get it reviewed, then build to
checkpoints.

---

## Restarting the environment

```bash
make docker-up        # Postgres + Redis
make run-auth         # :8081
make run-gateway      # :8080
make run-frontend     # :5173
```

Each `run-*` target runs in the foreground, so they need separate terminals.

`make help` now lists the test targets too.

Auth rate limiting is **on by default** (100 requests / 15 min per IP; backoff
after 5 consecutive failed logins). `RATE_LIMIT_ENABLED=false` turns it off if
it gets in the way during development — the gateway logs a warning at boot
whenever it is off.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty
database named `quantsim` also exists. `psql -d quantsim` connects successfully
and shows no `users` table, which reads like data loss and is not. The user is
**`quantsim`** and the database is **`postgres`** — that combination catches
people out:

```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT count(*) FROM users"     # 15
```

**`migrate` lives at `~/go/bin/migrate` and is not on a non-interactive
shell's PATH.** Use `make migrate-up` from an interactive shell, or the full
path. Note the integration harness does *not* use it — it execs the `.up.sql`
files directly, which is recorded in `docs/deferred-tuning.md` §7 with the
trigger that would change it.

**A failed migration leaves the schema dirty.** Recovery is
`make migrate-force VERSION=<n>` at the last good version, then fix the cause
and re-run. This applies to the dev database only; the test database is
recreated from scratch on every run and cannot be left dirty.

**Restart a service after changing its code.** Everything runs under `go run`,
so a live instance keeps serving the old binary. This silently burned an entire
step once: `:8081` was still accepting one-character passwords while three
commits of validation sat on disk.

**A green `go test ./...` still says nothing about SQL.** It no longer says
*nothing about the store* — `make test-integration` covers that now — but the
tagged suite is invisible to default tooling. `make vet` includes a
`-tags=integration` pass so it cannot rot silently; keep it that way.

**Rate-limit counters are per-process.** Correct while one gateway runs. A
second instance doubles the effective limit — `docs/deferred-tuning.md` §4, and
§5 for why `RemoteAddr` keying breaks behind a load balancer.

**`gofmt` reports drift in `services/auth/internal/service/interfaces.go` and
`types.go`.** Pre-existing, untouched by Steps 11–12, and deliberately left
alone for scope discipline. Worth a one-line cleanup commit before any `fmt`
check lands in a Makefile target or CI.

---

## Where things are written down

| | |
|---|---|
| `agents.md` | master context, working agreement, architecture |
| `PHASE1_CHECKLIST.md` | Phase 1, all 9 steps + Step 10 — **closed** |
| `PHASE2_CHECKLIST.md` | Phase 2 — Steps 11 and 12, and what remains |
| `SPEC.md` | the current step's spec (Step 12) |
| `tasks/plan.md`, `tasks/todo.md` | the current step's breakdown and checkpoints |
| `docs/TESTING_STRUCTURE.md` | test layout; **§6a is the integration-test guide** |
| `docs/security-backlog.md` | 8 known gaps — item 1 closed, **item 2 is next** |
| `docs/deferred-tuning.md` | deferred decisions with triggers; §6/§7 added by Step 12 |
| `docs/archive/phase*/` | every completed step's spec, plan, and todo |
| `docs/intent/quantsim-resume.md` | why the workflow changed in July 2026 |
