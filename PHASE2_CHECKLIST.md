# QuantSim Phase 2 — Trading Engine Checklist

Phase 1 is complete (`PHASE1_CHECKLIST.md`). Phase 2 delivers the trading
engine — order execution, trade storage, P/L tracking — and opens with the
security work that Phase 2 itself makes consequential.

**Why security comes first.** Today account takeover buys a read-only view of
public market data. Once `/trading/*` executes orders against a $100k
simulated balance, the same weakness lets someone trade as another user. The
auth surface does not get weaker in Phase 2; the consequences of its existing
gaps get materially worse. Reasoning in `docs/security-backlog.md`.

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
- [x] 14 tests over all three store methods
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

**Three guards on the target database.** The dev database holds 15 real users
and this harness runs `TRUNCATE`, while the environment actively misleads:
`POSTGRES_DB=quantsim` is an *empty decoy* and `DATABASE_URL` points at
`postgres`, where the real rows live. Both names a careless harness would grab
are wrong, one destructively. So the name is checked when the DSN is derived,
after the pool connects, and again immediately before every `TRUNCATE` — the
last by asking the server `SELECT current_database()` rather than trusting a
string parsed at startup.

**Rollback forced by numeric overflow.** `accounts.balance` is
`NUMERIC(20,4)`, so a `startingBalance` of `1e16` fails the accounts insert
*after* the users insert succeeded. No schema mutation, so unlike a temporary
`CHECK` constraint there is nothing that can leak into a later test if cleanup
fails.

### Two bugs found in the harness itself

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

Counted **14 PASS / 0 SKIP / 0 FAIL** rather than trusting `ok`, and confirmed
the dev database still holds `users=15, accounts=15` after every run.

The first attempt at the `23505` mutation only broke the build via unused
imports, which proves nothing about the tests — it was redone so the code still
compiled.

---

## Still open before the trading engine

- [ ] **Refresh-token revocation and a real logout** —
      `docs/security-backlog.md` item 2, now the highest-priority open item.
      Tokens live 7 days with no kill switch, and "sign out" is client-side
      only.
- [ ] **market-data's store has the same gap** — `historical_price_store.go`
      has no tests either, and its idempotent upsert (`UNIQUE(symbol,
      timeframe, timestamp)`) is exactly the kind of SQL worth covering. Step
      12's harness is the template; extracting it to `pkg/testutil/` is worth
      considering at that point, but not before (see
      `docs/TESTING_STRUCTURE.md` §4).
- [ ] **Pre-existing `gofmt` drift** in `services/auth/internal/service/`
      (`interfaces.go`, `types.go`). Untouched by Steps 11–12 and left alone
      deliberately; worth a one-line cleanup commit before any `fmt` check
      lands in a Makefile target or CI.

## Then the engine itself

- [ ] Order execution (market buy/sell)
- [ ] Trade storage and history
- [ ] Position tracking and P/L
- [ ] `/trading/*` stops returning `501` — the natural moment for the
      gateway-wide body cap (`docs/security-backlog.md` item 4)
