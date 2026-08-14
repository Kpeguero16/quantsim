# SPEC — QuantSim Auth Rate Limiting (Step 11)

Status: **Approved 2026-08-14.** Khalil resolved the three design decisions before drafting (§9) and approved both open findings as recommended (§10). Implementation is unblocked.
Scope: one new package in the gateway, one middleware wiring change, unit tests. No database, no migration, no change to the auth service.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `phase1-step10-identity-lookup/` — all complete. **Phase 1 is closed;** this is the first item of the Phase 2 security work.

---

## 1. Objective

Close `docs/security-backlog.md` item 1 — **the largest remaining gap in the auth surface**. Nothing throttles authentication attempts today. An attacker can submit credentials against `/auth/login` as fast as the network allows: no per-IP limit, no per-account limit, no backoff.

Step 9 added a 15-character minimum, a blocklist, and bcrypt. Every one of those raises the cost **per guess**. None bounds the **number** of guesses, which is what actually defeats credential stuffing against reused passwords.

**Why now, ahead of the trading engine.** Today account takeover buys a read-only view of public market data. Once `/trading/*` executes orders against a $100k simulated balance, the same weakness lets someone trade as another user. The auth surface does not get weaker in Phase 2 — the consequences of its existing gaps get materially worse. This is cheap now and expensive to retrofit under a live order path.

**Non-goals.** Not a general-purpose gateway rate limiter for `/market-data/*` or `/trading/*`. Not the refresh-token revocation work (backlog item 2). Not the full gateway-wide body cap (item 4) — see §10.2 for the sliver this step does pull in.

---

## 2. Design decisions

### 2.1 Counters live in memory, in the gateway process

A sharded map with periodic eviction, behind a `limiter.Store` interface. **No new module dependency.**

The backlog suggested Redis on the grounds that *"Redis is already a dependency, so a shared counter does not add infrastructure."* That is true of the **stack** and false of the **gateway**: `services/gateway/go.mod` requires only `chi` and `pkg`. Only market-data imports `go-redis`. Step 7 §8 lists *"any dependency beyond `go-chi/chi/v5`"* under **Ask first** — so this was a decision, not a detail.

In-memory is correct while exactly one gateway instance runs, which is true today and stays true under Phase 4's single-EC2 docker-compose plan. It also has a property Redis does not: **it cannot be unavailable**, which removes the fail-open/fail-closed question entirely. Fail-closed on a shared store means a Redis outage locks every user out of login; fail-open means the limiter silently stops limiting. Neither is a good answer, and this design does not have to pick one.

**The trade, stated plainly:** two gateway instances would each hold their own counters, doubling the effective limit. The `Store` interface exists so a Redis implementation drops in without touching middleware or handlers. Recorded in `docs/deferred-tuning.md` on completion, with the trigger — *more than one gateway instance* — named explicitly.

### 2.2 Two dimensions: per-IP on all `/auth/*`, per-account backoff on `/auth/login`

Neither alone is sufficient:

- **Per-IP alone** does not stop credential stuffing from a botnet, since each bot IP gets a fresh budget. That is the exact attack the backlog calls the highest-priority gap.
- **Per-account alone** does not stop one host spraying many accounts.

| Dimension | Applies to | Limit |
|---|---|---|
| Per-IP | all `/auth/*` | 100 requests / 15 min, fixed window |
| Per-account | `/auth/login` only | exponential backoff on consecutive failures |

Backoff schedule, keyed by submitted email: failures 1–4 pass freely; the 5th failure opens a 1-minute window, then 2, 4, 8, capped at 15 minutes. **Every window decays on its own.**

`/auth/register` and `/auth/refresh` get per-IP only. Register has no account to key on yet; refresh would require decoding the token, which puts token parsing in the gateway for no gain the per-IP limit does not already provide.

### 2.3 No hard lockout — deliberately

