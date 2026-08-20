# Next session — state of play

Last updated **2026-08-20**, with Step 20 (portfolio analytics) reviewed and merged to `main`. **Phase 4 is underway; its first roadmap item is done.**

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Step 20 is merged. Nothing is half-finished.

| | |
|---|---|
| Branch | `step20-portfolio-analytics` — squashed to one `feat(step20)` commit and merged to `main` with `--no-ff`, matching Steps 16–19. Feature branches stay local; only `main` is pushed. |
| Tests | `make vet`/`test` green across all seven modules; `make test-integration` 63 passed / 0 failed. `go test -race -count=1 ./...` clean on `ai-insights`, `trading-engine` and `backtesting`. All re-run at the pre-merge review rather than carried over. |
| D1's proof | `git diff --exit-code main -- services/backtesting/internal/service/metrics_test.go` is **empty** — the `pkg/portfoliomath` extraction changed no behavior, re-checked at the end of the step rather than only at the start. |
| Dev database | `users=20 accounts=20 trades=0 orders=0 positions=0`, `historical_prices` at 3507 rows across seven symbols — restored after the manual pass and **verified by query**, not assumed. |
| Local processes | All six services were started for the manual pass and **killed afterwards**. Postgres and Redis containers are up. `lsof -nP -iTCP -sTCP:LISTEN | grep 808` should show nothing. |

`docs/archive/phase4-step20-portfolio-analytics/{SPEC.md,plan.md,todo.md}` hold this step's spec, plan and todo — root `SPEC.md` and `tasks/` live only on a feature branch and are not carried on `main`. The todo records what each mutation caught and what each verification actually proved.

---

## What Step 20 shipped

`services/ai-insights` as the sixth service, serving one endpoint — `GET /insights/portfolio` — with three sections: **risk**, **benchmarking**, **behavior**. Deterministic and rule-based: **no LLM**, no frontend, and no database.

Every figure is derived from an equity curve **reconstructed on demand**, because QuantSim stores no portfolio history — there is a trade log and there are daily bars, and nothing recording what the account was worth on any past day.

- `pkg/portfoliomath` — `Sharpe`, `MaxDrawdownPct`, `DailyReturns`, `Mean`, `StdevPopulation`, extracted from `backtesting` and landed first and alone.
- `GET /trading/trades` on `trading-engine`, **ascending** (its only consumer replays the log chronologically), deliberately unlike `ListOrders`.
- `Calendar` → `Reconstruct` → `Reconcile` — intersection of every ever-held symbol's dates plus both benchmarks, no carry-forward, and a runtime invariant that **refuses rather than repairs**.
- Risk (HHI over *invested* positions, cash separate), benchmarking (buy-and-hold `SPY` and `QQQ` over the identical calendar), behavior (three rules with named thresholds and **evidence trade IDs attached**).
- Redis cache, `insights:{user_id}`, 5-minute TTL, fail-open both ways.

Full writeup — the §2.12 amendment, every mutation survivor, and the manual pass — in `PHASE4_CHECKLIST.md`'s Step 20 entry.

### Four things worth knowing about

**The spec was wrong and real data proved it.** §2.12's reconciliation guard blanked the *entire* report for a portfolio with 39 clean days and one buy placed that morning. The cause is worth keeping: **"the curve is truncated" and "the user traded after the last stored close" are arithmetically the same event** — both look like derived cash disagreeing with `accounts.balance`. §2.12 was amended in place, with a dated subsection recording what was given back.

**That amendment then created a defect, caught before merge.** Making post-calendar trades survivable made them routine, and `panicSelling` read the calendar without bounding itself by `as_of_date` the way `overtrading` already did — so a recent sell could be flagged as panic selling, with its trade ID attached as evidence, on a price move weeks earlier. **The live run did not expose it** (the last stored session happened to rise); reading the code did.

**`risk.positions` are as of `as_of_date`, not as of now**, and can legitimately disagree with the live `positions` table. The manual pass shows a reported GOOGL holding of 10 against a live 6.

**Five separate defects in this step had the same cause**: two paths to one outcome hide each other's boundary unless a test isolates them. Four were mutation survivors, each with green tests and correct behavior; what was missing was the ability to tell which mechanism produced the result. The fifth was the pre-merge review's own finding, and it was a real bug rather than a coverage gap — `panicSelling` returned a bare `false` both for "there is no pair of prior sessions to judge this against" and for "the prior session did not drop", so sells it had refused to judge were counted in the denominator of its share test and kept the finding quiet. **The post-`as_of` case had already been excluded with that exact reasoning written in a comment four lines away.** A principle stated in one branch and not applied to its sibling is the shape to look for.

---

## What to do next

