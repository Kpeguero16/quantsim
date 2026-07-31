# Security backlog — known gaps, deliberately deferred

Every item here was found during a real piece of work, verified against running
code, and left undone **on purpose** with the reasoning recorded. None is a
surprise; none is a Phase 1 blocker (Phase 1 is complete). This file exists so
they are scheduled rather than rediscovered.

**How this differs from `docs/deferred-tuning.md`:** that file is performance
defaults — its own framing is *"None are bugs"*, and it triggers at Phase 4 when
there is real traffic to measure. The items here **are** gaps, several have a
plausible attack behind them, and the first two are due sooner than Phase 4.

---

## Why Phase 2 changes the calculus

Today, taking over a QuantSim account gets an attacker a read-only view of
public market data. Nothing is at stake that is not already public.

**Phase 2 makes account takeover mean something.** Once `/trading/*` executes
orders against a $100k simulated balance, the same weakness lets someone trade
as another user, move their balance, and write to their trade history. The
authentication surface does not get weaker in Phase 2 — the *consequences* of
its existing weaknesses get materially worse.

That is the argument for doing items 1 and 2 **as part of Phase 2**, not after
it: they are cheap now and they are what stops "someone guessed a password"
from becoming "someone traded with my account."

---

## 1. No rate limiting on `/auth/login` — **highest priority**

**Where:** the gateway. Deliberately out of scope in the Step 7 spec §8.

**Now:** nothing throttles authentication attempts. An attacker can submit
credentials against `/auth/login` as fast as the network allows. There is no
per-IP limit, no per-account limit, and no lockout.

**Why it matters:** this is the single largest gap in the auth surface. Every
other control in Step 9 — a 15-character minimum, a blocklist, bcrypt — raises
the cost *per guess*. None of them bounds the *number* of guesses, which is
what actually defeats credential stuffing against reused passwords.

**Already in place to build on:** the gateway calls `r.SetXForwarded()`
(`services/gateway/internal/proxy/proxy.go:59`), which **replaces** any inbound
`X-Forwarded-For` with the value from the real connection. A per-IP limiter can
therefore trust that header — the usual reason naive limiters are bypassable
does not apply here.

**Shape when done:** per-IP and per-account limits on `/auth/login`, `/auth/register`,
and `/auth/refresh`, returning `429` with the standard `{code, message}` body.
Prefer a limiter that fails *closed* on its backing store being unavailable, and
decide that deliberately. Redis is already a dependency, so a shared counter does
not add infrastructure.

**Watch for:** a per-account lockout is itself a denial-of-service vector — an
attacker who knows an email can lock its owner out. Prefer throttling
(exponential backoff) over hard lockout, and never let the response distinguish
"locked" from "wrong password", which would undo the uniform-failure property
Step 9 §2.12 protects.

**Effort:** small-to-medium. **Do in:** Phase 2.

---

## 2. Refresh tokens cannot be revoked, and logout is client-side only

**Where:** `services/auth/internal/service/auth.go:86-88` says it outright —
*"refresh tokens are stateless by design; no revocation list exists."*

**Now:** a refresh token is valid for **7 days** from issue. There is no
server-side logout endpoint (verified: none exists). The frontend's "Sign out"
drops the tokens from memory — which is genuinely all it *can* do — but the
token itself stays valid for the rest of its lifetime. Anyone holding a copy
keeps minting access tokens, and there is no way to stop them short of rotating
`JWT_SECRET`, which logs out every user at once.

**Why it matters, and why it pairs with Phase 2:** with a trading engine, a
leaked refresh token is a week of authenticated access to someone's positions
and order history. "Sign out" not actually ending a session is also the kind of
thing users reasonably assume works.

**Shape when done:** a token store (Redis, already a dependency) keyed by a
`jti` claim, plus a real `POST /auth/logout`. Two designs worth weighing:
a denylist of revoked `jti`s until natural expiry (simple, storage grows with
revocations), or refresh-token rotation with reuse detection (stronger — reuse
of an already-spent token signals theft and can revoke the whole family, per
OAuth 2.0 BCP).

**Note if rotation is chosen:** the frontend's API client already shares one
in-flight refresh across concurrent 401s
(`frontend/src/api/client.ts`, Step 8 spec §2.6). That was an efficiency measure
under today's stateless refresh — **under rotation it becomes a correctness
requirement**, because seven parallel refreshes would burn seven tokens and look
exactly like token theft to a reuse detector. The client comment says so; do not
remove it.

**Effort:** medium. **Do in:** Phase 2.

---

## 3. bcrypt, and the 72-byte ceiling it imposes

**Where:** `services/auth/internal/service/auth.go` — `bcryptCost = 10`.

**Now:** bcrypt at cost 10, which meets OWASP's stated minimum. But bcrypt
hard-caps input at **72 bytes** (`golang.org/x/crypto` returns
`ErrPasswordTooLong` above it — it does not silently truncate, which is the good
outcome).

