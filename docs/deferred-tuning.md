# Deferred tuning — revisit under real traffic

Items deliberately left alone because the right value depends on traffic shape
we do not have yet. None are bugs; all are defaults that are fine for local
development and wrong-ish for a deployed service.

**Revisit when:** Phase 4 puts QuantSim on AWS and there is real request
volume to measure. Guessing now would bake in numbers with nothing behind
them — the point of deferring is to set these from observed behaviour rather
than from taste.

---

## 1. Gateway HTTP server has no `IdleTimeout`

**Where:** `services/gateway/cmd/server/main.go` — the `http.Server` literal.

**Now:** `ReadHeaderTimeout: 10s` and, since Step 27, `WriteTimeout: 60s`.
Everything else is at Go's defaults.

**Why `WriteTimeout` could not wait:** the per-backend connection cap in item 2
made a client that stops reading able to hold a slot, so it stopped being tuning.
60s is twice the proxy's 30s `ResponseHeaderTimeout` rather than a measured number,
so revisit it against the p99 below.

**Why it matters later:** `IdleTimeout` is unset, and when it is zero Go falls
back to `ReadTimeout`, which is also zero — so idle keep-alive connections are
never reaped. A client can hold connections open indefinitely. On a laptop
that is nothing; on a public instance it is a cheap way to exhaust file
descriptors.

**Likely fix:** add `IdleTimeout` (~120s). Deliberately **do not** set
`ReadTimeout` — it caps the whole request including the body, which would break
large uploads and any future streaming endpoint.

**What to measure first:** p99 backend response time (the `WriteTimeout` floor)
and steady-state idle connection count.

---

## 2. Proxy transport connection limits: resolved in Step 27

**Where:** `services/gateway/internal/proxy/proxy.go`, `NewTransport`.

**Now:** `MaxConnsPerHost` and `MaxIdleConnsPerHost` are both 128, and
`MaxIdleConns` is 0 (no total limit, so the real bound is backends x 128).

**This was a bug, not just tuning, and this file underrated it.** It predicted
"added per-request latency under concurrency." A load test measured something
worse: at 100 concurrent clients, 60.5% of price reads returned 502
(`connect: can't assign requested address`). A connection the pool cannot keep
is closed and holds an ephemeral port in TIME_WAIT for 30s, and macOS has 16,384
of those ports, so sustained churn above about 546 connections/s runs out. Linux
defaults (28,232 ports, 60s) give a similar ceiling of about 470/s, so this was
not a laptop artifact.

**Raising the idle limit alone only moves the failure.** The `MaxIdleConnsPerHost: 32`
this entry used to suggest would not have survived 100 clients. 128 idle with no
cap held at 200 clients and failed again at 500, with 7-82% of requests returning
502. The cap is what fixes it. With the two limits equal, a finished request
always has a place in the pool, and a request above the cap waits for a
connection instead of dialing.

| Price reads through the gateway | 100 clients | 500 clients | 1,000 clients |
|---|---|---|---|
| Default (2 idle per host) | 1,343 ok/s, 60.5% errors | not run | not run |
| 128 idle, no cap | 23,895 ok/s, 0 errors | 7-82% errors | 16-92% errors |
| 128 idle, 128 cap (shipped) | 25,715/s, 0 errors | 30,605/s, 0 errors | 31,145/s, 0 errors, 44 ms p99 |

Measured on one 10-core laptop over localhost, closed-loop clients, median of 3
runs. 5.3M requests against the shipped config produced no proxy errors. Bursts
of 500 simultaneous orders went from 214 of 10,000 returning 502 to none.

**What the cap costs:** a request above the cap waits for a connection, and that
wait is bounded by the client rather than by `responseHeaderTimeout`. If a backend
hangs, each batch of 128 takes 30s to fail, so a long queue can wait for minutes
unless the client gives up first. It stays within that backend:
`TestSaturatedBackendQueuesOnlyItsOwnRequests` proves the other backends are
unaffected and that a client giving up gets out.

**The cap made a slot worth stealing.** Anything that holds a backend connection
now denies it to everyone else on that backend. Measured against the real gateway
binary with 128 hostile connections and a 5s victim request:

