# QuantSim Phase 2 — Trading Engine Checklist

**Phase 2 complete as of Step 15.** Phase 3 (backtesting engine) continues
in `PHASE3_CHECKLIST.md`, starting at Step 16.

Phase 1 is complete (`PHASE1_CHECKLIST.md`). Phase 2 delivers the trading
engine — order execution, trade storage, P/L tracking — and opens with the
security work that Phase 2 itself makes consequential.

**Why security comes first.** Today account takeover buys a read-only view of
public market data. Once `/trading/*` executes orders against a $100k
simulated balance, the same weakness lets someone trade as another user. The
auth surface does not get weaker in Phase 2; the consequences of its existing
gaps get materially worse. Reasoning in `docs/security-backlog.md`.

**The security work landed first, and then the engine did.** Steps 11, 13 and
14 closed backlog items 1, 2 and 4; Step 14 shipped `/trading/*` itself. The
paragraph above is no longer a forecast — orders execute against real balances
today, and they did not before the three gaps that made that consequential were
closed.

---

## Step 11: Auth Rate Limiting

Closes `docs/security-backlog.md` item 1 — the largest remaining gap in the
auth surface. Nothing throttled `/auth/login` before this: no per-IP limit, no
per-account limit, no backoff. Step 9's password rules raised the cost *per
guess*; nothing bounded the *number* of guesses, which is what actually
defeats credential stuffing against reused passwords.

- [x] Fixed-window counter store, sharded, with eviction and an injected clock
- [x] Exponential backoff schedule over consecutive failures, always decaying
- [x] Per-IP limiting on `/auth/*`, keyed on `r.RemoteAddr`
- [x] Per-account backoff on `/auth/login`, keyed on the submitted email
- [x] `docs/security-backlog.md` item 1 corrected and closed
- [x] Two `docs/deferred-tuning.md` entries (§4, §5) with named triggers
- [x] Pre-merge review: two bypasses found and fixed

**Completed 2026-08-14.** Spec at `SPEC.md`, checkpoints in `tasks/plan.md` /
`tasks/todo.md`. No new module dependency; no change to `services/auth/`.

### What pre-merge review found

Both were **invisible to a green test suite**, which is the point worth
carrying forward: for a control like this, "the tests pass" is evidence of
very little on its own.

**1. Trailing data skipped per-account limiting entirely** (`4bf43e6`). The
gateway parsed the login body with `json.Unmarshal`, which rejects trailing
bytes; the auth service uses `json.NewDecoder().Decode()`, which parses the
first JSON value and ignores the rest. Appending a single junk byte made the
gateway fail to extract an account key — so backoff never ran — while auth
parsed the same body and processed the login normally. Measured at a threshold
of 3: a well-formed body was refused after 3 attempts; the same body plus one
trailing `x` let **10 of 10** through.

*The rule it produced:* **the gateway must never be stricter than the service
it protects.** Anything the backend will act on has to be something the
limiter can key. Being more lenient is safe; being stricter is a bypass.

**2. Concurrent bursts outran the counter** (`e6dc4c1`). Backoff checked the
count, proxied, and recorded the failure once the backend answered — leaving
the counter untouched for the whole round-trip, so every request arriving
meanwhile saw a clean slate. Measured through the running gateway with a 150ms
backend: **60 concurrent guesses at a threshold of 5, all 60 reached the
backend, none refused.** Backoff bounded sequential guessing and did nothing
about concurrent guessing — the easy case for the distributed attack it exists
to stop.

Fixed by making check-and-count atomic and optimistic (`Backoff.Attempt`),
corrected afterwards: `401` leaves the count, `200` clears it, anything else
rolls it back. That last branch is what stops a downed auth service from
throttling its own users — verified live: with auth stopped, ten attempts
returned `502` throughout and never escalated to `429`. Same measurement after
the fix: **5 of 60**.

### The two decisions worth remembering

**Backoff, not lockout.** A hard lockout is the stronger control against
guessing and was rejected: anyone who knows a user's email could freeze that
account at will, turning an auth control into a denial-of-service primitive,
and it needs an unlock path that does not exist. The accepted residual risk is
that an attacker can still degrade a known victim's login for up to ~15
minutes — bounded, self-healing, and strictly better than the alternative.

**Counters in memory, not Redis.** The backlog assumed Redis was free because
the stack already runs it. It was not: the *gateway* had no Redis dependency,
and Step 7 §8 required asking before adding one. In-memory also removes a
question with no good answer — a shared store that is unreachable forces a
choice between locking every user out and silently not limiting. The trade is
that counters are per-process, recorded in `docs/deferred-tuning.md` §4 with
its trigger: the second gateway instance.

