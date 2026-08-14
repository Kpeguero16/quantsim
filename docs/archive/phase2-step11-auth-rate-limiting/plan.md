# Implementation Plan — QuantSim Auth Rate Limiting (Step 11)

## Overview

Close `docs/security-backlog.md` item 1: nothing throttles `/auth/login` today. `SPEC.md` is **approved**; §9 records the three design decisions and §10 the two findings raised during drafting, both approved as recommended.

The step adds one package (`internal/limiter`), two middlewares, and a wiring change. **No new module dependency, no database change, no change to `services/auth/`.**

Two properties in this step are security invariants rather than features, and they are what the task ordering is built around:

- **The per-IP key must be `r.RemoteAddr`** (§2.5). The backlog's claim that inbound `X-Forwarded-For` is sanitized is wrong — `SetXForwarded()` runs on `r.Out` inside the proxy's `Rewrite`, after all middleware, and only shapes the upstream request. Keying on the header yields a limiter bypassable with one forged header per request: unlimited budget, while appearing to work.
- **Throttling must not become an enumeration oracle** (§2.6). Step 9 §2.12 made login's failure uniform on purpose. Counting only known accounts would let a `429` distinguish "real account" from "unknown", undoing that.

Both are pinned by tests written **before** the code (Tasks 1 and 5). A green suite that omitted either would prove nothing about the property that matters.

## Architecture decisions

Restated from `SPEC.md` §2 for reference while implementing:

- **In-memory counters behind a `limiter.Store` interface** — no new `go.mod` entry; cannot be "unavailable", so no fail-open/closed question — §2.1
- **Per-IP on all `/auth/*`; per-account backoff on `/auth/login` only** — neither dimension alone stops the attack — §2.2
- **No hard lockout.** Exponential backoff, always decaying — a lockout is a DoS primitive against a named user and needs an unlock path that does not exist — §2.3
- **Per-IP counts every request, checked before the proxy. Per-account counts only `401`s, accounted after it** — §2.4
- **Per-IP keys on `r.RemoteAddr`, port stripped** — §2.5
- **Throttle on the submitted email whether or not the account exists** — §2.6
- **One `429` code and message for both dimensions** — §2.7
- **Limiter sits inside CORS, outside the route group** — §2.8

## Dependency order

```
Task 1 (store + tests) ─┬─> Task 2 (per-IP mw) ──> Task 3 (router wiring) ─┬─> Task 6 (docs)
                        │                                                   │
Task 4 (backoff) ───────┴─> Task 5 (per-account mw + body) ─────────────────┘
```

Tasks 1 and 4 are independent of each other but both gate their middleware. Task 3 must land before Task 5, since Task 5 wraps a route Task 3 mounts.

---

## Phase 1 — The limiter core

### Task 1 — `limiter.Store` and the in-memory implementation

**Files:** `services/gateway/internal/limiter/limiter.go`, `memory.go`, `memory_test.go`

Define `Store` and a sharded in-memory implementation with periodic eviction. The store takes an injected `now func() time.Time` so **no test sleeps**.