| Attack | `main` | Cap alone | Shipped |
|---|---|---|---|
| Stalled upload to `/trading/orders` | 200, at most 2 ms | timed out, past 42s | 200, at most 3 ms |
| Stalled upload to `/auth/register`, victim logging in | 200, at most 13 ms | timed out, past 42s | 200, at most 2 ms |
| Client stops reading an 8 MB response | 200, at most 105 ms | timed out | timed out until `WriteTimeout`, recovered by 73s |

`/auth/*` needs no account, and its 100-per-IP budget means two addresses are
enough, so the cap alone let anyone lock every user out of login. Two controls
answer it. `BufferBody` reads the capped body before a request can take a
connection, which removes the upload case outright. `WriteTimeout` (item 1) bounds
the read case.

**Still open:** the read case is bounded, not removed. An authenticated client can
reopen its connections every 60s and keep a backend starved, provided it can get
a response too large for the socket buffers. A 500 KB response did not hold a slot
on macOS, and market-data caps history at 2,000 bars, so this needs measuring on
Linux, whose buffers start smaller. Client-held slots end only with a per-user
limit on in-flight requests, which is the fix if this is ever seen in practice.

**Revisit if WebSockets go through this transport.** Nothing long-lived does today,
but `agents.md` plans them, and each one would hold a slot for its whole life. 128
of them would block every other request to that backend. Give them their own
transport rather than raising this cap.

---

## 3. Index migrations lock the table they build on

**Where:** `infra/migrations/004_case_insensitive_identity.up.sql` (creates two
unique indexes) and `005_drop_redundant_unique_constraints.down.sql` (re-adds
two unique constraints, which builds indexes to back them).

**Now:** `CREATE UNIQUE INDEX` and `ALTER TABLE ... ADD CONSTRAINT ... UNIQUE`
both take an `ACCESS EXCLUSIVE` lock for the duration of the build, blocking
reads and writes to `users`. On 15 rows this completes in milliseconds and is
genuinely irrelevant. Against a real dataset it is a write outage for as long
as the build takes.

**Why it is not already fixed — the trade is real, not an oversight.** The
production answer is `CREATE INDEX CONCURRENTLY`, which **cannot run inside a
transaction block**. Under golang-migrate that means the `-- no-transaction`
directive, and that forfeits the all-or-nothing rollback these migrations
currently rely on: `004`'s dry run specifically verified that a failure at the
index leaves the preceding `UPDATE` rolled back rather than half-applied. A
concurrent build also fails *asynchronously*, leaving an `INVALID` index behind
that has to be dropped and rebuilt by hand.

Trading a clean rollback for a non-blocking build is the right call once there
is data worth protecting, and the wrong one while the whole table fits on a
screen.

**What to measure first:** row count in `users`, and whether any deploy window
tolerates a brief write pause. Below roughly 10k rows this is noise.

**When it changes:** the first migration that adds an index to a table with
meaningful volume — realistically Phase 2's orders or trade-history tables, not
`users`.

---

## 4. Rate-limit counters are per-process

**Where:** `services/gateway/internal/limiter/memory.go`, chosen in `SPEC.md`
(Step 11) §2.1.

**Now:** the gateway counts authentication attempts in its own memory. One
gateway runs, so the count is the truth.

**Why it is not Redis:** the gateway had no Redis dependency —
`services/gateway/go.mod` required only `chi` and `pkg`, and Step 7 §8 listed
any new dependency under *ask first*. In-memory also removes a decision that
has no good answer: if a shared store is unreachable, failing closed locks
every user out of login and failing open silently stops limiting. An
in-process store cannot be unreachable.

**The trade:** counters do not cross processes. Two gateway instances would
each hold their own, so the effective limit is the configured one multiplied
by the instance count. `limiter.Store` is an interface precisely so a Redis
implementation drops in without touching middleware or handlers.

**What to measure first:** nothing to measure — this is a topology question,
not a load question. The moment more than one gateway serves traffic, the
limit is wrong by a known factor.

**When it changes:** **the second gateway instance.** Horizontal scaling, a
blue/green deploy that runs two at once, or any load balancer in front of more
than one process.

---

## 5. The per-IP limiter keys on `RemoteAddr`, which breaks behind a proxy

**Where:** `clientIP` in `services/gateway/internal/middleware/ratelimit.go`,
decided in `SPEC.md` (Step 11) §2.5.

