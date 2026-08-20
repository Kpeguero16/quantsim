# Next session — state of play

Last updated **2026-08-19**, at the end of Step 19 (portfolio backtests) — **built and verified, not yet reviewed or merged.**

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Step 19 is complete on its branch and waiting on review

This is the one difference from how the last few sessions ended: **nothing has been merged.** All 19 tasks are done and every checkpoint is green, but the independent adversarial review that `SPEC.md` §4 requires before merge has **not** been run yet. That review is the next action, not a formality — Step 18's found a real integer-overflow bug, and this step's shared-cash arithmetic and first-ever array column are at least as good a place to look.

| | |
|---|---|
| Branch | `step19-portfolio-backtests`, local only, **unmerged**. 24 commits, one per task plus the review-finding fix (R1). `main` is untouched since Step 18. |
| Tests | `make vet`/`test`/`test-integration` green across all five services. `go test -count=1 -race` green on `services/backtesting`, with and without `-tags=integration`. Frontend: `tsc -b` clean, `npm run build` ✓, `npm run test` 61/61, `npm run lint` with only the four pre-existing `exhaustive-deps` warnings (none in a file this step touched). |
| Dev database | `users=20`, `accounts=20`, `backtests=0` — restored to baseline after the manual pass. Migration **`009_backtest_portfolios` is applied** (`schema_migrations` 9, not dirty): `backtests.symbols TEXT[]`, `backtest_trades.symbol`, `backtest_trades.seq`, and `backtests.symbol` **dropped**. |
| Local processes | `auth` and `market-data` were already running and were left alone. **`gateway`, `backtesting`, `trading-engine`, and the frontend dev server were started during Step 19's manual pass and left running** — unlike previous sessions, they were not killed. Check `lsof -i :8080-8084` and `:5173` and kill what you don't want before assuming any port's state. |

`docs/archive/phase3-step19-portfolio-backtests/{SPEC.md,plan.md,todo.md}` hold this step's spec, plan and todo. The todo is unusually detailed — it records what each mutation caught and what each verification actually proved, which is the material a reviewer wants.

---

## What Step 19 shipped

One backtest run over **N symbols drawing on a single shared pool of capital** — the last named item in `agents.md` §3's backtesting scope, deferred by both Step 16 and Step 18 as "a materially different simulator."

Deliberately *not* N independent single-symbol runs stapled together: that would have been a loop around the existing engine and would have answered a different question. The symbols genuinely compete for the same cash.

- `alignBars` — the intersection of every symbol's dates, alphabetically ordered, so bar index `i` is the same trading day for every symbol.
- `SimulatePortfolio` — sells settle first into the shared pool, one `target := equityAtOpen / N` per bar, then buys in symbol order capped at `min(cash, target)`. `Simulate` was proven equivalent at N=1 and then **deleted**.
- `symbol TEXT` → `symbols TEXT[]`, per-trade `symbol`, and a stored trade-log `seq` (migration `009`).
- `normalizeSymbols` — trim/upper/sort, rejecting empty, >10, and case-insensitive duplicates outright; mirrored client-side by `validateSymbols`.
- Frontend: a comma-separated Symbols field, a Symbol column in the trade log, and `symbols.join(', ')` wherever a run's symbol was shown.
- `ComputeMetrics` is **untouched** and still does not know how many symbols produced the curve it is handed.

Full writeup — mutation results, integration additions, and the manual pass — in `PHASE3_CHECKLIST.md`'s Step 19 entry.

### Four things worth knowing about

1. **An `ORDER BY` over row values cannot express a sequence those values don't determine.** This was Step 19's one real bug (R1), found in the Checkpoint B review. The trade SELECT was `ORDER BY bar_timestamp, id` with `id` a random UUID, and it worked only because one run meant one symbol and so at most one fill per bar. A portfolio run fills several symbols on the same bar routinely, so a same-bar group came back shuffled differently on every read — a sell could be listed beneath the same-bar buy it funded. The fix stores the order (`seq INTEGER NOT NULL` + `UNIQUE (backtest_id, seq)`) rather than re-deriving it; re-deriving would have been a second copy of the engine's within-bar rules, free to drift from them.
2. **`[]string` binds and scans against `TEXT[]` through pgx v5 with no `pgtype` wrapper.** This is the repo's first array column, so it had no precedent here. It was verified against real Postgres in the integration suite rather than trusted to compile — a `Scan` that compiles proves nothing about an array codec.
3. **A zero-value `errgroup.Group` is sometimes the right choice over `WithContext`.** `fetchHistories` deliberately does *not* cancel siblings on the first failure: a sibling cancelled mid-flight would report a context error in place of the real one it was about to return, which would reintroduce exactly the scheduling-dependent nondeterminism the ordered error scan exists to remove. Errors are collected per-index and scanned in symbol order, so two unavailable symbols always name the same one.
4. **`NUMERIC(20,4)` bites harder at N>1 than it did at N=1.** A position is now funded by `equity/N`, so the same fixed 0.0001 granularity covers a larger share of a smaller position. Quantity assertions in the integration suite read the column as text via the existing `numeric()` helper and compare against what Postgres actually kept — comparing `float64`s would be checking Go's arithmetic against itself.