### The bug that never shipped

The backlog stated that the gateway's `r.SetXForwarded()` call sanitises
inbound `X-Forwarded-For`, so a per-IP limiter could trust that header.
**It does not.** That call runs on `r.Out` inside `proxy.New`'s `Rewrite`,
which executes *after* every middleware and builds only the upstream request.
The inbound header is whatever the client sent.

Building to the backlog as written would have produced a limiter bypassable
with one forged header per request — an unlimited budget from a control that
still looked like it worked, and would have passed a naive test suite. The
limiter keys on `r.RemoteAddr`; the backlog entry is corrected in place.

### On the tests

Twelve tests, two of which are the step rather than coverage of it, both
written before the code and both **verified by mutation** — the check that a
test would actually fail against the wrong implementation:

| Test | Mutation applied | Result |
|---|---|---|
| Forged `X-Forwarded-For` earns no budget | `clientIP` reads the header | 3 forged attempts returned `200` instead of `429` |
| Unknown and known accounts throttle identically | only "real" accounts counted | unknown returned `401` where existing returned `429`, with different bodies and `Retry-After` |

The second is the one that keeps a `429` from becoming the user-enumeration
oracle Step 9 §2.12 deliberately closed. The limiter never consults a
database, so it cannot tell a real address from an invented one — and neither
can a caller.

Verified against the running binary as well as in tests: with a threshold of
3, a real and a nonexistent address both returned `401, 401, 401, 429` with
byte-identical bodies; capitalisation variants shared one counter; a correct
password cleared the count; `/healthz` answered `200` throughout; and the
backend received the full request body every time.

---

## Step 12: Store-Layer Integration Harness

`services/auth/internal/store/` had **zero tests**. All 18 auth test files ran
against `mock.UserStore`, a Go map — they would have stayed green against a
completely wrong SQL query. Step 10's central fix lives in that layer and
shipped with the caveat recorded in `PHASE1_CHECKLIST.md`: *"the verification
was manual, which proves the fix today and protects nothing tomorrow."*

- [x] Harness against a real Postgres, in `services/auth/integration/` behind
      the repo's first build tag
- [x] Dedicated `quantsim_test` database, dropped and recreated each run
- [x] 15 tests over all three store methods
- [x] `make test`, `test-integration`, `test-all`, `test-db-drop`, `vet`
- [x] `var _ service.UserStore` assertions on both the store and the mock
- [x] Two `docs/deferred-tuning.md` entries (§6, §7) with named triggers

**Completed 2026-08-17.** Spec at `SPEC.md`, checkpoints in `tasks/plan.md` /
`tasks/todo.md`. **No query changed, no migration added, no new module
dependency** — `services/auth/go.mod` is untouched.

### The decisions

**Reuse the compose Postgres, not testcontainers.** There is no CI anywhere in
this repo, so testcontainers' main advantage — an ephemeral database CI can
start unaided — is currently an advantage over nothing. Recorded as the upgrade
path in `docs/deferred-tuning.md` §6 with its trigger: the first CI pipeline.

**A denylist plus a check on every write path.** The dev database holds 15 real
users and this harness runs `TRUNCATE`, while the environment actively
misleads: `POSTGRES_DB=quantsim` is an *empty decoy* and `DATABASE_URL` points
at `postgres`, where the real rows live. Both names a careless harness would
grab are wrong, one destructively. `assertTestDB` therefore fails closed twice
over — an absolute `protectedDatabases` list first, then an exact match on
`quantsim_test` — and runs when the DSN is derived, before the `DROP`, after
the pool connects, and immediately before every `TRUNCATE`.

**Rollback forced by numeric overflow.** `accounts.balance` is
`NUMERIC(20,4)`, so a `startingBalance` of `1e16` fails the accounts insert
*after* the users insert succeeded. No schema mutation, so unlike a temporary
`CHECK` constraint there is nothing that can leak into a later test if cleanup
fails.

### What pre-merge review found

**The guards defended against the wrong thing.** All of them compared the
target against `testDBName`, so they caught a bad connection string and nothing
else — editing that constant to `postgres` would have satisfied every check
while the harness dropped and truncated the database holding real users. The
call that looked most protective, `assertTestDB(testDBName)` immediately before
the `DROP`, was the emptiest of all: a constant compared with itself, which can
never fire. **A check that reads as protective but cannot fail is worse than no
check, because it gets believed.**