**RED first** — write these before `memory.go` exists:
- under-limit requests pass; the N+1th is refused (test #1)
- window expiry restores the budget (test #2)
- two keys have independent budgets (test #3)
- eviction reclaims entries stale past their window (test #12)

**Acceptance criteria:**
- `Store` is an interface; `MemoryStore` is the concrete type — consumers depend on the interface per `docs/TESTING_STRUCTURE.md`
- Fixed-window counting: key → (count, window start)
- Eviction runs on a ticker, is cancellable via `context.Context`, and never blocks a request path
- Sharded by key hash so eviction does not contend with the whole map
- `go test ./internal/limiter/...` green; `go vet` clean
- **No new entry in `services/gateway/go.mod`**

**Verification:** the four tests above, plus a `-race` run — this is the one genuinely concurrent piece in the step.

---

### Task 4 — The exponential backoff schedule

**Files:** `services/gateway/internal/limiter/backoff.go`, `backoff_test.go`

Consecutive-failure counter with the §2.2 schedule: failures 1–4 free; the 5th opens 1 min, then 2, 4, 8, capped at 15 min. A success resets to zero.

*(Numbered 4 to match `todo.md` ordering; it has no dependency on Tasks 2–3 and may be built directly after Task 1.)*

**RED first:**
- 4 failures pass, the 5th throttles (test #5)
- delays double and cap at the ceiling (test #6)
- a success resets the counter to zero (test #7)

**Acceptance criteria:**
- Pure function of (failure count, config) → delay; no I/O, no clock reads inside the schedule itself
- Ceiling configurable, defaulting to 15 min
- Every window decays without intervention — **no permanent state anywhere**
- Reports the remaining delay so the caller can set `Retry-After`

**Verification:** table test walking failures 1→10 against expected delays, including the cap.

---

## Phase 2 — Per-IP limiting

### Task 2 — `RateLimitByIP` middleware

**Files:** `services/gateway/internal/middleware/ratelimit.go`, `ratelimit_test.go`

**Depends on:** Task 1

**RED first — this is the bypass test, write it before the middleware:**
- **a forged `X-Forwarded-For` does not create a new budget** (test #4)

Then:
- `429` body matches `{code, message}` and carries `Retry-After` (test #10)

**Acceptance criteria:**
- Key is `r.RemoteAddr` with the port stripped via `net.SplitHostPort`; IPv6 handled
- **`X-Forwarded-For` and `X-Real-IP` are never read** — §2.5
- Refusals use `httperr.Write` with code `rate_limited`, never a bare `http.Error` — §2.7
- `Retry-After` in seconds
- Honours `RATE_LIMIT_ENABLED=false` by passing through

**Verification:** test #4 is the one that matters. Confirm it fails before the middleware keys on `RemoteAddr` and passes after.

---

### Task 3 — Wire the limiter into the router

**Files:** `services/gateway/internal/handler/router.go`, `cmd/server/main.go`, `.env.example`

**Depends on:** Task 2

Mount `RateLimitByIP` in the chain and construct the store in `main.go`.

**Acceptance criteria:**
- Order is `StripUserID -> CORS -> RateLimitByIP -> [route group]` — §2.8
- A `429` carries CORS headers (test #11) — the reason the limiter sits inside CORS
- Config read in `main.go` only, via the existing `envOrDefault` pattern; packages take values, not `os.Getenv`
- All five `RATE_LIMIT_*` knobs from §4 documented in `.env.example` with working defaults
- Eviction goroutine started with a cancellable context; `RATE_LIMIT_ENABLED=false` logs loudly at boot
- Existing `router_test.go` still green — no regression to routing or auth

**Checkpoint — stop for review.** Per-IP limiting is live end to end.

**Manual verification:** restart the gateway (`go run` serves a stale binary otherwise — this cost an entire step once), then loop `/auth/login` past the threshold. Expect `429` with the standard body, recovery after the window, and `/healthz` plus `/market-data/*` unaffected.

---

## Phase 3 — Per-account limiting

### Task 5 — `RateLimitLoginByAccount` and body handling

**Files:** `services/gateway/internal/middleware/ratelimit.go`, `ratelimit_test.go`, `handler/router.go`

**Depends on:** Tasks 3, 4

The one piece of real machinery. Reads `/auth/login`'s JSON body to extract the email, replays it to the proxy, and accounts the result **after** the upstream responds.

**RED first — this is the enumeration-oracle test:**
- **unknown and known emails throttle identically** (test #8)

Then:
- `Foo@x.test` and `foo@x.test` share one counter (test #9)

**Acceptance criteria:**
- Body read under a **64KB cap** (§10.2), buffered, and replayed via `io.NopCloser` — the proxy must see an identical body
- A body over the cap, or malformed JSON, is **passed through untouched** for the auth service to reject — the gateway does not take over validation it does not own
- Key is `strings.ToLower(strings.TrimSpace(email))` — the Step 9/10 rule, §2.6
- **The counter is keyed on the submitted email with no database consultation** — existence is never checked, so both cases throttle identically
- A `ResponseWriter` wrapper captures the upstream status; only `401` increments, `200` resets — §2.4
- Throttled requests return the **identical** `429` code and message as the per-IP path — §2.7
- Wraps `/auth/login` only; `/auth/register` and `/auth/refresh` keep per-IP only

**Checkpoint — stop for review.** Both dimensions live.

**Manual verification:** repeated failures for a real account throttle after 5; a *nonexistent* email throttles identically (same status, body, and timing); a correct password from a different IP still succeeds during the backoff.

---

## Phase 4 — Close out

### Task 6 — Documentation and corrections

**Files:** `docs/security-backlog.md`, `docs/deferred-tuning.md`, `PHASE1_CHECKLIST.md` or a Phase 2 equivalent, `docs/NEXT_SESSION.md`

**Depends on:** Task 5

**Acceptance criteria:**
- **`docs/security-backlog.md` item 1 corrected** (§10.1) — the `X-Forwarded-For` premise is wrong and must not be rediscovered and believed; mark the item closed and point at `SPEC.md` §2.5
- `docs/deferred-tuning.md` gains two entries with **named triggers**, not vague intentions:
  - in-memory counters are per-instance → trigger: *more than one gateway instance*
  - `RemoteAddr` keying breaks behind an ALB → trigger: *Phase 4 deployment behind a load balancer*
- Step 11 written up wherever Phase 2 status is tracked
- `docs/NEXT_SESSION.md` rewritten (not appended to) per its own convention

---

## Risks

| Risk | Mitigation |
|---|---|
| **Limiter keys on a forgeable header** — silently bypassable, looks fine | Test #4 written before the middleware; §2.5 states the corrected mechanism |
| **`429` becomes an enumeration oracle** — undoes Step 9 §2.12 | Test #8 written before the code; no DB consultation in the key path |
| **Body buffering breaks login** — proxy sees a consumed body | Replay via `io.NopCloser`; oversized/malformed bodies pass through untouched |
| **Memory growth under a distributed attack** — many keys, none evicted | Fixed windows + eviction ticker; test #12 pins reclamation |
| Legitimate users throttled by shared NAT egress | Per-IP budget is 100/15min across all `/auth/*` — generous for humans; per-account counts failures only, so correct logins never burn budget |
| Concurrency bug under load | `-race` on the limiter suite; sharded locking |
| Gateway not restarted, behaviour looks wrong | Called out in every manual verification step; `NEXT_SESSION.md` records the precedent |

## Out of scope

Rate limiting for `/market-data/*` or `/trading/*`; refresh-token revocation (backlog item 2); the full gateway-wide body cap (item 4 — only the `/auth/login` 64KB read lands here); a Redis `Store` implementation; any change inside `services/auth/`.
