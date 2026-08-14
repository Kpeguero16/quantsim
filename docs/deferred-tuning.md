# Deferred tuning — revisit under real traffic

Items deliberately left alone because the right value depends on traffic shape
we do not have yet. None are bugs; all are defaults that are fine for local
development and wrong-ish for a deployed service.

**Revisit when:** Phase 4 puts QuantSim on AWS and there is real request
volume to measure. Guessing now would bake in numbers with nothing behind
them — the point of deferring is to set these from observed behaviour rather
than from taste.

---

## 1. Gateway HTTP server has only `ReadHeaderTimeout`

**Where:** `services/gateway/cmd/server/main.go` — the `http.Server` literal.

**Now:** `ReadHeaderTimeout: 10s`, everything else at Go's defaults.

**Why it matters later:** `IdleTimeout` is unset, and when it is zero Go falls
back to `ReadTimeout`, which is also zero — so idle keep-alive connections are
never reaped. A client can hold connections open indefinitely. On a laptop
that is nothing; on a public instance it is a cheap way to exhaust file
descriptors.

**Likely fix:** add `IdleTimeout` (~120s) and a `WriteTimeout` chosen to sit
above the slowest legitimate backend response. Deliberately **do not** set
`ReadTimeout` — it caps the whole request including the body, which would break
large uploads and any future streaming endpoint.

**What to measure first:** p99 backend response time (sets the `WriteTimeout`
floor) and steady-state idle connection count.

---

## 2. Proxy transport `MaxIdleConnsPerHost` is 2

**Where:** `services/gateway/internal/proxy/proxy.go` — `NewTransport`.

**Now:** clones `http.DefaultTransport`, inheriting `MaxIdleConnsPerHost: 2`.

**Why it matters later:** with only two backends, more than two concurrent
in-flight requests per backend forces a fresh TCP handshake per request. The
transport is shared across proxies specifically so connections pool; a limit of
2 means it barely does. Irrelevant at one-developer volume, visible as added
per-request latency under concurrency.

**Likely fix:** `MaxIdleConnsPerHost: 32` or so, tuned against measured
concurrency. Raising it costs idle sockets, so it is worth a number rather than
a guess.

**What to measure first:** concurrent in-flight requests per backend at peak,
and the ratio of new connections to reused ones.

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