**Now:** the gateway is the edge. `r.RemoteAddr` is the real client, and
forwarding headers are client-authored and therefore untrusted — reading them
would make the limiter bypassable. This is correct **only** while nothing sits
in front of the gateway.

**What breaks when something does:** behind an ALB or any reverse proxy, every
request arrives from the proxy's address. All traffic collapses onto one key,
one shared budget, and the limiter starts refusing everyone at once — a
self-inflicted outage rather than a security hole, but an outage.

**Shape when it changes:** trust a forwarding header **only** from known proxy
addresses — parse `X-Forwarded-For` right-to-left, skipping entries
contributed by trusted hops, and take the first untrusted one. The trusted set
must be configured explicitly; a limiter that trusts the header
unconditionally is the bug this whole design avoids.

**When it changes:** **Phase 4 deployment behind a load balancer.** Do it in
the same change that introduces the proxy, never before — an untriggered
"forward-compatible" version of this is just the bypass, early.

---

## 6. Integration tests reuse the dev Postgres instead of testcontainers

**Where:** `services/auth/integration/`, chosen in `SPEC.md` (Step 12) §2.1.

**Now:** the store suite connects to the Postgres that `make docker-up`
already runs, against a dedicated `quantsim_test` database it drops and
recreates each run. Correct and fast on a development laptop.

**Why not testcontainers:** there is **no CI anywhere in this repo** — no
`.github/`, nothing. Testcontainers' main advantage is a self-contained
ephemeral database that CI can start without any host setup, which is
currently an advantage over nothing. It would also add a heavyweight
dependency to `services/auth` and container startup to every run.

**The trade:** the suite needs a running Postgres and depends on host state
(the compose stack, `.env`). It cannot run on a machine that has only a Go
toolchain. And it is the developer's own database server, so the guards
described in `docs/TESTING_STRUCTURE.md` §6a are what stand between the suite
and real data — a design with an ephemeral container would not need them.

**What to measure first:** nothing to measure. This is an environment
question, not a performance one.

**When it changes:** **the first CI pipeline that needs to run
`make test-integration`.** At that point testcontainers (or a service
container in the CI config) stops being optional. The harness is structured so
only `ensureTestDatabase` and DSN resolution change; no test moves.

---

## 7. Migrations are applied to the test database by exec'ing the SQL files

**Where:** `applyMigrations` in `services/auth/integration/harness_test.go`,
chosen in `SPEC.md` (Step 12) §2.4.

**Now:** the harness globs `infra/migrations/*.up.sql`, sorts by filename, and
executes each with the pgx pool it already holds. Verified at the time: every
migration is plain SQL, 1–5 statements, with no golang-migrate directives.
This keeps `services/auth/go.mod` unchanged — Step 7 §8 makes any new
dependency an ask-first decision, and Go does not mark test-only dependencies
differently in the module graph.

Version tracking and dirty-state recovery buy nothing here because the
database is recreated on every run, so migrations always apply exactly once
against an empty database.

**The trade:** this is a second, simpler implementation of something
golang-migrate already does, and the two could diverge. Concretely, it ignores
anything migrate would interpret rather than execute — `-- no-transaction`
above all.

**When it changes:** **the first migration that needs a golang-migrate
directive.** §3 above makes this a live possibility rather than a
hypothetical: `CREATE INDEX CONCURRENTLY` cannot run inside a transaction
block, which under golang-migrate means `-- no-transaction`. The moment a
migration carries one, this loop applies it as ordinary SQL and the test
schema stops matching what `make migrate-up` produces. Switch to the migrate
library in the same change.

---

## 8. Refresh-token revocation is a denylist, not rotation with reuse detection

**Where:** `SPEC.md` (Step 13) §2.1, closing `docs/security-backlog.md` item 2.

**Now:** `POST /auth/logout` writes the token's `jti` to Redis with a TTL
equal to its remaining lifetime; `Refresh` checks it. A refresh token is not
itself rotated or revoked by a successful `Refresh` call — it stays valid,
reusable, until it expires or is explicitly logged out.