**1. Phase 4 continues** — the remaining roadmap items, in the order `agents.md` lists them:

- **Insight generation** — the LLM layer that phrases Step 20's numbers. Its contract is already fixed by Step 20's design: **it may phrase only numbers it is handed, and may never produce one.** That is why the analytics shipped first.
- **Insights frontend** — Step 21, per the Step 16 → 17 precedent.
- **Dockerization**, then **cloud deployment** (AWS free tier: EC2 + docker-compose; Redis stays containerized since ElastiCache has no free tier).
- **`docs/deferred-tuning.md`** — timeouts and pooling defaults left unset because the right values depend on traffic shape that only exists once deployed. Deployment is what unblocks this.

**2. One thing Step 20 recorded rather than fixed.** With Redis down the insights endpoint returns 502 — not a fail-open bug (the cache logs "cache read failed, computing" and proceeds correctly), but because `trading-engine` degrades *slowly*: `GET /trading/portfolio` takes **8.7s against 5.8ms healthy**, tripping the 5s upstream timeout. So a Redis outage takes the report down even though every figure in it comes from Postgres. Fixing it means changing `trading-engine`'s retry behaviour.

**3. The two long-standing small items**, both still open and both still lower priority:

- `market-data`'s store has no tests (`historical_price_store.go`). The integration harness still exists in **three** copies — `ai-insights` owns no database, so it did not become the fourth and the extraction trigger in `docs/TESTING_STRUCTURE.md` §6a is still unfired.
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
make run-ai-insights      # :8085
make run-gateway          # :8080
make run-frontend         # :5173
```

Each `run-*` target runs in the foreground, so they need separate terminals. `make help` lists the test targets too.

Auth rate limiting is **on by default** (100 requests / 15 min per IP; backoff after 5 consecutive failed logins). `RATE_LIMIT_ENABLED=false` turns it off if it gets in the way during development.

`services/auth` requires `REDIS_URL` to boot (`log.Fatal` if unset). **`services/ai-insights` does not** — without it every request is computed from scratch and the service says so at startup. **Do not put `PORT=` in `.env`**: the Makefile exports that file to every target, so it would move every service onto the same port. Override per process instead.

**Register a fresh password with something that isn't your username, email, or "quantsim."** Auth's password validator rejects any password containing the username, the email, or the service name as a substring, case-insensitively. Registration also requires a `username` — a body with only `email`/`password` is rejected.

**The `migrate` CLI is installed to `$(go env GOPATH)/bin`, not on the default `PATH`.** `make migrate-up`/`migrate-down` will report `migrate: command not found` from a plain shell unless that directory is on `PATH`. Every migration here is plain SQL with no migrate directives, so applying a file via `psql` and updating `schema_migrations` by hand is an equivalent fallback — but prefer the CLI when it's reachable. **Step 20 added no migration**; `ai-insights` owns no tables.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty database named `quantsim` also exists. `psql -d quantsim` connects successfully and shows no `users` table, which reads like data loss and is not. The user is **`quantsim`** and the database is **`postgres`**:

```bash
docker exec quantsim-postgres psql -U quantsim -d postgres -tAc \
  "SELECT count(*) FROM users"     # 20, as of this session