A hard lockout is the stronger control against guessing and is **rejected**. Anyone who knows a user's email could freeze that account at will, converting an auth control into a denial-of-service primitive against named users. It also needs an unlock path — an email flow or an admin tool — and neither exists.

Exponential backoff keeps the useful property (guessing gets exponentially slower) without permanent state. **An attacker can still degrade a known victim's login for up to ~15 minutes.** That is the accepted cost of the trade and it is bounded, self-healing, and requires sustained effort to maintain.

### 2.4 Per-account counts only failures; per-IP counts every request

Per-IP is checked **before** the proxy runs — cheap, and it blocks floods without any knowledge of what auth decided.

Per-account is accounted **after** the proxy returns, and only when the upstream status is `401`. A success resets the counter to zero. This means a legitimate user typing the right password is never throttled, however often they log in — only misses burn budget.

Cost: the gateway must observe the upstream response status, which needs a `ResponseWriter` wrapper capturing the code the proxy wrote. This is the one piece of real machinery in the step.

### 2.5 The per-IP key is `r.RemoteAddr` — **not** `X-Forwarded-For`

**The backlog's stated premise is wrong, and following it would produce a bypassable limiter.**

It claims the gateway's `r.SetXForwarded()` call *"replaces any inbound `X-Forwarded-For`, so a per-IP limiter can trust that header."* Verified against the code: `SetXForwarded()` is called on `r.Out` inside `proxy.New`'s `Rewrite` (`services/gateway/internal/proxy/proxy.go:59`). `Rewrite` runs **after** every gateway middleware and builds only the **upstream** request. The inbound `r.Header["X-Forwarded-For"]` that a middleware sees is never sanitized — it is whatever the client sent.

A limiter keying on that header could be bypassed with one line of `curl`: a fresh forged `X-Forwarded-For` per request buys an unlimited budget. The limiter keys on `r.RemoteAddr` with the port stripped, which is the real TCP peer and unforgeable at this hop.

**The backlog entry gets corrected as part of this step** so the wrong premise is not rediscovered and believed by the next reader.

*When the gateway later sits behind an ALB (Phase 4), the real client IP will arrive in a header from a trusted proxy and this decision must be revisited deliberately.* Noted in `docs/deferred-tuning.md`, not designed for now.

### 2.6 A `429` must not become an enumeration oracle

Step 9 §2.12 deliberately made login's failure uniform: unknown-email and wrong-password return the identical `401`, backed by a dummy-hash timing defence. A rate limiter is an easy way to undo that.

**Invariant: throttle on the submitted email whether or not the account exists.** The counter keys on the normalized email string, and nothing consults the database. Since a nonexistent email also returns `401`, both cases accumulate failures identically and throttle identically. A `429` therefore reveals only that *this email has recent failed attempts* — attempts the attacker made themselves, so they learn nothing they did not already know.

Concretely forbidden: skipping the counter when the user is unknown, or any distinct code/message/timing between the two.

Email normalization for the key is `strings.ToLower(strings.TrimSpace(email))` — the same rule Steps 9 and 10 made structural, so `Foo@x.test` and `foo@x.test` share one counter rather than getting a fresh budget per capitalization.

### 2.7 Response shape

Standard JSON error via `httperr.Write`, matching every other gateway response:

```json
{ "code": "rate_limited", "message": "too many requests, please try again later" }
```

`429 Too Many Requests`, plus a `Retry-After` header in seconds. One code and one message for both dimensions — a per-IP block and a per-account block are indistinguishable to the client, which keeps §2.6 intact.

### 2.8 Middleware order

```
StripUserID -> CORS -> RateLimitByIP -> [route group] -> proxy
```

`RateLimitByIP` sits **inside** CORS for the reason Step 7 put `RequireAuth` there: a `429` without CORS headers surfaces in a browser as an opaque network error, and you debug the wrong layer. It stays **outside** the route group so it applies before any per-route work.

Per-account accounting wraps the `/auth/login` route specifically, since it is the only route that needs the response-status wrapper.

---

