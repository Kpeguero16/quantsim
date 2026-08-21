# Next session — state of play

Last updated **2026-08-20**, with Step 21 (insight generation) complete on
`step21-insight-generation` and **not yet merged**. **Phase 4's two AI items are
both done; the infra half is what remains.**

This file answers three questions on picking the project back up: *is anything
half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to
be rewritten each time, not appended to.

---

## Step 21 is complete but unmerged. Nothing else is half-finished.

| | |
|---|---|
| Branch | `step21-insight-generation`, 17 commits, **not yet squashed or merged**. Steps 16–20's pattern is squash to one `feat(step21)` commit, then `--no-ff` into `main`. Feature branches stay local; only `main` is pushed. |
| Tests | `make vet`/`test` green across all seven modules. `go test -race` clean on `ai-insights`. `GOWORK=off go build ./...` passes for all seven — the Dockerization case. **374 tests** in `ai-insights`. |
| Mutations | **24 run, 24 killed.** Three real defects came out of it; see `PHASE4_CHECKLIST.md`'s Step 21 entry. |
| Manual pass | Done against a real portfolio. **25 of 25 figures verified by eye**, no advisory language. ~10 billable calls, roughly **$0.20**. |
| Dev database | `users=20 accounts=20 trades=0 orders=0 positions=0`, `historical_prices` at 3507 rows — restored after the manual pass and **verified by query**. Redis has no leftover `narrative:*` keys. |
| Local processes | All services killed. Postgres and Redis containers up. `lsof -nP -iTCP -sTCP:LISTEN \| grep 808` shows nothing. |

Spec, plan and todo are archived at
`docs/archive/phase4-step21-insight-generation/`. Root `SPEC.md` and `tasks/`
live only on the feature branch and are not carried on `main`.

---

## What Step 21 shipped

`GET /insights/portfolio/narrative` — three short paragraphs, one per Step 20
section, in which **every figure was rendered by Go from the report struct and
none was produced by the model.**

The model is handed the report *with* its values, because it has to know a 34%
drawdown is severe and a 2% one is not, and must write prose in which every
figure is a named placeholder. Go substitutes. A surviving digit rejects the
draft; one retry quoting the offending fragment, then refusal. Nothing is ever
repaired.

- `internal/narrative/` — `Placeholders` (the vocabulary, built once and used
  twice), `Render` (the only place a figure becomes text), `Validate` (three
  checks plus caps), `Generate` (draft → validate → retry once → render).
- `internal/llm/` — the frozen system prompt and the `claude-opus-5` client at
  effort `low`. It returns the model's **raw** text and knows nothing about the
  guarantee, which is what stops a change there from weakening it.
- Cache `narrative:{user_id}:{report_hash}`, 24h; a daily generation cap that
  **fails closed**; nine distinct degradation reasons, all 200s.

Two carry-over items landed first: the `services/auth` `gofmt` drift, and
`trading-engine`'s unbounded portfolio pricing (**15.014s → 3.007s** against a
hung Redis with 5 holdings).

---

## What to do next

**1. Merge Step 21.** Squash to one `feat(step21)` commit, `--no-ff` into
`main`, then a docs follow-up — matching Steps 16–20.

**2. Phase 4's remaining roadmap items**, in `agents.md`'s order:

- **Insights frontend** — Step 22. Two hard requirements from Step 21: it must
  follow the **percent convention** (below), and it should render the numbers
  first and fill the prose in after, which is why they are two endpoints.
- **Dockerization**, then **cloud deployment** (AWS free tier: EC2 +
  docker-compose; Redis stays containerized, ElastiCache has no free tier).
  `GOWORK=off go build ./...` passes for all seven modules today — **re-check it
  before starting**, since that is exactly what a standard Go Dockerfile does.
- **`docs/deferred-tuning.md`** — unblocked by deployment. Step 21 added nothing
  to it.

**3. The long-standing small items.**

- `market-data`'s store still has no tests (`historical_price_store.go`). The
  integration harness is still in **three** copies; `ai-insights` owns no
  database, so it did not become the fourth and
  `docs/TESTING_STRUCTURE.md` §6a's extraction trigger is **still unfired** —
  re-confirmed in Step 21 rather than assumed.