Fixed with an absolute `protectedDatabases` denylist consulted first, which
makes the constant subject to the guard rather than the yardstick for it.
Verified by poisoning `testDBName` to `quantsim` — the *empty decoy*, chosen so
a guard failure would cost nothing recoverable — and confirming the run aborts
non-zero. The failure being guarded against is unrecoverable, so it is never
reproduced against the real database in order to test it.

Review also confirmed two things by running them rather than assuming:
`TRUNCATE users CASCADE` really does reach `accounts`, `positions`, `orders`
and `trades`; and `make test-db-drop`, which had shipped unexercised, works and
the suite recreates the database afterwards.

### Three bugs found in the harness while building it

Both were mine, both were caught mid-step, and both produced a **green `ok`
while testing nothing** — which is the failure mode this step exists to
eliminate, arrived at twice by different routes:

1. **Migrations were not idempotent.** `ensureTestDatabase` only created the
   database when absent, so the second run failed on `CREATE TABLE users`
   (`42P07`). Combined with (2), the suite reported success having run zero
   tests. Fixed by dropping and recreating the database each run — which also
   preserves a property worth having: the schema is rebuilt from `001` every
   run, so the suite continuously proves the migration chain applies to an
   empty cluster. The dev database, migrated incrementally over months, has
   never demonstrated that.

2. **Any setup error skipped the suite.** Now exactly one condition skips —
   Postgres unreachable — and a guard violation, failed migration, or bad DSN
   exits non-zero. A harness that cannot tell *"nothing to test against"* from
   *"the harness is broken"* protects nothing.

3. **`make test-integration` hid the skips.** Without `-v`, `go test` prints
   only `ok` and suppresses skipped-test output, so an all-skip run looked
   identical to an all-pass one — the same trap, reintroduced by the command
   that runs the suite. `-v` is now load-bearing, not cosmetic.

### Mutation checks

Every one confirmed to fail the named test, then reverted:

| Mutation | Result |
|---|---|
| `lower(email) = $1` → `email = $1` | mixed-case lookup fails — **the headline check; nothing else in the repo failed on this change** |
| `tx.Commit` moved before the accounts insert | rollback test reports `users = 1` |
| `23505` mapping pointed at a SQLSTATE that never fires | all four duplicate cases fail |
| migration 004's email index commented out | schema assertion **and** the case-differing duplicate fail — this is what proves migrations are genuinely applied, not inherited |

Counted **15 PASS / 0 SKIP / 0 FAIL** rather than trusting `ok`, and confirmed
the dev database still holds `users=15, accounts=15` after every run.

The first attempt at the `23505` mutation only broke the build via unused
imports, which proves nothing about the tests — it was redone so the code still
compiled.

---

## Step 13: Refresh-Token Revocation and Logout

`docs/security-backlog.md` item 2, carried as the highest-priority open
security item since Step 11. Refresh tokens lived 7 days with no server-side
kill switch, and `services/auth/internal/service/auth.go` said outright:
*"refresh tokens are stateless by design; no revocation list exists."*

- [x] `GenerateToken` sets a `jti` on every token (`pkg/auth`)
- [x] `RevocationStore` interface, a Redis-backed implementation, and a mock
      with error-injection fields for testing the fail-open path
- [x] `services/auth` gains its first Redis dependency (`go-redis`)
- [x] `Refresh` rejects a revoked `jti`; `POST /auth/logout` revokes one
- [x] Redis integration tests on logical DB 15 — round-trip, real TTL expiry,
      no key collision — independent of the existing Postgres skip path
- [x] Frontend: `api.logout`, `AuthProvider`'s `logout` clears the session
      immediately and revokes best-effort
- [x] Pre-push adversarial review found and fixed a real cross-user
      collision bug at the deploy boundary — see below

**Completed 2026-08-17.** Spec at `SPEC.md` (archived alongside prior steps
once the next spec is drafted). No gateway changes — `/auth/*` was already
proxied and rate-limited as a wildcard.

### The decisions

**A `jti` denylist, not rotation with reuse detection.** Closes the actual
stated problem — no kill switch — without forcing
`frontend/src/api/client.ts`'s shared in-flight-refresh promise to become a
correctness requirement instead of an optimization. Recorded as the upgrade
path in `docs/deferred-tuning.md` §8 with its trigger: the threat model needing
theft *detection*, not just a way to end a session.

**`go-redis` in `services/auth/go.mod` is a new dependency, not an inherited
one.** `docs/security-backlog.md` item 1 already corrected itself once on
"already a dependency" meaning the workspace, not the specific service — the
same scrutiny applied here before implementation rather than after.

**Fail open on a Redis error, at both the check and the write.** A Redis
outage must not become a second, unrelated way to log out every active session
every 15 minutes, and a failed revocation write must not surface as a failed
sign-out when the frontend already treats clearing local state as the whole
guarantee "sign out" makes. Both paths log so an outage stays visible.