```

**`historical_prices` has no `date` column** — the column is `timestamp` (`timestamptz`), and a bar's calendar day is `timestamp::date`. Grouping or joining on a `date` column that doesn't exist is the first thing that fails when poking at bars by hand.

**`docker exec` without `-i` silently discards a heredoc.** `docker exec quantsim-postgres psql ... <<'SQL'` connects, reads nothing, and exits 0 — so a batch of `UPDATE`s appears to run and changes nothing. Use `docker exec -i`.

**The `insights:{user_id}` cache is not invalidated on trade** (5-minute TTL). Placing trades and re-reading the endpoint returns the *previous* report, and `computed_at` is the field that tells you so. Flush with `redis-cli DEL insights:<uuid>` when testing by hand. Errors are never cached, so a 404 or 502 does not stick.

**A Redis outage takes `/insights/portfolio` down with a 502**, despite the cache being fail-open — see item 3 above. If the endpoint 502s, check Redis before suspecting the service.

**`risk.positions` are as of `as_of_date`, not as of now.** A trade placed after the last stored bar is reflected in the reconciliation (which projects past the calendar) but *not* in the reported positions. A reported holding disagreeing with the live `positions` table is expected behavior, not a bug.

**`omitempty` cannot tell "absent" from "zero".** Every figure whose zero is a reachable measurement — an all-cash portfolio's `concentration_hhi`, a flat curve's `annualized_volatility_pct` — must not carry it, and a test that only checks a struct field will never notice, because the loss happens at marshalling. Use it only where zero is genuinely unreachable.

**Percentage-form threshold comparisons are untestable at their own boundary.** `(95.0/100.0 - 1) * 100 == -5.000000000000004` for *every* clean 5% fall, because `0.95` has no exact binary representation — so `<=` and `<` are indistinguishable there and a test claiming to pin "exactly −5%" pins nothing. Compare **prices against a threshold price** instead: `100.0 * (1 - 5.0/100) == 95.0` exactly. See `previousTradingDayDropped`.

**A mutant that doesn't compile, or no longer applies, is not a caught mutant.** Both report as something other than SURVIVED and both look like passing coverage. Re-point stale mutants when the code moves under them, and rewrite non-compiling ones (e.g. `asOf.AddDate(100, 0, 0)` in place of a bare `false`) so they actually run.

**`backtests.user_id` has no `ON DELETE CASCADE`, unlike `backtest_trades.backtest_id`.** Deleting a throwaway user who has run any backtests fails with a foreign-key violation unless their `backtests` rows go first. The same applies to `trades`/`orders`/`positions` → `accounts` → `users`: delete children before parents when cleaning up a test account.

**The frontend holds both tokens in memory only, never in `localStorage`/`sessionStorage`/a cookie.** A page refresh logs you out, and there is no way to hand a browser session a token out of band — any browser-driven verification has to go through the login form.

**`go build` succeeding says nothing about whether a module builds outside the workspace.** `go.work` supplies requirements that an individual `go.mod`/`go.sum` may be missing, so `make vet` and `make test` can be green while `cd <module> && GOWORK=off go build ./...` fails with `missing go.sum entry for go.mod file`. `pkg` and `services/gateway` were both in that state until the Step 20 review; all seven modules pass now. **Check it before Dockerization** — a standard Go Dockerfile copies one module's `go.mod`/`go.sum` and runs `go mod download`, which is exactly the off-workspace case:

```bash
for m in pkg services/*; do printf '%-28s ' "$m"; (cd $m && GOWORK=off go build ./... >/dev/null 2>&1 && echo OK || echo FAILS); done
```

**A `go run` service started before a code change keeps serving the old binary.** Kill the whole process tree and restart rather than assuming a long-running dev process reflects the code on disk. A stale `ai-insights` on `:8085` cost real time in this step: it served a pre-`T8` router, so the new route looked missing. The frontend's `vite` dev server is the exception — it has HMR.

**A gateway wildcard route (`/prefix/*`) does not match the bare prefix with no trailing segment.** This was Step 16's routing bug. `/insights/*` is fine because `ai-insights` has no collection endpoint at its own root, but any future service that does needs both `r.Handle("/prefix", proxy)` and `r.Handle("/prefix/*", proxy)`.

**A nil Go slice and an empty one are `len()`-identical but `encoding/json`-different.** `var s []T` marshals as `null`; `s := []T{}` marshals as `[]`. Every list-shaped response field needs the latter deliberately — including the degraded paths, where it's easiest to forget.

**`toLocaleDateString()` with no `timeZone` option uses the *browser's* local zone, not UTC.** Any value that's a calendar date rather than a real instant needs `{timeZone: 'UTC'}`. `frontend/src/format.ts`'s `formatDate` is the one place that does this correctly — reuse it.

**A bare `tsc --noEmit` silently no-ops against this project's `tsconfig` setup** and reports zero errors regardless of what's broken. Use `npm run build` (which runs `tsc -b`) or `npx tsc -b`.

**`Pick<UnionType, K>` does not distribute over a union in TypeScript** — it collapses a discriminated union's fields into one flat shape and loses the pairing a `switch` needs to narrow. See `strategy-display.ts`'s `BacktestParamsByKind`.

**A generic bound checked on a sum doesn't protect against the addends overflowing first.** See Step 18's overflow bug: each strategy constructor bounds its own period-like fields individually before the arithmetic that feeds the single `WarmupBars() > 500` check.

---

## Where things are written down

| Topic | File |
|---|---|
| Phase 1 (auth + market data) | `docs/archive/phase1-step4-auth/` through Step 7's archive |
| Phase 2 (trading engine) — complete | `PHASE2_CHECKLIST.md`, archived specs `docs/archive/phase2-step*` |
| Phase 3 (backtesting engine) — complete | `PHASE3_CHECKLIST.md`, archived specs `docs/archive/phase3-step*` |
| Phase 4 (AI insights + infra) — in progress | `PHASE4_CHECKLIST.md`, archived specs `docs/archive/phase4-step*` |
| Deferred tuning / known trade-offs | `docs/deferred-tuning.md` |
| Testing conventions | `docs/TESTING_STRUCTURE.md` |
| Security backlog | `docs/security-backlog.md` |
| Roadmap / phase definitions | `agents.md` |
