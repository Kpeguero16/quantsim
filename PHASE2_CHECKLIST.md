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

## Still open before the trading engine

- [ ] **Store-layer integration harness** — `internal/store/` has no tests at
      all. Both existing suites run against a Go map and would stay green with
      a completely wrong SQL query. `docs/TESTING_STRUCTURE.md` §4 sketches the
      shape; the open decision is testcontainers vs. the existing
      docker-compose, and how CI gets a database. **Do this before Phase 2
      adds far more SQL than auth ever had.**
- [ ] **Refresh-token revocation and a real logout** —
      `docs/security-backlog.md` item 2, now the highest-priority open item.
      Tokens live 7 days with no kill switch, and "sign out" is client-side
      only.

## Then the engine itself

- [ ] Order execution (market buy/sell)
- [ ] Trade storage and history
- [ ] Position tracking and P/L
- [ ] `/trading/*` stops returning `501` — the natural moment for the
      gateway-wide body cap (`docs/security-backlog.md` item 4)