## 3. Project structure

```
services/gateway/internal/limiter/
  limiter.go        # Store interface, Limit/Result types
  memory.go         # sharded in-memory Store + eviction loop
  memory_test.go
  backoff.go        # exponential schedule, failure counters
  backoff_test.go
services/gateway/internal/middleware/
  ratelimit.go      # RateLimitByIP + RateLimitLoginByAccount
  ratelimit_test.go
```

Existing files touched: `handler/router.go` (wiring), `cmd/server/main.go` (construct the store, start eviction), `.env.example` (new knobs), `docs/security-backlog.md` (§2.5 correction), `docs/deferred-tuning.md` (§2.1 and §2.5 triggers).

**Not touched:** `services/auth/` entirely. This is a gateway-layer control.

---

## 4. Configuration

Follows the existing `envOrDefault` pattern in `main.go`. Every knob has a working default so nothing new is required in `.env` to run the stack.

| Variable | Default | Meaning |
|---|---|---|
| `RATE_LIMIT_IP_REQUESTS` | `100` | requests per window per IP |
| `RATE_LIMIT_IP_WINDOW` | `15m` | per-IP window |
| `RATE_LIMIT_LOGIN_FAILURES` | `5` | consecutive failures before backoff |
| `RATE_LIMIT_LOGIN_MAX_BACKOFF` | `15m` | backoff ceiling |
| `RATE_LIMIT_ENABLED` | `true` | escape hatch; `false` disables both dimensions |

`RATE_LIMIT_ENABLED=false` exists so a limiter bug cannot become an outage with no way out. It logs loudly at boot when off.

---

## 5. Testing strategy

Per `agents.md`, Phase 1 allowed manual verification. This step is Phase 2 security work with real logic worth pinning, so it gets **unit tests, TDD, RED before GREEN** — consistent with how Steps 9 and 10 were closed.

Every test runs against the in-memory store with an **injected clock**, so no test sleeps. `limiter.Store` takes a `now func() time.Time`.

| # | Test | Proves |
|---|---|---|
| 1 | under-limit requests pass, N+1 gets `429` | the per-IP limit binds |
| 2 | window expiry restores the budget | counters decay, no permanent state |
| 3 | two IPs have independent budgets | key isolation |
| 4 | **forged `X-Forwarded-For` does not create a new budget** | §2.5 — the bypass is closed |
| 5 | 4 failures pass; 5th throttles | the backoff schedule binds |
| 6 | delays double and cap at the ceiling | schedule correctness |
| 7 | a `200` resets the failure counter | §2.4 — success is not penalized |
| 8 | **unknown and known emails throttle identically** | §2.6 — no enumeration oracle |
| 9 | `Foo@x.test` and `foo@x.test` share a counter | §2.6 — normalization |
| 10 | `429` body matches `{code, message}` and carries `Retry-After` | §2.7 contract |
| 11 | a `429` carries CORS headers | §2.8 — order is correct |
| 12 | eviction reclaims stale entries | unbounded growth is bounded |

Tests 4 and 8 are the two that would catch a silently broken security property, and both are written first.

**Manual verification at the checkpoint:** a real login loop against the running stack confirming `429` after the threshold, recovery after the window, and that a correct password still succeeds mid-backoff from a different IP.

---

## 6. Code style

Follows what the gateway already does, which is more specific than "idiomatic Go":

- Comments explain **why**, not what — the existing gateway comments are the reference (`proxy.go` on `SetXForwarded`, `identity.go` on `StripUserID` being outermost). Every non-obvious security decision above carries its reasoning into the code.
- Constructors return concrete types; consumers depend on interfaces (`limiter.Store`), per `docs/TESTING_STRUCTURE.md`.
- No new dependency. Standard library plus `chi`.
- `httperr.Write` for every error response — never a bare `http.Error`.
- Config read in `main.go` only; packages take values, not `os.Getenv`.

---

## 7. Commands

