# Next session — state of play

> **Step 24 is merged.** Nothing is half-finished. Root `SPEC.md` and `tasks/` are untracked on
> purpose and were not carried onto `main`; the full per-task record is archived at
> `docs/archive/phase4-step24-report-cache-invalidation/todo.md`.

Last updated **2026-08-23**, with Step 24 (report cache invalidation on a fill) finished on its
branch. **Phase 4's feature work and all three of its defects are done. The infra half is what
remains, and it is now the whole of what remains.**

This file answers three questions on picking the project back up: *is anything
half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to
be rewritten each time, not appended to.

---

## Step 24 is merged. Nothing is half-finished.

| | |
|---|---|
| The change | trading-engine deletes `insights:{user_id}` after a successful fill. It gains a Redis client and an optional `REDIS_URL`; `pkg/` gains `cachekeys`, which both services use for that one key. |
| Backend | `make vet` clean; `make test` green across all seven modules; `make test-integration` **63/0**, unchanged; `GOWORK=off go build ./...` passes for all seven. |
| Tests | Ten across three packages. The invalidator runs against `miniredis`, not a mock. |
| Mutations | **6 run, 6 killed.** |
| Live stack | With Redis: the key is deleted on the fill and the refetched report agrees with the database at 6 trades. Without: the key survives and the report reads 4 while the database holds 5. |
| Cost | **$0.00.** The narrative endpoint was never called. |
| Dev database | Restored and **verified by query**: `users=20 accounts=20 trades=0 orders=0 positions=0`, `historical_prices=3525`. No `insights:*` or `narrative:*` keys in Redis. |
| Local processes | All services stopped. Postgres and Redis containers up. |

---

## What Step 24 did, and what it deliberately did not

A fill's report refetch was defeated by the five-minute cache, so the reader saw figures computed
before their own trade. trading-engine now deletes that key when it fills an order.

Three details carry the step, and each would have shipped looking correct. It is **synchronous**,
because the event being fixed is the dashboard refetching immediately after the fill and a
goroutine racing that has no ordering guarantee. It uses **`context.WithoutCancel`**, because the
fill has already committed and a client hanging up must not take the invalidation with it. And it
**can never fail an order**, because the trade is durable before it runs.

**The key stopped being a literal.** Two services produce `insights:{user_id}` now, so it lives in
`pkg/cachekeys` and takes a `uuid.UUID` rather than a string — that is what keeps ai-insights'
guarantee that a token subject cannot become an arbitrary Redis key.

**A fill now costs a narrative generation.** `narrative:{user_id}:{report_hash}` is keyed on
content, so a fill changes the report, changes the hash, and misses. Nothing needed invalidating,
and nothing should be added. It is correct, since the prose describes figures that moved, but it
is a real cost change and it only became possible once Step 23 stabilised the hash.

**Rejected as the read-side design**, and it was a real option: ai-insights checking freshness
before serving its cache keeps the dependency direction as it runs and needs no Redis in
trading-engine. It loses because `ListTrades` orders `executed_at ASC`, so `?limit=1` returns the
oldest trade and there is no cheap probe to call, and because it would pay an HTTP round trip on
every cached read forever to catch something that happens once per fill.

---

## What to do next

**Phase 4 has no defects left. What remains is infrastructure and long-standing small items.**

**1. Dockerization**, then **cloud deployment** (AWS free tier: EC2 + docker-compose; Redis stays
containerized, ElastiCache has no free tier). `GOWORK=off go build ./...` passes for all seven
modules today, re-checked in Step 24 with `pkg`'s new package in place, not assumed.

Worth knowing before starting: **trading-engine now reads `REDIS_URL`**, so it belongs in the
compose environment alongside auth's and ai-insights'. It is optional — orders place without it —
but a deployment that omits it silently reintroduces Step 24's defect.

**2. `docs/deferred-tuning.md`** — unblocked by deployment. Step 24 added nothing to it.

**3. Consolidate the four Redis client construction sites.** `ai-insights` and now
`trading-engine` set `ContextTimeoutEnabled`; **`auth` and `market-data` do not**, so
`context.WithTimeout` around their Redis calls does nothing and each waits its own `ReadTimeout`.
That is the defect that cost 6.05s in Step 21, latent in two services. Its own step, because it
touches auth's token revocation path. Found in Step 24's survey, not fixed by it.

**4. The long-standing small items.**

- **The frontend hooks have no tests at all.** `use-narrative`'s double-spend guard protects a
  billed call, and it broke in Step 22 without a single test noticing. Needs `renderHook`;
  `@testing-library/react` is installed and still unused.
- `market-data`'s store still has no tests (`historical_price_store.go`). The integration harness
  is still in **three** copies; Step 24 added no `integration/` package, so
  `docs/TESTING_STRUCTURE.md` §6a's extraction trigger is **still unfired**.

**5. Security backlog:** items 1, 2 and 4 are closed. Item **8** (Unicode-normalise passwords) is
the cheap one left and gets more expensive as accounts accumulate. Item **3** (Argon2id) is next
substantive and wants its own step, since it carries a migration strategy.

**6. Worth considering, not scheduled: cache histories by symbol rather than reports by user.**
The report math is ~25µs; essentially the entire cost the report cache avoids is HTTP, and most of
that is per-symbol history identical for every user. That design would have deleted Step 24's
problem rather than needing it patched, and it would get a far better hit rate. It is a rewrite of
`ai-insights` SPEC §2.8, so it wants a real reason before anyone starts.

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