**Logout returns `200 {}`, not `204`.** Caught before it shipped:
`client.ts`'s generic response handling calls `response.json()`
unconditionally on success, which throws on an empty 204 body.

### What review found

**Invisible to a green test suite**, the same pattern Step 11's review found
twice: every test written test-first exercised revocation with a real,
unique `jti` — nothing probed what happens at the actual deploy boundary,
where every token minted by the *previous* binary has no `jti` at all (the
zero value, since the claim didn't exist yet).

**`Logout` would revoke the empty string** (`8107f94`). Since every
pre-deploy token shares that same empty `jti`, they'd all resolve to the
identical revocation key. Confirmed directly: two fabricated tokens for two
different users, both `jti`-less — logging out token A made token B's
`Refresh` fail with `ErrTokenInvalid`, for a user who never logged out.
Bounded in practice (a token only collides until its first post-deploy
refresh, which mints a real `jti`), but real: one stale session logging out
could have silently broken refresh for every other stale session, for every
user, right at this step's own deploy.

*The rule it produced:* **never let a security-sensitive lookup key on a
zero value.** An empty string is a valid map/Redis key, not a signal that
nothing was found — treating it as an identity here is what let unrelated
tokens collide.

Fixed by rejecting a `jti`-less token in `Logout` outright rather than
revoking it. `Refresh` is deliberately left permissive for `jti`-less tokens
— rejecting them there too would force-log-out every session open across a
routine deploy, and the collision could only ever be *created* by the write
path, so closing `Logout` closes it completely.

### Verification

**Mutation check:** commented out the `IsRevoked` call in `Refresh` —
`TestRefresh_RevokedTokenRejected` and `TestLogout_RevokesTheToken` both
failed, as they should. Reverted. Same done for the `jti`-less guard itself:
removing it made `TestLogout_RejectsTokenWithoutJTI` fail, as it should.
Reverted.

**Manual, end to end:** registered and signed out in a real Chrome tab —
network tab showed `POST /auth/logout` → `200`, the UI returned to the login
screen instantly, no console errors. Separately confirmed by `curl` against
the gateway that replaying the same (now revoked) refresh token against
`/auth/refresh` returns `401 invalid_token`.

---

## Step 14: Trading Engine MVP

`agents.md` §2's "Simulated Trading Engine" — the last major system before
Phase 3, and `docs/security-backlog.md` item 4 (a gateway-wide request-body
cap), which was explicitly tied to `/trading/*` going live rather than
returning its `501` placeholder.

- [x] Spec drafted and reviewed — eleven design decisions resolved (`SPEC.md`
      §8)
- [x] Step 13's spec archived to
      `docs/archive/phase2-step13-refresh-token-revocation/`
- [x] Feature branch `step14-trading-engine-mvp` created and pushed
- [x] Plan (`tasks/plan.md`, `tasks/todo.md`) — 17 tasks, 5 checkpoints
- [x] Migration 006: `positions.avg_cost`, `orders.filled_price` /
      `rejection_reason`, `trades.realized_pl`
- [x] Fourth Go module `services/trading-engine`, added to `go.work`,
      `GO_MODULES`, and the Makefile's run/test/vet targets
- [x] `POST /trading/orders` — market buy and sell, filled at
      `market-data`'s live price, inside one transaction holding
      `SELECT ... FOR UPDATE` on the account row
- [x] `GET /trading/orders` / `/positions` / `/portfolio` — the read side,
      fail-open
- [x] Store integration tests against a real Postgres, including two
      concurrency proofs
- [x] `docs/security-backlog.md` item 4 **closed**: a gateway-wide 64 KiB
      body cap on every route, not just login
- [x] The gateway's `/trading/*` `501` replaced by a real proxy to
      `TRADING_ENGINE_SERVICE_URL`
- [x] Adversarial review found and fixed a real money-minting bug — see
      below

**Completed 2026-08-17.** Spec, plan and todo archived to
`docs/archive/phase2-step14-trading-engine-mvp/`. Backend only: the trading
UI is its own step (`SPEC.md` §1 non-goals).

### The decisions

**Backend only.** Order execution, positions, trade history, P/L, and the
gateway's body cap — no frontend UI. Mirrors how Step 11 shipped the entire
auth backend before Step 13 was the first step to touch `frontend/` at all;
the trading UI is sized as its own step once this API exists to build
against.

**Price fetched over HTTP from `market-data`, not read from its Redis cache
directly.** Keeps the cache format a private implementation detail behind
`market-data`'s own API — the same boundary every other cross-service call in
the project already respects — and costs `trading-engine` zero new
infrastructure.

**The order-write path fails closed; every read path fails open.** The
opposite split from Step 13's revocation check, deliberately: filling an
order at an unknown price is a correctness violation this project's fintech
premise doesn't tolerate, where a read degrading to "no live P/L available"
is not. Getting this reversed is called out in the spec as the easiest way to
violate its intent.

**`trading-engine` writes `accounts.balance`, a table `auth` also writes.**
First cross-service table write in the project. Deliberate — the schema has
supported it since migration 002 — but flagged rather than left implicit.

**New migration (006): `positions.avg_cost`, `orders.filled_price` /
`rejection_reason`, `trades.realized_pl`.** Rejected orders are persisted,
not discarded, for the same audit-trail reasoning that's driven every other
schema decision in this project.

**`SELECT ... FOR UPDATE` on the account row for the whole order
transaction**, so concurrent orders on one account can't both read the same
pre-trade balance and double-spend it. The spec calls for a dedicated
concurrency integration test proving this serializes in practice, not just
reading correct.

**No separate symbol whitelist** — `market-data`'s existing `404
price_not_cached` becomes the order's rejection reason directly, so there's
one source of truth for "is this symbol tradeable," not two that can drift.

**Long-only.** Selling more than a position holds is rejected
(`insufficient_position`), never shorted — `agents.md` never scopes
short-selling in.

**`trading-engine` revalidates the JWT itself**, matching the precedent
`auth`'s own `/me` route already set (revalidating rather than trusting the
gateway's injected `X-User-ID` header) — the trading surface is at least as
sensitive.

Full reasoning for all eleven, including the two options weighed for each,
is in `SPEC.md` §2.

### Four the plan decided, because the spec didn't

1. **A rejected order COMMITs; it does not roll back.** §2.3 says "rollback,
   reject" and §2.5 says rejected orders are persisted — a rollback would
   erase the row §2.5 wants. Only infrastructure failure rolls back.
2. **Validation lives in the store, not the service.** It has to read the
   balance *inside* the lock, and the service cannot hold a transaction
   without leaking `pgx` across the layer boundary.
3. **A user with no account row is `500`**, not a new 4xx code that should be
   unreachable. Every registered user gets an account; this is a broken
   invariant, not caller error.
4. **No `orders(account_id)` index in migration 006** — beyond the scoped
   migration, which is an "ask first" boundary. Deferred with a recorded
   trigger (`docs/deferred-tuning.md`).

### What review found

**A quantity below the ledger's tick minted money** (`00cb7ba`). Found by
Task 16's adversarial review, against the running stack — invisible to the
whole test suite, which was green before and after the discovery.

`quantity` was validated as `> 0` and nothing else, while `orders`, `trades`
and `positions` all store it as `NUMERIC(20,4)`. A quantity of `0.00001`
therefore passed validation, was charged for at the full price, and then
rounded to `0.0000` shares on the way into the database. **The cash leg
landed and the share leg vanished.**

On a buy that destroys money. On a sell it mints it, which is the direction
an attacker picks. Thirty dust sells through the gateway against a live
300-share AAPL position:

```
balance   8303.4969 -> 8303.5899   (+0.0930)
position   300.0000 ->  300.0000   (unchanged)
```

Free money at roughly a third of a cent per request, with nothing bounding
how often it could be repeated. It also left thirty `"quantity": 0,
"status": "filled"` rows in the order history — a filled sell of zero shares
at 305.655.

*The rule it produced:* **a bound on one end of a range is a question about
the other end.** `maxQuantity` already existed, and its comment already
reasoned explicitly about `NUMERIC(20,4)` — the same reasoning had simply
never been applied downward. Wherever code justifies an upper limit by the
storage format, the lower limit is owed the same justification.

Fixed by making the ledger's own tick the floor: `minQuantity = 0.0001`,
with anything finer *above* the floor snapped to the tick before the balance
is touched, so the quantity charged for is the quantity recorded rather than
the two disagreeing by whatever Postgres rounded away.

**The residual, stated honestly.** After the fix, the share leg is exact and
a sub-tick residual remains on the cash leg alone: measured at **+0.0000345
per fill** over 30 tick-sized sells at 305.655. That is intrinsic to
`float64` money stored at 4dp (see `docs/deferred-tuning.md`), not closable
by validation, and no longer worth farming — the caller now gives up 0.0306
of stock to gain 0.0000345 of cash.

**A diagnosis-quality issue, left unfixed as out of scope.** `market-data`
collapses *any* cache error — including a Redis-layer failure — into
`ErrPriceNotCached` → `404`
(`services/market-data/internal/service/market_data.go:162`). So a Redis
blip makes an order reject as `404 symbol_unavailable` instead of `502
upstream_unavailable`. Fail-closed still holds; only the reason recorded in
the order history is less precise.

### Mutation checks

Green tests are not evidence. Every control below was broken deliberately
and the suite re-run; all mutations were reverted.

| mutation | result |
|---|---|
| Delete `FOR UPDATE` from the account-row lock | Both concurrency tests failed on **10 of 10** runs |
| Rejection path `ROLLBACK` instead of `COMMIT` | 4 tests red across 3 concerns: buy rejection, sell rejection, order history |
| Restore `quantity > 0` in place of the tick floor | The 3 dust tests go red |
| Drop the tick-snapping of quantity | The snapping test goes red |
| Remove the gateway's `Content-Length` check | The 413 test goes red, chunked test still passes |
| Remove the gateway's `MaxBytesReader` | The chunked-truncation test goes red, 413 test still passes |
| Return `0` instead of `null` for an unpriceable position | 3 tests red across **both** the service and handler suites |

The `FOR UPDATE` mutation is worth recording in detail, because **the
balance is not what goes wrong.** Two concurrent orders of 600 against a
balance of 1000 both filled — and the balance still read exactly `400`. Two
trade rows, two filled orders, a position of 2. The account received 1200 of
stock for 600 of cash. A concurrency test asserting only the final balance
would have passed while the ledger was broken; the test asserts trade and
order counts for exactly that reason. The lower-contention test landed at
9900 in nine runs and 9800 in one, against a correct 9000 — eight or nine of
ten fills silently discarded.

### Verification

**Adversarial, against the full stack running** (auth, market-data,
trading-engine, and a gateway on `:8090` so the dev instance on `:8080` was
left alone):

- **20 concurrent orders on one account** — 3 filled, 17 rejected, balance
  exactly `100000 − Σtrades` = 8303.50, all 17 rejections persisted with a
  reason, and zero negative balances anywhere in the database
- **Selling into a short** — sell 500 holding 300; sell holding nothing; sell
  a never-bought symbol; sell 300.0001 holding 300. All four `400
  insufficient_position` with no state change. The last is the one a sloppy
  float comparison would have leaked
- **Garbage input** — quantity `0`, `-5`, `-0.0`, `1e308`, `null`, absent →
  `400 invalid_request`; `1e400`, `"10"`, a bare `NaN` → `400 malformed JSON
  body`; side `SELL`, `short`, `""`, `null`, absent → `400`; symbol absent,
  empty, 5000 chars, `AAPL'; DROP TABLE trades;--` → `400`; empty body,
  `null`, non-JSON, a JSON array → `400`
- **Cross-user isolation** — a valid token plus a forged `X-User-ID` naming
  the other account served the token holder's own data on all three reads,
  and debited the token holder on a buy. Repeated **against `:8083`
  directly, bypassing the gateway entirely**: still the token holder's data,
  which is what proves the engine's own `RequireAuth` defends independently
  of the gateway's `StripUserID`
- **Token forgery** — tampered signature, an `alg:none` token naming another
  user, and a refresh token presented as an access token: `401` at both the
  gateway and the engine
- **`market-data` killed mid-session** — write path `502
  upstream_unavailable` on buy *and* sell with the rejection persisted; read
  path `200` with `latest_price: null`, positions intact, portfolio valued
  at cost. The posture split holds on the final build

**Body cap, end to end:** a 100 KiB `POST /auth/login` returned `413
payload_too_large` and never reached auth; the same body sent chunked was
truncated at 64 KiB, and auth rejected the cut JSON with its own `400`; a
small chunked body reached auth intact. `payload_too_large` appears in no
other service, which is what confirms the 413 came from the gateway.

**First trade through the real edge:** register → login → buy 3 AAPL through
the gateway, filled at 305.6550, balance 99083.035, row confirmed in
Postgres.

`make test` green with Docker down, `make test-integration` **43 PASS / 0
FAIL** with it up, `make vet` clean, `gofmt -l` clean across the new module
and the gateway, migrations at version 6.

---

## Step 15: Trading Frontend

Wires the four `/trading/*` endpoints Step 14 shipped into the existing
dashboard — a typed wire layer, two polling/refetch hooks, and five new
components (order ticket, positions table, order history, portfolio
summary, and a shared null-safety helper), landing inside
`frontend/src/market/Dashboard.tsx`'s existing shell. Frontend only, no
backend change (`SPEC.md` §1 non-goals). First frontend step with automated
tests — `vitest` + `@testing-library/react`, scoped to the units holding
real logic.

- [x] Spec drafted and reviewed — ten design decisions resolved, all as
      recommended (`SPEC.md` §8)
- [x] Plan (`tasks/plan.md`) — 13 tasks across 4 phases, 3 checkpoints
- [x] `vitest` + Testing Library scaffolded; test files excluded from
      `tsc -b`
- [x] Wire types and `api.placeOrder/orders/positions/portfolio`, mirrored
      field-for-field against `services/trading-engine/internal/service/types.go`
- [x] Shared `frontend/src/format.ts` (`formatPrice`, `formatQuantity`);
      `PriceList.tsx` migrated onto it
- [x] `rejection-reason.ts` — four persisted rejection codes mapped to
      readable copy, `invalid_request` deliberately excluded (§2.6)
- [x] `usePortfolio` (15s poll) / `useOrders` (fetch-once), both with
      `refetch()` and a request-id guard against a poll tick and a refetch
      racing each other
- [x] `position-display.ts` — the null-price/null-P&L rule (§2.5) as one
      tested pure function, not reimplemented per component
- [x] `PositionsTable`, `PortfolioSummary`, `OrdersTable`, `OrderTicket` —
      the four new trading components
- [x] `Dashboard.tsx` wired: three-column layout, tabbed
      Chart/Positions/Orders/Portfolio panel, order ticket pinned across
      every tab, header balance
- [x] Adversarial manual pass found and fixed a real bug — see below

**Completed 2026-08-18.** Spec, plan and todo archived to
`docs/archive/phase2-step15-trading-frontend/`.

### The bug the adversarial pass found

`OrderTicket`'s `<input type="number" min={MIN_QUANTITY} max={MAX_QUANTITY}>`
sits inside a `<form>`. Submitting an out-of-range value (e.g. `0`) lets the
browser's own HTML5 constraint validation silently block the submit *before*
`handleSubmit` ever runs — no network request, but also no re-render, so
whatever error text happened to be on screen from a **previous** rejection
(a stale `insufficient_position` message, in the run that caught it) stayed
up, now describing the wrong thing entirely. `npm run build`/`lint`/`test`
all stayed green through this the whole time — it only showed up driving
the real form through a real browser, exactly the posture
`docs/NEXT_SESSION.md` and this project's own review convention insist on.
Fixed with `noValidate` on the `<form>`: the same bounds are still enforced,
just entirely by `validateQuantity` (already correct, already tested by the
other three cases), so there is exactly one place quantity errors get
decided and rendered instead of two disagreeing ones.

### Verification

**Adversarial, against the full stack running** (auth, market-data,
gateway, trading-engine, and the Vite dev server, all on their normal
ports) through a real browser, one throwaway account
(`step15manual`, deleted afterward):

- **Buy then sell, same interaction** — 10 AAPL bought, balance/position/
  order all updated with no refresh and no wait for the next poll tick;
  5 AAPL sold, same immediacy, confirmed from three different tabs
  (Positions, Orders, Portfolio) including the header balance figure
- **All four rejection reasons**, each rendering distinct, readable copy in
  both the order ticket and `OrdersTable`'s history: an oversized buy
  (`insufficient_balance`), an oversized sell (`insufficient_position`),
  and `market-data` stopped mid-session for a buy attempt
  (`upstream_unavailable`). `symbol_unavailable` was not reproducible
  through the running UI — the watchlist is a fixed, fully-cached
  seven-symbol list, so there is no symbol reachable through the actual
  form that the backend would refuse to price; this is a gap in what the
  *manual* pass could exercise, not a claim the code path is untested (it's
  covered by the handler's own table-driven test)
- **Client-side quantity validation** — `0`, `-5`, and `0.00001` (below the
  `0.0001` floor) all confirmed via the Network tab to never reach the
  gateway. This is the pass that found the bug above: `0` specifically
  didn't reach the network *or* update the error text, until the
  `noValidate` fix
- **`market-data` killed mid-session**: Positions showed em-dashes (not
  `$0.00`) for price and P/L on the existing position, Portfolio's muted
  "N position(s) valued at cost" note appeared with the correct count, and
  a buy attempt failed with the `upstream_unavailable` copy — all three in
  the same session. Restarting `market-data` and waiting one poll tick
  cleared the em-dashes and the muted note without a page refresh
- **Responsive layout** — below the `lg` breakpoint the three sections
  stacked watchlist → tab content → order ticket, matching the plan's
  acceptance criterion; above it, the three-column layout rendered with the
  order ticket pinned in the third column
- **Tab-switch mid-typing** — `42.5` typed into the quantity field survived
  two tab switches (Chart → Positions) unchanged, confirming `OrderTicket`
  lives outside the tab-conditional render as designed

`npm run build`, `npm run lint` (three pre-existing, non-blocking
`react-hooks/exhaustive-deps` warnings — one inherited from `use-prices.ts`,
two new ones on the same ref-in-cleanup pattern in the two new hooks,
neither failing the command), and `npm run test` (17 tests) all green on
the final commit. Dev database returned to `users=20, accounts=20,
orders=0, trades=0, positions=0` after the throwaway account was deleted.

### Second pass: code review plus mutation-tested the test suite

Before committing, a `/code-review high` pass over the full diff found four
more issues, all fixed and re-verified:

- **No reentrancy guard in `OrderTicket.handleSubmit`.** The submit
  button's `disabled={submitting}` only takes effect once React commits a
  re-render; a fast double-click or double Enter could fire two submits
  before that happens. Fixed with a synchronous `if (submitting) return` at
  the top of the handler, then confirmed live: a double-click on the real
  form in a real browser produces exactly one order in history, not two.
  (The pre-fix race wasn't separately reproduced — the guard closes an
  obviously real gap regardless, and the post-fix check is the one that
  matters.)
- **Stale quantity and error text on a symbol switch.** Typing `500` for
  AAPL, getting `insufficient_balance`, then switching the watchlist
  selection to MSFT left both the `500` and the AAPL-specific error
  visible under MSFT's header — misleading, and a real risk of an
  unintended large order carried over to the wrong symbol. Fixed with a
  `useEffect` keyed on `symbol` that clears `quantity` and `error` (not
  `side`, which is deliberately symbol-agnostic per §2.8). Reproduced
  before the fix and confirmed clean after it, live in the browser.
- **`tsconfig.app.json`'s test-file `exclude` (added in Task 1) was
  unnecessary** and quietly turned off type-checking for every `*.test.ts`
  file — verified by running `tsc` against the same config with the
  exclude removed, which compiled clean with zero errors. Removed
  entirely; `npm run build` still passes and test files are type-checked
  again.
- **The four rejection codes were hardcoded a second time** in
  `OrderTicket`'s live-submit error handling, duplicating
  `rejection-reason.ts`'s own list. `isKnownReason` was exported as
  `isRejectionReason` and `OrderTicket` now calls it instead of repeating
  the four strings — one list instead of two that could drift apart.

**Mutation-tested the three new test files** to confirm they're a real
safety net, not decoration — the same posture `docs/NEXT_SESSION.md`
insists on for UI verification, applied to the tests themselves: reverted
`position-display.ts`'s null-check to also require `unrealized_pl !== 0`
(reintroducing the exact "0 reads as flat" class of bug this file exists
to prevent) and the test suite failed immediately; duplicated one of
`rejection-reason.ts`'s four descriptions and the distinctness test caught
it; loosened `formatQuantity`'s trailing-zero trim and both affected tests
failed. All three mutations reverted afterward with a clean diff.

Full suite re-verified after every fix: `npm run build`/`lint`/`test`
green, `make test`/`make vet` green (untouched, confirming no backend
regression), dev database back to baseline after a second throwaway
account (`step15review`) used to verify the two behavioral fixes live.

---

## Still open

- [ ] **market-data's store has the same gap** — `historical_price_store.go`
      has no tests either, and its idempotent upsert (`UNIQUE(symbol,
      timeframe, timestamp)`) is exactly the kind of SQL worth covering. It is
      now also **the recorded trigger for extracting the harness**: Step 14
      copied it a second time rather than sharing it, and a third use is where
      that stops being defensible (`docs/TESTING_STRUCTURE.md` §6a).
- [ ] **Pre-existing `gofmt` drift** in `services/auth/internal/service/`
      (`interfaces.go`, `types.go`). Untouched by Steps 11–14. Still worth a
      one-line cleanup commit before any `fmt` check lands in a Makefile
      target or CI.
- [x] ~~**Dev-database rows from Step 14's verification.**~~ **Resolved
      2026-08-17.** The four throwaway users (`step14manual`, `step14gateway`,
      `step14adva`, `step14advb`) and their rows were deleted before the merge,
      on Khalil's approval — including the 31 `quantity = 0, status = filled`
      rows left by the bug the review found, which documented a defect that no
      longer exists. All 103 orders, 73 trades and 4 positions in the dev
      database belonged to those four accounts, so the trading tables are now
      empty and the database is back to `users=20, accounts=20` — the plan's
      own criterion, unamended.