**The trade:** this closes "there is no kill switch," which is what the
backlog item asked for. It does not add *theft detection* — rotation with
reuse detection would additionally notice when an already-used refresh token
is presented again (a strong signal of a stolen token) and revoke the whole
token family. A denylist alone cannot tell "the legitimate owner refreshed
twice" apart from "someone else has a copy and is also refreshing."

**Why not now:** rotation touches `frontend/src/api/client.ts`'s shared
in-flight-refresh promise (module docstring, `client.ts:1-26`), which today
tolerates duplicate refreshes as harmless. Under rotation, duplicate refreshes
burn tokens from the same family and look exactly like theft — the shared
promise stops being an optimization and becomes a correctness requirement,
and the client needs a new codepath to distinguish "expired, retry is fine"
from "theft detected, force a real sign-in." Bigger and riskier than the
problem being solved right now.

**When it changes:** the threat model needs theft *detection*, not just a way
to end a session — e.g. evidence of token exfiltration in practice, or a
compliance requirement for it. The `jti` infrastructure this step adds is the
prerequisite either design needs, so this is additive later, not a rewrite.

---

## 9. `orders` has no index on `account_id`

**Where:** `infra/migrations/006_*.up.sql` — Step 14's migration, and the
`ListOrders` query in
`services/trading-engine/internal/store/postgres_trading_store.go`.

**Now:** `GET /trading/orders` runs `WHERE account_id = $1 ORDER BY created_at
DESC, id DESC` against an unindexed column, so it is a sequential scan of the
whole table filtered down to one account's rows. Every rejected order is persisted
too (that is deliberate — the audit trail is worth more than a small table),
so the table grows faster than the fill rate alone suggests: Step 14's own
adversarial review wrote 103 orders in an afternoon, of which only 73 were
fills. (Those rows were deleted before the merge, so the table is empty today
— the number is the growth rate, not the current size.)

**Why it was left out:** the spec scoped migration 006 to exactly four columns,
and the plan treats anything beyond that scope as an "ask first" boundary
(`docs/archive/phase2-step14-trading-engine-mvp/plan.md`, decision D4). Adding
an index nobody had measured a need for would have been the plan quietly
widening its own mandate. At current row counts the scan is unmeasurable.

**When it changes:** whichever comes first —
- `orders` passes ~10k rows, or
- `GET /trading/orders` shows up as slow with a real user's history behind it.

**Likely fix:** `CREATE INDEX CONCURRENTLY idx_orders_account_id_created_at ON
orders (account_id, created_at DESC, id DESC)` — composite and in that order,
so it serves the filter and the sort together rather than forcing a sort after
the lookup. `CONCURRENTLY` for the reason item 3 in this file already
documents. The same question applies to `trades(account_id)`, which is
unindexed for the same reason; `positions` needs nothing, since its
`UNIQUE(account_id, symbol)` constraint already provides the index its lookups
use.

---

## 10. Money is `float64` in Go and `NUMERIC(20,4)` in Postgres

**Where:** every money and quantity field in `services/trading-engine` —
`Balance`, `Price`, `Quantity`, `AvgCost`, `RealizedPL`, `TotalEquity`.

**Now:** Postgres is the authority and stores exact decimals; Go computes in
binary floating point and hands back a value that is rounded to 4dp on write.
The two disagree by less than half a tick per operation, and the store's
integration tests read money as `::text` precisely so that any real precision
loss becomes visible instead of being rounded away by the assertion.

**The measured consequences**, both from Step 14's adversarial review:

1. **A derived response field can show the artifact.** A portfolio whose parts
   are exactly 100000 reported `"total_equity": 99999.99999999999`. Nothing
   stored is wrong — the sum is computed in Go across positions and cash — but
   it is visible in the API, and a frontend that formats it naively will show
   it to a user.
2. **A sub-tick residual on the cash leg of every fill.** Measured at
   **+0.0000345 per fill** across 30 tick-sized sells at 305.655: the proceeds
   `0.0305655` cannot be represented at 4dp, so the account is credited
   `0.0306`. It is bounded by half a tick, it is not reliably one-directional
   across prices, and it is not profitable to farm — the caller gives up
   0.0306 of stock to gain 0.0000345 of cash.