**`REDIS_URL` now reaches trading-engine too, and it is optional there** (Step
24). With it, a fill deletes the placing user's cached report. Without it,
orders place exactly as before and that report can lag the trade by up to five
minutes -- the service says so at boot, and `make REDIS_URL= run-trading-engine`
is how to reproduce the old behaviour deliberately.

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

**A position quantity does not change after a fill, and it looks exactly like a
failed cache invalidation.** Holdings describe `as_of_date`, which is where the
bar calendar ends, and a trade after that date is projected forward for the
reconciliation guard only -- never into reported positions. That is §2.12's
tail truncation working as designed. **`behavior.trade_count` is the figure that
moves**, so that is the one to watch when checking invalidation by hand. Chasing
the position quantity instead means debugging correct code.

**Cancelling a context before calling `PlaceOrder` never reaches a fill.** The
price fetch is refused first, the order is recorded as rejected, and any
assertion about what happens after a fill is testing nothing while passing.
Reaching post-fill code with an already-cancelled request context needs the
cancellation to happen *during* the fill; `mock.TradingStore.OnExecute` exists
for exactly that, and before it the cancellation test passed against code with
no `context.WithoutCancel` in it at all.

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

**Percentages round halfway cases AWAY FROM ZERO — but "any browser formatter
will do" is FALSE, and Step 22 measured it.** Go's `FormatFloat` rounds halves
to even, so the backend uses `roundHalfAway` instead. The note this file used to
carry said `toFixed`, `toLocaleString` and `Intl.NumberFormat` all round away
from zero and the frontend could therefore use a one-liner. That is true of the
rule and false of `toFixed`, which rounds the exact *binary* value: `-99.85` is
really `-99.8499999999999943`, so `toFixed(1)` gives `-99.8` where the backend
gives `-99.9`. Over 60,002 constructed decimals, `toLocaleString` disagreed on
**0** at one decimal place and `toFixed` on **960**. `toLocaleString` diverges
too at two and three places, where Sharpe and HHI live. So `format.ts` **ports**
the backend's rounding rather than calling a one-liner, and two parity tests
guard it: `format.test.ts`'s table owns the rounding rule, and
`insights/parity.live.test.ts` owns which formatter is applied to which `Kind`.
Neither catches the other's faults — verified by mutation.

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

**Symbols and finding codes are interpolated into placeholder token names**,
and nothing in `internal/narrative` constrains them — they arrive over HTTP from
`trading-engine`. `safeName` drops anything outside `[A-Za-z0-9._-]{1,32}`, so a
symbol containing a brace cannot split one token into two and a symbol
containing a sentence cannot be injected into the prompt as data. Unreachable
today because a position only exists for a symbol `market-data` could price, but
that constraint lives two services away. Found in the pre-merge review.

**Careless `pkill -f` patterns kill sibling services.** In Step 21 one killed
`market-data` mid-measurement and produced an 8ms reading that looked like a
spectacular result. Check what is still listening before trusting a number.

**A `go run` service started before a code change keeps serving the old
binary.** Kill the process tree and restart. `vite` is the exception.

**And a passing `/healthz` proves a server is there, not that it is yours.**
`pkill -f "ai-insights/cmd/server"` matches nothing, because `go run` compiles
to a temp binary whose process is named `server`. In Step 23 the old process
kept port 8085, the replacement died with `bind: address already in use`, and
ten minutes of measurements came off the wrong build and read as the fix having
failed. Confirm the log line says `listening`, and that the PID from
`lsof -nP -iTCP:<port> -sTCP:LISTEN` is the new one. Killing by that PID is also
the version of `pkill` that cannot reach a sibling service.

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
so browser-driven verification has to go through the login form. Two
consequences found in Step 22: browser automation cannot reach a tab you opened
by hand (the extension only drives its own tab group), and **adding a `useRef`
to a mounted component cannot hot-reload** — React Fast Refresh raises
"Rendered more hooks than during the previous render", the page goes blank, and
the full reload it needs costs another sign-in.

**A `git checkout --` revert inside a mutation driver silently discards an
uncommitted fix in the same file.** It happened twice in Step 22, and both times
it was caught only because mutations that had previously reported `build=PASS`
started reporting `build=FAIL`. Restore from a copy of the pre-mutation file,
not from `HEAD`, whenever the tree carries uncommitted work.

**`vitest` does not typecheck.** A `@ts-expect-error` proves nothing under
`vitest run` — confirm it with `tsc`, and confirm it by *removing* the
suppression and watching the error appear.

**A test that passes against real data may simply not discriminate.** Mutating
`fixed()` to `toFixed` leaves Step 22's live parity file entirely green, because
no figure a real portfolio produces lands on a halfway case. That is not a weak
test; it is the wrong test for that fault. Know which fault each test owns.

**Summing a map in Go is not deterministic, and no displayed figure will tell
you.** Fixed in Step 23, and worth keeping in mind for the next accumulation
anyone writes here. Map iteration order is randomized per pass and float64
addition is not associative, so the same values summed over `range m` differ in
their last bits between runs. Every figure rounds that away; `ReportHash` is
defined on the serialized bytes and saw all of it, at 9 distinct hashes over 10
live recomputes. **A tolerance-based test cannot catch this** -- the existing
order test compares at `eps = 1e-9` and the drift is around 1e-11. If you write
another one, compare `math.Float64bits`.

**A stability fixture built on round numbers proves nothing.** `bars()` closes
at whole dollars, which are exact in binary, and Go's small-map randomization
produces rotations rather than permutations, so seven rotations of seven exact
values agree. The first Step 23 fixture reported 1 distinct equity curve over
200 runs against **broken** code. Rounding the closes to cents took the same
test to 199. Use `driftBars`.

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