**Why it matters:** NIST SP 800-63B §3.1.1.2 says verifiers *SHOULD* permit at
least 64 **characters**. For ASCII, 72 bytes clears that. For multi-byte scripts
it does not — a 64-character password in Cyrillic or CJK exceeds 72 bytes and is
rejected. Recorded as a documented deviation in the Step 9 spec §2.4 rather than
left unnoticed.

**Shape when done:** migrate to **Argon2id** (OWASP's first choice for new
systems), which has no such ceiling. Existing bcrypt hashes stay valid and are
upgraded opportunistically: on successful login, re-hash the just-verified
plaintext with Argon2id and replace the stored value. Never a bulk re-hash —
the plaintext is only available at login.

**Effort:** medium. **Do in:** Phase 2 or 3. Not urgent — bcrypt at cost 10 is
not broken, and the deviation affects a narrow set of users.

---

## 4. Request body size is capped only on the auth service

**Where:** Step 9 §2.11 adds `http.MaxBytesReader` (64 KiB) to the auth handlers.

**Now:** that covers `/auth/*` only. The gateway proxies `/market-data/*` — and
will proxy `/trading/*` — with no body limit, and the market-data service sets
none of its own. Step 7 §8 deferred body limits at the gateway explicitly.

**Why it matters:** an unbounded body is buffered before any handler logic runs,
so no amount of validation downstream helps. Cheap to exhaust memory with a
handful of large requests.

**Shape when done:** a body-size middleware at the gateway covering every
proxied route, so new services inherit it rather than each remembering.
**Phase 2 adds `/trading/*` with order payloads** — the natural moment to do
this is when that route stops returning `501`.

**Effort:** small. **Do in:** Phase 2, alongside the trading routes.

---

## 5. No online breach-corpus check

**Where:** Step 9 §2.5 implements an embedded blocklist; §7 defers the online
lookup.

**Now:** registration checks an embedded list of common 15+ character passwords,
context terms, and trivial patterns. That satisfies the NIST *SHALL* to compare
against a blocklist, but the list is necessarily small.

**Shape when done:** Have I Been Pwned's Pwned Passwords API using k-anonymity
(send the first 5 hex characters of the SHA-1, match suffixes locally — the full
hash never leaves the process). Strictly stronger than any list we would ship.

**Why deferred:** it puts a third-party network call on the registration path,
with new latency, a new failure mode, and a fail-open/fail-closed decision that
deserves being made on purpose rather than by default.

**Effort:** small, but needs the availability decision made. **Do in:** Phase 4,
or earlier if registration ever faces the open internet.

---

## 6. Backends trust anything that reaches them

**Where:** Step 7 spec §2.4. Also summarised in `docs/deferred-tuning.md`.

**Now:** the auth and market-data services have no authentication of their own.
The Phase 1 control is network isolation: every service binds `127.0.0.1` by
default (`BIND_ADDR`), and `docker-compose.yml` binds published ports to
loopback. Anyone who can reach `:8081` or `:8082` directly bypasses the gateway
and its JWT check entirely.

**Why it matters later:** loopback binding is a correct and sufficient control
while everything shares one host. It stops being sufficient the moment services
run on separate hosts, in separate containers on a shared network, or behind a
load balancer.

**Shape when done:** mTLS between gateway and backends, or signed internal
tokens. Decide alongside the deployment topology rather than in the abstract.

**Effort:** medium-large. **Do in:** Phase 4, with deployment.

---

## 7. No TLS, and the CORS origin is compile-time

**Where:** everything is plain HTTP on localhost. `allowedOrigin` is a hardcoded
constant, `services/gateway/cmd/server/main.go:18`.

**Now:** access and refresh tokens travel in cleartext. On loopback that is
fine — the traffic never leaves the machine. The hardcoded CORS origin was a
deliberate Step 7 call ("a CORS origin is not a knob") and is right for a
single-origin local app.

**Why it matters later:** without TLS in a deployed environment, every token is
readable in transit, which makes every other control in this file irrelevant.
The CORS origin also has to become configurable, since the deployed frontend
will not be on `localhost:5173`.

**Shape when done:** TLS terminated at the load balancer or the gateway;
`ALLOWED_ORIGIN` moves to env with no default, failing fast if unset — an empty
default would be worse than a hardcoded one.

**Effort:** small in code, mostly infrastructure. **Do in:** Phase 4, and it is
a hard prerequisite for any public deployment.

---

## Suggested order

| # | Item | Phase | Effort |
|---|---|---|---|
| 1 | Rate limiting on auth routes | **2** | S–M |
| 2 | Refresh-token revocation + real logout | **2** | M |
| 4 | Gateway-wide body size cap | **2** (with `/trading/*`) | S |
| 3 | Argon2id migration | 2–3 | M |
| 5 | HIBP breach lookup | 4 | S + a decision |
| 6 | Service-to-service auth | 4 | M–L |
| 7 | TLS + configurable CORS origin | 4 | S code, infra-heavy |

Items 1, 2, and 4 are the Phase 2 set. Doing them alongside the trading engine
costs little and closes the gaps that Phase 2 itself makes consequential.