```bash
make docker-up                          # Postgres + Redis
make run-auth                           # :8081
make run-gateway                        # :8080  (restart after any code change)
cd services/gateway && go test ./...     # the suite for this step
go build ./...                           # compile check
```

**Restart the gateway after every change.** It runs under `go run`; a live instance keeps serving the old binary. `NEXT_SESSION.md` records this silently costing an entire step once — three commits of validation sat on disk while `:8081` kept accepting one-character passwords.

---

## 8. Boundaries

**Always do:**
- Key the per-IP limit on `r.RemoteAddr`, never on a client-supplied header (§2.5)
- Throttle on the submitted email whether or not the account exists (§2.6)
- Return one identical `429` code and message for both dimensions (§2.7)
- Keep the limiter inside CORS so `429`s carry CORS headers (§2.8)
- Normalize the email key with the Step 9/10 rule (§2.6)
- Run `go test ./...` before flagging a checkpoint done

**Ask first:**
- Any new module dependency — the §2.1 decision is specifically what keeps this at zero
- Extending rate limiting to `/market-data/*` or `/trading/*`
- Changing the uniform-failure behaviour of login in any way
- Any change inside `services/auth/`

**Never do:**
- Implement a hard account lockout (§2.3)
- Let the limiter distinguish "throttled" from "wrong password" in code, message, or timing (§2.6)
- Trust inbound `X-Forwarded-For` or `X-Real-IP` for limiting (§2.5)
- Log a password, a token, or a full email at any level
- Skip the counter for unknown accounts

---

## 9. Decisions resolved before drafting

Resolved 2026-08-14, all as recommended:

| # | Decision | Resolution |
|---|---|---|
| 1 | Counter store | **In-memory**, behind a `Store` interface — §2.1 |
| 2 | Per-account policy | **Per-IP + per-account throttle**, no hard lockout — §2.2, §2.3 |
| 3 | What to count | **Failures only** for per-account; all requests for per-IP — §2.4 |

---

## 10. Findings raised during drafting — **both approved 2026-08-14**

### 10.1 The backlog's `X-Forwarded-For` premise is wrong (§2.5) — **approved**

Not a question so much as a correction to confirm: the limiter keys on `r.RemoteAddr`, and I amend `docs/security-backlog.md` item 1 in this step so the wrong premise does not get rediscovered. **Recommend: yes, correct it here** — the entry is the first thing the next reader will trust.

### 10.2 Per-account keying requires reading the request body — a scope question — **approved**

This one genuinely expands the step and I do not want to absorb it silently.

To key on the submitted email, the gateway must **read the JSON body of `/auth/login`**, which it has never done — it is a pure proxy that streams the body upstream untouched. That pulls in three things:

1. Buffering the body and replaying it to the proxy (`io.NopCloser` over the buffered bytes).
2. **A size cap on that read**, because buffering an unbounded body is a memory-exhaustion vector. This is a narrow slice of backlog **item 4** (the gateway-wide body cap) arriving early — scoped here to `/auth/login` only, at 64KB.
3. The gateway parsing an auth-specific payload shape, which is a small but real layering leak. It currently knows only path prefixes.

**Recommend: accept it, scoped to `/auth/login`.** Per-account limiting is what stops distributed credential stuffing, and there is no way to key on an account without reading the account identifier. The 64KB cap is strictly an improvement and item 4 gets easier, not harder. The layering cost is one route and one field, documented.

**The alternative is dropping per-account limiting entirely** and shipping per-IP only — smaller, cleaner, no body handling — but it leaves the headline attack from §1 substantially open.

---

## 11. Implementation

Approved. `tasks/plan.md` holds the task breakdown, acceptance criteria, dependency order, and risks; `tasks/todo.md` is the checkpoint list. Four checkpoint-sized slices: **(1)** the store and backoff schedule with their tests, **(2)** the per-IP middleware wired into the router, **(3)** the per-account middleware and body handling, **(4)** documentation and backlog corrections.
