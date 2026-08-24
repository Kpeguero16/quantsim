# Next session — state of play

> **Step 26 is merged.** Nothing is half-finished, but one thing is **blocked**: provisioning
> needs an AWS account that does not exist yet. Everything up to it is built and verified.
> The per-task record is archived at `docs/archive/phase4-step26-cloud-deployment/todo.md`.

Last updated **2026-08-23**, with Step 26 (cloud deployment readiness) merged. **Phase 4's
feature work and all its defects are done. What remains of the roadmap is one EC2 instance.**

This file answers three questions on picking the project back up: *is anything half-finished?*,
*what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not
appended to.

---

## Where Step 26 stopped, and why

| | |
|---|---|
| Built and verified | one origin (Caddy serves the bundle and proxies the API), relative URLs so the bundle names no host, `CORS_ALLOWED_ORIGIN`, a healthcheck binary for the distroless images, `TRUSTED_PROXIES`, the production overlay, `infra/deploy/*`, `docs/DEPLOYMENT.md` |
| **Blocked** | provisioning. No AWS account. Creating one is not something I can do |
| Backend | `make vet` clean, `make test` green, `make test-integration` **63/0**, `GOWORK=off` **8/8** (the healthcheck module is the eighth) |
| Mutations | `clientip.go`: **10 run, 10 killed** |
| Cost | **$0.00** |
| Dev database | restored and **verified by query**: `users=20 accounts=20 trades=0 orders=0 positions=0 backtests=0`, `historical_prices=3525` |
| Local processes | none. Two containers, Postgres and Redis |

---

## What Step 26 did

**One origin.** Caddy serves the compiled bundle and proxies `/auth`, `/market-data`, `/trading`,
`/backtests` and `/insights` to the gateway, in **both** environments. Two of Step 25's three
blockers were consequences of having two origins and stopped existing rather than being solved:
the bundle now contains no API host, and nothing crosses origins.

**And it broke the rate limiter, silently.** `clientIP` reads `r.RemoteAddr` and deliberately
nothing else. With a proxy in front that is the proxy's address on every request, so the per-IP
limiter works perfectly on **one key for the entire internet** — 100 requests per 15 minutes
shared by everybody. `TRUSTED_PROXIES` is empty by default and, when the peer is trusted, the
client is the **rightmost** `X-Forwarded-For` entry: the only one a client cannot forge. Measured
with a control that reproduces the shared bucket exactly.

**Healthchecks exist now**, via a tiny binary copied into every distroless image. Visibility, not
recovery: Docker does not restart an unhealthy container.

---

## What to do next

**1. Provision the instance.** `docs/DEPLOYMENT.md` is the runbook end to end. The short version:
create the AWS account and an IAM user, `aws configure` (Khalil types the keys), t3.micro with
**swap allocated first**, a security group of 22/80/443 and nothing else, an Elastic IP, an A
record for `quantsim` at `khalilpeguero.me`'s DNS, secrets into SSM, then
`infra/deploy/deploy.sh`. The domain question is settled: the apex keeps serving GitHub Pages and
the subdomain is a separate record.

**2. Nothing restarts a hung service.** `restart: unless-stopped` covers a crash; the healthchecks
report a hang and Docker does not act on it. Worth choosing a watchdog after something has
actually hung rather than before.

**3. No automated backups.** Losing the instance loses the database. `pg_dump` is in the runbook
and is manual. Restore from one deliberately, before it matters.

**4. Consolidate the four Redis client construction sites.** `ai-insights` and `trading-engine`
set `ContextTimeoutEnabled`; **`auth` and `market-data` do not**, so `context.WithTimeout` around
their Redis calls does nothing. The defect that cost 6.05s in Step 21, latent in two services.
Its own step, because it touches auth's token revocation path. Untouched by Steps 25 and 26.

**5. `docs/deferred-tuning.md`** — still blocked until something is deployed and has traffic.

**6. The long-standing small items.**

- **The frontend hooks have no tests at all.** `use-narrative`'s double-spend guard protects a
  billed call and broke in Step 22 without a single test noticing. Needs `renderHook`;
  `@testing-library/react` is installed and still unused.
- `market-data`'s store still has no tests (`historical_price_store.go`). The integration harness
  is still in **three** copies; `docs/TESTING_STRUCTURE.md` §6a's extraction trigger is **still
  unfired**.

**7. Security backlog:** items 1, 2 and 4 are closed. Item **8** (Unicode-normalise passwords) is
the cheap one left. Item **3** (Argon2id) wants its own step, since it carries a migration.

---

## Restarting the environment

Two ways, and they publish the same port, so run one or the other.

**Everything in containers.** Needs only Docker and a `.env`:

```bash
make stack-up             # builds and starts all 9 containers
make stack-logs
make stack-down
```