- The `gofmt` drift is **closed**.

**4. Security backlog:** items 1, 2 and 4 are closed. Item **8**
(Unicode-normalise passwords) is the cheap one left and gets more expensive as
accounts accumulate. Item **3** (Argon2id) is next substantive and wants its own
step, since it carries a migration strategy.

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

Each `run-*` target runs in the foreground, so they need separate terminals.

`ANTHROPIC_API_KEY` is in `.env` and **optional** — without it the report
endpoint is unaffected and the narrative endpoint returns 200 with
`narrative: null`. `ANTHROPIC_MODEL` defaults to `claude-opus-5`.

**Without `REDIS_URL` the narrative endpoint returns nothing, deliberately.** No
Redis means no cache *and* no generation counter, and uncached plus uncapped is
the one combination with no cost ceiling. The report endpoint still works.

Auth rate limiting is **on by default** (100 requests / 15 min per IP; backoff
after 5 consecutive failed logins). `RATE_LIMIT_ENABLED=false` turns it off.
`services/auth` requires `REDIS_URL` to boot. **Do not put `PORT=` in `.env`** —
the Makefile exports that file to every target.

**Register a fresh password with something that isn't your username, email, or
"quantsim."** Registration also requires a `username`.

**The `migrate` CLI is installed to `$(go env GOPATH)/bin`, not on the default
`PATH`.** Step 21 added no migration; `ai-insights` still owns no tables.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty
database named `quantsim` also exists, so `psql -d quantsim` connects and shows
no `users` table, which reads like data loss and is not:

```bash
docker exec quantsim-postgres psql -U quantsim -d postgres -tAc \
  "SELECT count(*) FROM users"     # 20
```

**`docker stop` is NOT a Redis outage — use `docker pause`.** A stopped
container *refuses* connections in microseconds, so everything fails fast and
degraded paths look healthy: the unfixed sequential pricing loop finished in
2.5s under `docker stop` and looked fixed while still broken. A paused container
accepts the connection and never answers, which is the outage that actually
hurts. This cost real time in Step 21 and will again.

**`context.WithTimeout` around a go-redis call does nothing by default.**
`ContextTimeoutEnabled` is `false` unless set, and while it is false the client
ignores context deadlines and waits its own `ReadTimeout`. The code reads as
bounded, compiles, passes review, and waits the full default anyway — that is
where `GET /insights/portfolio`'s 6.05s came from. Build Redis clients through
`internal/cache.NewClient`, which sets it.

**A mock that reimplements the logic it stands in for cannot test it.** The
cap-boundary tests drove the mock counter, which had its own copy of the
comparison, so the real implementation was exercised by nothing until two
mutations exposed it. miniredis drives the real one now.

**A mutant that does not apply is not a caught mutant**, and it looks exactly
like coverage. Two reported as SURVIVED in Step 21 purely because the
replacement string did not match — one dropped a `§`, one had wrong regex
escaping. Verify the mutation applied before believing the result.

**A test whose deadline is tighter than the budget it checks is testing its own
setup.** Step 21's first pricing-budget test passed a 900ms parent context and
went green against the *unfixed* sequential code.

**Percentages round halfway cases AWAY FROM ZERO, and Step 22 must match.**
`frontend/src/format.ts` has `formatPrice`, `formatQuantity` and `formatDate`
but **no percent formatter**, so Step 21 set the convention. Go's `FormatFloat`
rounds halves to even; `toFixed`, `toLocaleString` and `Intl.NumberFormat` all
round away from zero. An exact 7.25 is `7.2` under Go's rule and `7.3` in a
browser. The narrative renders server-side, so a mismatched frontend formatter
puts the same figure on screen two ways.

**Drawdown is a positive magnitude.** `pkg/portfoliomath` reports "the largest
peak-to-trough decline ... as a positive percentage", so a 1.7% fall arrives as
`1.7`, not `-1.7`. Rendering it signed prints "+1.7%" for a loss. A unit fixture
using a negative drawdown is testing a value no code path can produce.