**What is *not* deferred:** the same review found a far worse version of this,
where a quantity below the ledger's tick was charged for in full and then
rounded to **zero shares** — the cash leg landing while the share leg vanished,
which minted money on the sell side. That was a bug, not a tuning question, and
it was fixed in Step 14 (`00cb7ba`) by making `0.0001` the minimum quantity and
snapping finer ones to the tick. See `PHASE2_CHECKLIST.md` Step 14. **The
distinction matters:** a bounded rounding residual is a trade-off; an entire
leg of a transaction disappearing is not, and no amount of "money is float64"
context makes the second one acceptable.

**When it changes:** whichever comes first —
- a user-visible number is wrong by more than a cent, or
- P/L has to reconcile against anything external, or
- fractional-share quantities finer than 0.0001 are wanted.

**Likely fix:** integer minor units (store cents/ten-thousandths as `int64`) or
a decimal type such as `shopspring/decimal`, applied at the service boundary so
Postgres stays the authority it already is. Both are a wide change — every
struct field, every JSON contract, and both test suites — which is exactly why
it wants its own step rather than a corner of one.

---

## 11. The Postgres integration-test harness is now a third copy, not extracted

**Where:** `services/auth/integration/`, `services/trading-engine/integration/`,
and as of Step 16, `services/backtesting/integration/` — `harness_test.go`,
`harness_guard_test.go` and the `TestMain`/`newStore` pair in `main_test.go`
are near-verbatim copies across all three.

**Now:** `docs/TESTING_STRUCTURE.md` §6a names "a third service needing it"
as the trigger to extract this to `pkg/testutil/`, and had predicted
`market-data`'s `historical_price_store.go` would be the third. It was
`backtesting` instead — SPEC.md (Step 16) §2.6's store needed the same
real-Postgres treatment first. The trigger has fired; the extraction has not
been done.

**Why not done in Step 16:** the extraction is explicitly cross-cutting —
§6a's own text says to "port all three call sites in the same change," which
means touching already-shipped, working test files in `auth` and
`trading-engine` as a side effect of a step whose `SPEC.md` scope was a new
service. That is a bigger, differently-reviewed change than "add a new
service's tests," and bundling it in would have made this step's diff harder
to review for what it actually set out to do.

**What to measure first:** nothing — this is a structural call, not a
performance one. The three copies are still verified against each other by
`diff`, per §6a's existing "when you change the harness, change both (now
three) copies" rule.

**When it changes:** the next time any of the three harness files needs a
real edit (not just a per-service seed helper or table-list addition) is the
natural moment to extract instead of editing three files in lockstep.
`market-data`'s own store still has no integration tests at all, and adding
those later would otherwise create a fourth copy — extract before that happens
rather than after.

---

## 12. trading-engine's market-data client is on Go's default connection pool

**Where:** `services/trading-engine/internal/client/market_data_client.go`,
which builds `&http.Client{Timeout: requestTimeout}` and so inherits 2 idle
connections per host. Every order fetches a price through it.

**Now:** no failures measured, but it is the same mechanism as item 2. At about
2,200 orders/s with 100 clients, roughly 11,000 of the 16,384 ephemeral ports sit
in TIME_WAIT toward market-data, about 370 new connections per second. A faster
order path or a lower port budget turns that into 502s.

`backtesting` builds its client the same way. It showed no churn at 10 concurrent
clients and has not been measured higher.

**Likely fix:** the same shape as item 2, a transport with `MaxConnsPerHost` and
`MaxIdleConnsPerHost` equal.

**What to measure first:** nothing further. The measurement above is enough to act on.

---

## Related decisions recorded elsewhere

- **Graceful shutdown** — none of the three services drain on SIGTERM
  (market-data's poller included). Recorded in
  `docs/archive/phase1-step6-market-data-live/SPEC.md` §2.9. Becomes real when
  deploys start rolling rather than restarting.
- **Security gaps — moved to `docs/security-backlog.md`.** Rate limiting and
  service-to-service auth used to be listed here. They now live in their own
  register, along with refresh-token revocation, the bcrypt 72-byte ceiling,
  body-size limits beyond the auth routes, and TLS. They were a poor fit for
  this file: its own framing is *"None are bugs"*, and its trigger is Phase 4
  traffic — whereas several of those **are** gaps, and the first ones are due
  in Phase 2. Tracking them in one place beats maintaining two partial lists.