Then http://localhost:5173. **Only 5173 is published** (plus Postgres and Redis on loopback):
Caddy proxies the API, so the gateway is not reachable from the host at all.

**Services on the host** — the development loop:

```bash
make docker-up            # Postgres + Redis only
make run-auth             # :8081
make run-market-data      # :8082
make run-trading-engine   # :8083
make run-backtesting      # :8084
make run-ai-insights      # :8085
make run-gateway          # :8080
make run-frontend         # :5173, and it proxies the API to :8080
```

Each `run-*` target runs in the foreground, so they need separate terminals.

`ANTHROPIC_API_KEY` is in `.env` and **optional** — without it the report endpoint is unaffected
and the narrative endpoint returns 200 with `narrative: null`. `ANTHROPIC_MODEL` defaults to
`claude-opus-5`.

**Without `REDIS_URL` the narrative endpoint returns nothing, deliberately.** No Redis means no
cache *and* no generation counter, and uncached plus uncapped is the one combination with no cost
ceiling. The report endpoint still works. `REDIS_URL` also reaches trading-engine, where it is
optional and buys Step 24's report invalidation on a fill.

Auth rate limiting is **on by default** (100 requests / 15 min per IP; backoff after 5 consecutive
failed logins). `RATE_LIMIT_ENABLED=false` turns it off. **Do not put `PORT=` in `.env`** — the
Makefile exports that file to every target.

**Register a fresh password with something that isn't your username, email, or "quantsim."**
Registration also requires a `username`.

**The `migrate` CLI** is not needed for the container stack, which runs migrations as a one-shot.
The schema is still at version 9.

---

## Things that will trip you up

**Do not put a second proxy in front of Caddy** — an ALB, Cloudflare, anything — without
revisiting `services/gateway/internal/middleware/clientip.go`. The rightmost `X-Forwarded-For`
entry stops being the client and becomes the inner proxy, and the per-IP limiter goes back to one
shared bucket for everybody. Nothing fails; it just stops protecting anyone.

**`no-new-privileges` and file capabilities are mutually exclusive.** Any image whose binary
carries `setcap` capabilities crash-loops under it with `exec ...: operation not permitted`, which
names neither the cause nor the fix. Strip the capability, do not drop the sandbox.

**`!reset` on a list in a compose overlay removes the key AND the entries under it.** It looks
like the way to replace an appended list and silently produces nothing. `!override` is the tag
that replaces.

**A healthcheck pinned to a port that moves between environments is worse than none.** It reports
unhealthy forever and buries every real failure in the noise. The frontend's lives on an internal
`:5199` for exactly that reason.

**`/backtests` has to be listed twice** in both the Caddyfile and the Vite proxy, bare and
prefixed. The collection is requested at exactly that path and a prefix pattern does not match it.

**A stack pointed at the wrong database looks completely healthy.** Migrations that run
automatically create the schema wherever they are aimed, so registration returns 201 and orders
fill against an empty database. `POSTGRES_APP_DB` names the right one; `POSTGRES_DB` is an empty
decoy called `quantsim`.

**`docker compose down` does not stop services behind a profile.** The containers it leaves hold a
reference to the network it removed, and the next `stack-up` fails with `network <hash> not
found`. `make docker-down` now takes down every profile.

**Right after `make stack-up` the first price request can 404.** `price:{symbol}` carries roughly
a 40-second TTL and market-data repopulates it on a loop.

**A user that has run a backtest cannot be deleted by the obvious five deletes.**
`backtests_user_id_fkey` rolls the whole transaction back, and it reads as "nothing happened".

**Opening the insights tab spends money.** The narrative fires from a mount effect, not a button.


**A stack pointed at the wrong database looks completely healthy.** Step 25's own defect.
Migrations that run automatically will create the schema wherever they are aimed, so registration
returns 201 and orders fill against an empty database. `POSTGRES_APP_DB` is what names the right
one for containers, and `POSTGRES_DB` is *not* it — that names an empty decoy called `quantsim`.

**`docker compose down` does not stop services behind a profile.** The containers it leaves hold a
reference to the network it just removed, and the next `stack-up` fails with
`network <hash> not found`, which names nothing that leads back to the cause. `make docker-down`
now takes down every profile; if you hit this some other way, `docker compose --profile app down`
is the cure.

**Right after `make stack-up` the first price request can 404.** `price:{symbol}` carries roughly
a 40-second TTL and market-data repopulates it on a loop; before the first tick lands there is no
cached price and `POST /trading/orders` is refused with `symbol_unavailable`. Wait a few seconds.

**A user that has run a backtest cannot be deleted by the obvious five deletes.**
`backtests_user_id_fkey` rolls the whole transaction back — correct, and it reads as "nothing
happened" if you do not read the output. Delete `backtest_trades` and `backtests` first.

**Opening the insights tab spends money.** The narrative fires from a mount effect, not a button.

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