**A cache hit returns no `generated_at`.** That is how a hit is told from a
fresh generation. Identical figures give identical prose word for word — correct,
and it will read as staleness.

**A refusal is an HTTP 200 with a stop reason**, not an error. Check
`StopReason` before reading content, or it becomes an empty draft and burns the
retry on something a retry cannot fix.

**An `httptest` handler that blocks on the request's own context deadlocks the
test.** `Close` waits for outstanding requests; it needs a release channel closed
by a function `defer`, which runs before any `t.Cleanup`.

**`historical_prices` has no `date` column** — the column is `timestamp`
(`timestamptz`), and a bar's calendar day is `timestamp::date`. Bars run
**2024-07-29 to 2026-07-28** for seven symbols: AAPL, AMZN, GOOGL, MSFT, QQQ,
SPY, TSLA. **`NVDA` has none** — a position in it makes the report 404. Any
hand-built trade history must sit inside that range, or the reconstruction has
no calendar and every section is `insufficient_data`.

**A hand-built trade history must reconcile or the whole report blanks.**
Derived cash replayed from the trade log has to equal `accounts.balance`, and
positions have to match the net quantities (SPEC §2.12). Insert orders, trades,
positions and the balance in one transaction, computed together.

**`docker exec` without `-i` silently discards a heredoc.** It connects, reads
nothing, and exits 0.

**`timeout` is not installed on macOS.** A loop using it reports every case as
failing, which looks like a catastrophic result and is just a missing binary.
Use `go test -timeout` instead.

**Careless `pkill -f` patterns kill sibling services.** In Step 21 one killed
`market-data` mid-measurement and produced an 8ms reading that looked like a
spectacular result. Check what is still listening before trusting a number.

**A `go run` service started before a code change keeps serving the old
binary.** Kill the process tree and restart. `vite` is the exception.

**A gateway wildcard route (`/prefix/*`) does not match the bare prefix.** That
was Step 16's bug. `/insights/*` covers `/insights/portfolio/narrative` because
it has a further segment.

**A nil Go slice and an empty one are `len()`-identical but
`encoding/json`-different**: `var s []T` marshals as `null`, `s := []T{}` as
`[]`. The narrative object relies on this deliberately — no prose marshals as
`null`, never `{}`.

**`omitempty` cannot tell "absent" from "zero".** Every figure whose zero is a
reachable measurement must not carry it. `Finding.TurnoverRatio` and
`Occurrences` do carry it, so `Placeholders` treats their zero as absent to
match.

**Percentage-form threshold comparisons are untestable at their own boundary.**
Compare prices against a threshold price instead. See
`previousTradingDayDropped`.

**`backtests.user_id` has no `ON DELETE CASCADE`.** Delete children before
parents when cleaning up a test account: trades/orders/positions → accounts →
users, and `backtests` before the user.

**The frontend holds both tokens in memory only.** A page refresh logs you out,
so browser-driven verification has to go through the login form.

**`go build` succeeding says nothing about building outside the workspace.**
`go.work` supplies requirements an individual `go.mod` may lack:

```bash
for m in pkg services/*; do printf '%-28s ' "$m"; (cd $m && GOWORK=off go build ./... >/dev/null 2>&1 && echo OK || echo FAILS); done
```

**A bare `tsc --noEmit` silently no-ops** against this project's `tsconfig`. Use
`npm run build` or `npx tsc -b`.

---

## Where things are written down

| Topic | File |
|---|---|
| Phase 1 (auth + market data) | `docs/archive/phase1-step4-auth/` onward |
| Phase 2 (trading engine) — complete | `PHASE2_CHECKLIST.md`, `docs/archive/phase2-step*` |
| Phase 3 (backtesting engine) — complete | `PHASE3_CHECKLIST.md`, `docs/archive/phase3-step*` |
| Phase 4 (AI insights + infra) — in progress | `PHASE4_CHECKLIST.md`, `docs/archive/phase4-step*` |
| Deferred tuning / known trade-offs | `docs/deferred-tuning.md` |
| Testing conventions | `docs/TESTING_STRUCTURE.md` |
| Security backlog | `docs/security-backlog.md` |
| Roadmap / phase definitions | `agents.md` |
