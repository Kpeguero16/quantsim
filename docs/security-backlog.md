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

## 1. No rate limiting on `/auth/login` — ~~highest priority~~ **CLOSED (Step 11, 2026-08-14)**

**Done.** Per-IP limiting on `/auth/*` and per-account exponential backoff on
`/auth/login`, both at the gateway. Full reasoning in `SPEC.md` (Step 11);
implementation in `services/gateway/internal/limiter/` and
`internal/middleware/ratelimit.go`.

Two things this entry said turned out to be **wrong**, and are corrected here
rather than left to be rediscovered and believed:

**❌ "The gateway calls `r.SetXForwarded()` … a per-IP limiter can therefore
trust `X-Forwarded-For`."** It cannot. That call runs on `r.Out` inside
`proxy.New`'s `Rewrite` (`services/gateway/internal/proxy/proxy.go:59`), which
executes **after** every middleware and builds only the **upstream** request.
The inbound header a middleware sees is whatever the client sent, and nothing
sanitises it — because until Step 11 nothing read it. A limiter keying on that
header would have been bypassable with one forged header per request: an
unlimited budget, from a control that still looked like it was working. The
limiter keys on `r.RemoteAddr`, and a test fails if that ever changes.

**❌ "Redis is already a dependency, so a shared counter does not add
infrastructure."** True of the stack, false of the gateway —
`services/gateway/go.mod` required only `chi` and `pkg`; only market-data
imports `go-redis`. Counters are held in memory instead, which adds no
dependency and removes the fail-open/fail-closed question entirely, since an
in-process store cannot be unavailable. See `SPEC.md` §2.1 and
`docs/deferred-tuning.md` §4 for the trade and its trigger.

**✅ What this entry got right:** the warning about lockouts. A per-account
hard lockout was rejected for exactly the reason given — it hands anyone who
knows an email a denial-of-service primitive against its owner — and the
implementation uses decaying exponential backoff, with one identical `429` for
both limiter dimensions so a refusal cannot report which one fired.

**Residual risk, accepted:** an attacker can still degrade a known victim's
login for up to ~15 minutes by failing on purpose. Bounded, self-healing, and
strictly better than the alternative. `SPEC.md` §2.3.

---

## 2. Refresh tokens cannot be revoked, and logout is client-side only — **CLOSED (Step 13, 2026-08-17)**

**Done.** `POST /auth/logout` revokes the presented refresh token; `Refresh`
rejects a revoked token before issuing a new pair. Redis-backed, keyed by a
`jti` claim added to every token (access and refresh alike). Full design in
`SPEC.md` (Step 13; archived alongside prior steps once the next spec is
drafted, per `docs/NEXT_SESSION.md`'s convention).

One thing this entry said turned out to need a correction, in the same spirit
as item 1's self-correction above:

**❌ "a token store (Redis, already a dependency)."** True of the workspace
(`services/market-data` already used it), **not true of `services/auth`**,
which had zero Redis usage before this step. `go-redis` is a genuinely new
dependency there — SPEC.md §2.2 caught this before implementation rather than
after, the same distinction item 1 draws between "the stack" and "the specific
service."

**✅ What this entry got right:** the denylist-vs-rotation framing, and
specifically the rotation warning. Denylist was chosen (SPEC.md §2.1) — simpler,
closes the actual stated problem (no kill switch), and doesn't force
`frontend/src/api/client.ts`'s shared in-flight-refresh promise to become a
correctness requirement instead of an optimization. That trap is exactly what
this entry predicted, and it's still live if rotation is ever chosen later —
the client comment still says so.

**Residual risk, accepted:** access tokens are not revoked, only refresh
tokens — a stolen access token is valid for its full 15-minute life regardless
of logout. Bounded and short-lived by design, the same shape of accepted
residual risk as item 1's. A Redis outage also makes revocation fail open
(SPEC.md §2.3): a token revoked moments before the outage could still refresh
until Redis recovers. Both are documented trade-offs, not oversights.

**Effort:** medium. **Done in:** Phase 2 (Step 13).

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

**Partially addressed by Step 11.** The per-account limiter has to read
`/auth/login`'s body to find the email, so it caps that read at 64 KiB
(`maxLoginBodyBytes` in `services/gateway/cmd/server/main.go`). That is one
route, not the gateway-wide middleware this item asks for, and it deliberately
**forwards** an oversized body rather than rejecting it — the gateway does not
own login's validation rules. Treat it as a worked example of the shape, not
as the item being done.

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

## 8. Passwords are not Unicode-normalised before hashing

**Where:** `services/auth/internal/service/validate.go` — the password is length-
checked and blocklist-checked, then passed to bcrypt exactly as received.

**Now:** NIST SP 800-63B §3.1.1.2 says verifiers *SHOULD* apply NFKC or NFC
normalisation to the password before hashing. We do not. Verified rather than
assumed: the same visually identical passphrase entered precomposed (NFC) vs
decomposed (NFD) is **22 vs 25 bytes**, and registration accepts both. They
therefore produce different bcrypt hashes.

**Why it matters:** a user whose password contains an accented or non-Latin
character can be locked out of their own account by typing it on a different
keyboard, OS, or input method than the one they registered with — the string
looks identical on screen and compares unequal in bytes. It is a correctness
bug that presents to the user as "my password stopped working."

**Shape when done:** apply `golang.org/x/text/unicode/norm` (NFC) to the
password in the service layer before hashing and before comparing, and to the
username while you are there. Note that `golang.org/x/text` is already an
indirect dependency of the auth module.

**Why it is worth doing early, despite affecting nobody today:** the asymmetry
is the whole argument. Right now no non-ASCII password exists, so normalising
is free and invisible. Once one exists, adding normalisation *changes* the hash
that user authenticates against and locks them out — the same class of problem
as item 3's rehash-on-login, needing the same machinery to fix. Cheap now,
expensive exactly when it stops being theoretical.

**Effort:** extra small. **Do in:** Phase 2, ideally alongside item 3 since both
touch the hashing path — but it is worth doing on its own even if Argon2id slips.

---

## Suggested order

| # | Item | Phase | Effort |
|---|---|---|---|
| ~~1~~ | ~~Rate limiting on auth routes~~ | **done — Step 11** | — |
| 2 | Refresh-token revocation + real logout | **2** | M |
| 4 | Gateway-wide body size cap | **2** (with `/trading/*`) | S |
| 8 | Unicode-normalise passwords before hashing | **2** (cheap now, lockout later) | XS |
| 3 | Argon2id migration | 2–3 | M |
| 5 | HIBP breach lookup | 4 | S + a decision |
| 6 | Service-to-service auth | 4 | M–L |
| 7 | TLS + configurable CORS origin | 4 | S code, infra-heavy |

Items 1, 2, 4, and 8 are the Phase 2 set. Doing them alongside the trading engine
costs little and closes the gaps that Phase 2 itself makes consequential.
**Item 1 is done** (Step 11, 2026-08-14); **item 2 is now the highest-priority
open item**, and it is the one that makes "sign out" mean something before
`/trading/*` starts moving a balance.