---

## What to do next

**1. Review and merge Step 19.** The independent adversarial review (`SPEC.md` §4) is the blocking item. Highest-value places to look, in order:

- `SimulatePortfolio`'s shared-cash arithmetic — plausible-looking and wrong is the failure mode here, and the A/B equivalence test that originally guarded it was deleted in T7 once `Simulate` went (three portfolio tests now cover it; that was re-proven by mutation, not assumed).
- `009`'s backfill. It is structurally **uncoverable** by the integration harness, which always migrates a freshly created empty database — the `UPDATE` there always runs against zero rows. It was verified by hand instead, in both directions, including a live down/up against the real dev database during T18. Do not add a test that appears to cover it (plan D2).
- The `[]string ⇄ TEXT[]` round trip, as the first array column in the repo.

Then squash to a single `feat(step19): …` commit and merge, matching Steps 16–18's precedent.

**2. Phase 3 is finished once that merge lands.** `agents.md` §3's backtesting scope has no remaining named items. The next major work is **Phase 4 — AI Insights + Infra** (portfolio analytics, insight generation, Dockerization, cloud deployment), and `services/ai-insights` is still a stub `go.mod`.

**3. The two long-standing small items**, both still open and both still lower priority:

- `market-data`'s store has no tests (`historical_price_store.go`). The integration harness exists in **three** copies (auth, trading-engine, backtesting); a fourth use is the point to actually extract to `pkg/testutil/` — see `docs/deferred-tuning.md` §11 and `docs/TESTING_STRUCTURE.md` §6a.
- Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`, untouched since Step 11. Worth a one-line cleanup commit before any `fmt` check lands in CI.

**4. Security backlog:** items 1, 2 and 4 are closed. Item **8** (Unicode-normalise passwords) is the cheap one left from the Phase 2 set and gets more expensive as real accounts accumulate. Item **3** (Argon2id) is the next substantive one and wants its own step, since it carries a migration strategy.

---

## Restarting the environment

```bash
make docker-up            # Postgres + Redis
make run-auth             # :8081
make run-market-data      # :8082
make run-trading-engine   # :8083
make run-backtesting      # :8084
make run-gateway          # :8080
make run-frontend         # :5173
```

Each `run-*` target runs in the foreground, so they need separate terminals. `make help` lists the test targets too.

Auth rate limiting is **on by default** (100 requests / 15 min per IP; backoff after 5 consecutive failed logins). `RATE_LIMIT_ENABLED=false` turns it off if it gets in the way during development.

`services/auth` requires `REDIS_URL` to boot (`log.Fatal` if unset). **Do not put `PORT=8083` or `PORT=8084` in `.env`**: the Makefile exports that file to every target, so it would move every service onto the same port. Override per process instead.

**Register a fresh password with something that isn't your username, email, or "quantsim."** Auth's password validator rejects any password containing the username, the email, or the service name as a substring, case-insensitively. A generic throwaway phrase with no connection to the account's own name/email sidesteps it. Registration also requires a `username` — a body with only `email`/`password` is rejected.

**The `migrate` CLI is installed to `$(go env GOPATH)/bin`, not on the default `PATH`.** `make migrate-up`/`migrate-down` will report `migrate: command not found` from a plain shell unless `$(go env GOPATH)/bin` is on `PATH` — export it first, or run the `migrate` binary by its full path. Every migration in this repo is plain SQL with no migrate directives, so applying a file directly via `psql` and updating `schema_migrations` by hand is an equivalent fallback (that is how T18's down/up was run), but prefer the CLI when it's reachable.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty database named `quantsim` also exists. `psql -d quantsim` connects successfully and shows no `users` table, which reads like data loss and is not. The user is **`quantsim`** and the database is **`postgres`**:

```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT count(*) FROM users"     # 20, as of this session
```

**`backtests.user_id` has no `ON DELETE CASCADE`, unlike `backtest_trades.backtest_id`.** Deleting a throwaway user who has run any backtests fails with a foreign-key violation unless their `backtests` rows are deleted first. `backtest_trades` cascades fine from `backtests` — it's only the `users → backtests` edge that needs the extra step.

**The frontend holds both tokens in memory only, never in `localStorage`/`sessionStorage`/a cookie** (`SPEC.md` §2.5). A page refresh logs you out, and there is no way to hand a browser session a token out of band — any browser-driven verification has to go through the login form.

**An `ORDER BY` over row values cannot impose an order those values don't determine.** See R1 above. If a read needs a specific order and the columns can tie, store the order explicitly; do not add tie-breaker columns that re-derive a rule owned elsewhere in the code.

**A `go run` service started before a code change keeps serving the old binary.** Kill the whole `make run-*` / `go run` process tree and restart rather than assuming a long-running dev process reflects the code currently on disk. The frontend's `vite` dev server is the exception — it has HMR and picks up changes live, so it doesn't need restarting between edits the way the Go services do.

**A gateway wildcard route (`/prefix/*`) does not match the bare prefix with no trailing segment.** This was Step 16's routing bug. If a new backend service has a collection endpoint at its own root (no sub-path, unlike `trading-engine`'s `/trading/orders` etc.), the gateway needs both `r.Handle("/prefix", proxy)` and `r.Handle("/prefix/*", proxy)`.

**The integration harness now exists in three copies** (`services/{auth,trading-engine,backtesting}/integration/`), not extracted to `pkg/testutil/` yet — see `docs/deferred-tuning.md` §11 for why, and what should trigger doing it for real.

**A nil Go slice and an empty one are `len()`-identical but `encoding/json`-different.** `var s []T` marshals as `null`; `s := []T{}` marshals as `[]`. Every list-shaped response field needs the latter, deliberately, even when every existing test only ever checks `len(s)` — that check cannot tell the two apart. This is what Step 17's `Simulate` bug was, and why `scanBacktest` guards `Symbols == nil` even though the column is `NOT NULL` and cannot currently produce it.

**`toLocaleDateString()` with no `timeZone` option uses the *browser's* local zone, not UTC.** Any value that's a calendar date rather than a real instant (a form's `start_date`/`end_date`, a daily bar's `bar_timestamp`) needs `{timeZone: 'UTC'}` passed explicitly, or it can render a day off depending on where the browser sits relative to UTC. `frontend/src/format.ts`'s `formatDate` is the one place in this app that does this correctly — reuse it rather than calling `toLocaleDateString()` directly on a calendar-date field.

**A bare `tsc --noEmit` silently no-ops against this project's `tsconfig` setup and reports zero errors regardless of what's broken.** Use `npm run build` (which runs `tsc -b`) or `npx tsc -b` directly to actually typecheck this frontend.

**`Pick<UnionType, K>` does not distribute over a union in TypeScript** — it collapses a discriminated union's fields into one flat shape and loses the pairing a `switch` on the discriminant needs to narrow the other field. See `strategy-display.ts`'s `BacktestParamsByKind` for the fix (a direct union, not a `Pick`).

**A generic bound checked on a sum doesn't protect against the addends overflowing first.** `maxWarmupBars` is checked once as `WarmupBars() > 500`, but each strategy constructor also bounds its own period-like fields individually before feeding them into that arithmetic — see Step 18's overflow bug. Any future "one check covers every case" design over integer inputs derived by arithmetic needs the same treatment.

---

## Where things are written down

| Topic | File |
|---|---|
| Phase 1 (auth + market data) | `docs/archive/phase1-step4-auth/` through Step 7's archive |
| Phase 2 (trading engine) — complete | `PHASE2_CHECKLIST.md`, archived specs `docs/archive/phase2-step*` |
| Phase 3 (backtesting engine) — code complete, Step 19 pending merge | `PHASE3_CHECKLIST.md`, archived specs `docs/archive/phase3-step*` |
| Deferred tuning / known trade-offs | `docs/deferred-tuning.md` |
| Testing conventions | `docs/TESTING_STRUCTURE.md` |
| Security backlog | `docs/security-backlog.md` |
| Roadmap / phase definitions | `agents.md` |
