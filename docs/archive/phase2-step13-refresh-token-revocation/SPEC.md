# SPEC — Refresh-Token Revocation and Logout (Step 13)

Status: **Approved 2026-08-17.** All six decisions resolved as recommended; §9 records them. Implementation is unblocked.
Scope: `pkg/auth`, `services/auth` (service, handler, router, store, mock, `go.mod`), `services/auth/integration`, `frontend/src/api/client.ts`, `frontend/src/auth/AuthProvider.tsx`. No gateway code changes — `/auth/*` is already proxied and rate-limited as a wildcard (`services/gateway/internal/handler/router.go`).

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase2-step12-store-integration-harness/`.

---

## 1. Objective

`docs/security-backlog.md` item 2: refresh tokens are valid for 7 days with no server-side kill switch. `services/auth/internal/service/auth.go:86-88` says it outright — *"refresh tokens are stateless by design; no revocation list exists."* There is no `POST /auth/logout`; the frontend's "sign out" only drops tokens from memory (`AuthProvider.tsx`'s `clearSession`, `AuthProvider.tsx:33-37`), and the token itself stays valid for up to 7 more days in anyone else's hands.

**Why now.** This is the highest-priority open item in the security backlog and `docs/NEXT_SESSION.md`, and it is sequenced deliberately ahead of the trading engine: once `/trading/*` executes real orders, a leaked refresh token stops being "read-only access to public market data" and becomes a week of authenticated access to someone's positions.

**Objective:** a real logout that actually ends a session — `POST /auth/logout` revokes the presented refresh token so it can never mint another access token again, using Redis (already running via `make docker-up`, already a workspace dependency via `services/market-data`) as the revocation store.

**Non-goals:**
- **Refresh-token rotation.** Considered and not recommended for this step — §2.1.
- **Access-token revocation.** Access tokens stay valid on signature and expiry alone. At a 15-minute TTL this is the accepted residual risk already established for the equivalent tradeoff in `docs/security-backlog.md` item 1 — bounded, self-healing, cheaper than adding a second revocation check to `pkg/auth.RequireAuth`, which the gateway also calls and which has no other dependency on any service's internals today (`pkg/auth/jwt.go`'s package comment).
- **"Log out everywhere" / revoke-all-for-user.** Nothing in the system today models multiple concurrent sessions per user; adding that concept is a bigger feature than this backlog item asks for.
- **Argon2id, breach-corpus checks, or any other item from `docs/security-backlog.md`.** Item 2 only.

---

## 2. Design decisions

### 2.1 A `jti` denylist in Redis, not rotation-with-reuse-detection

Two designs, per the backlog doc's own framing:

**(a) Denylist.** Every token gets a `jti` claim. `POST /auth/logout` writes it to Redis with a TTL equal to the token's remaining lifetime; `Refresh` checks the presented token's `jti` against that set before issuing a new pair. Simple, and the storage question resolves itself: a Redis key with `TTL = time.Until(expiry)` disappears on its own at natural expiry, so there is no cleanup job and no unbounded growth — only ever as many keys as there are refresh tokens revoked in the last 7 days.

**(b) Rotation with reuse detection.** Every `Refresh` call also revokes the token it was just handed and issues a new one from the same "family"; presenting an already-rotated token revokes the whole family and signals theft (OAuth 2.0 BCP). Stronger — it can detect theft, not just enable logout — but it is a bigger change with a specific, already-documented trap: `frontend/src/api/client.ts`'s shared in-flight-refresh promise (`client.ts:118-127`) is today an efficiency measure that tolerates duplicate refreshes because they're harmless. Under rotation, duplicate refreshes burn tokens from the same family and look exactly like theft to a reuse detector — the shared promise stops being an optimization and becomes a correctness requirement, and the client would also need a new codepath to distinguish "refresh failed, token expired" from "refresh failed, theft detected, do not silently retry."

**Recommendation: (a).** The backlog item's stated problem is "there is no kill switch and logout doesn't work" — a denylist closes that completely. Rotation's marginal benefit is theft *detection*, which is a materially different and larger feature than the one being asked for, and it is the one design in this project explicitly flagged as easy to get wrong in a file this project has otherwise kept simple on purpose. A denylist is also not a dead end: `jti` tracking is the prerequisite either design needs, so choosing (a) now doesn't foreclose (b) later if the threat model changes.

### 2.2 A new module dependency: `github.com/redis/go-redis/v9` in `services/auth/go.mod`

Per Step 7 §8 / Step 11, any new module dependency is an ask-first decision — Step 11 held itself to zero new dependencies for exactly this reason, and `docs/security-backlog.md` item 1 already corrected itself once on this exact point: *"'Redis is already a dependency, so a shared counter does not add infrastructure.' True of the stack, false of the gateway."* The same scrutiny applies here: true of the workspace (`services/market-data/go.mod` already has it), **false of `services/auth` today.**

The alternative — a TTL-expiring denylist in Postgres — was considered and rejected: Postgres has no native expiring row, so it would need a scheduled cleanup job (`DELETE WHERE expires_at < now()`) that doesn't exist as a concept anywhere in this codebase yet, for a problem Redis solves natively with a key TTL. It would also be the first write path into `services/auth`'s store outside the existing user/account tables, where Step 12's harness has no coverage.

**Recommendation: add the dependency.** It is a one-line `go.mod` change, the client usage pattern already has a working example to follow (`services/market-data/internal/cache/redis_price_cache.go`), and `docker-compose.yml`'s Redis service is already running for anyone with `make docker-up` up.

### 2.3 Fail open on a Redis error at request time

Introducing Redis into `services/auth` raises the fail-open/fail-closed question `docs/security-backlog.md` item 5 deferred for HIBP lookups and says plainly "deserves being made on purpose rather than by default." Two paths touch Redis, and both take an error the same way:

- **`Refresh`**, checking whether the presented `jti` is revoked.
- **`Logout`**, writing the revoked `jti`.

**Recommendation: fail open, and log.** If Redis is unreachable, `Refresh` treats "cannot confirm revoked" as "not revoked" and proceeds — identical to today's behavior, so a Redis outage does not become a second, unrelated way for every active session in the app to be logged out every 15 minutes. `Logout` returns success to the caller either way: the frontend's "sign out" already clears the local session unconditionally as its primary effect (`AuthProvider.tsx`'s own docstring: "genuinely all it can do"), so a network- or Redis-level failure during the best-effort server call must not surface as a failed sign-out. Both paths `log.Printf` the error so an outage is visible in the service log, matching how `RATE_LIMIT_ENABLED=false` already logs a warning rather than failing silently.

This is a real, bounded tradeoff, not a default: for the duration of a Redis outage, a token revoked moments earlier could still refresh successfully. Same shape as the accepted residual risk in `docs/security-backlog.md` item 1 — bounded to the outage window, self-healing the moment Redis comes back.

### 2.4 `jti` on both token types; the revocation check stays out of `pkg/auth`

`pkg/auth.Claims` embeds `jwt.RegisteredClaims`, which already has an `ID` field for this — just never populated (`pkg/auth/jwt.go`). `GenerateToken` sets `RegisteredClaims.ID = uuid.NewString()` unconditionally, for both access and refresh tokens: one code path, and every token gets a stable identifier for free even though only refresh tokens are ever checked against the denylist.

**The revocation check itself lives in `services/auth/internal/service`, not in `pkg/auth.ValidateToken` / `RequireAuth`.** `pkg/auth/jwt.go`'s own package comment is explicit: *"It has no dependency on any service's internal packages"* — and `RequireAuth` is what the gateway calls to gate `/market-data/*` and `/trading/*`. Pulling a Redis check into that shared, dependency-free package would force the gateway to gain a Redis dependency too, for a check this step doesn't need there (§1 non-goals — access tokens are not revoked). `Service.Refresh` and the new `Service.Logout` call `pkgauth.ValidateToken` exactly as `Refresh` does today, then consult the revocation store themselves.

### 2.5 `POST /auth/logout` is unauthenticated, keyed on the refresh token, and returns `200 {}`

**Request shape mirrors `/auth/refresh` exactly:** `{"refresh_token": "..."}`, no `Authorization` header required. Same reasoning as `Refresh` — by the time a user wants to log out, their 15-minute access token may already be expired, and the refresh token is what's actually being revoked, so it's what authorizes the call.

**Validation reuses `Refresh`'s first two checks** (`pkgauth.ValidateToken`, then `claims.TokenType == TokenTypeRefresh`) before touching the store — a malformed token or an access token presented as a refresh token gets the same `ErrTokenInvalid` → `401 invalid_token` `Refresh` already returns, not a new error vocabulary for one endpoint.

**Response is `200 OK` with an empty JSON object, not `204 No Content`.** This is a deliberate, narrow fix to something that would otherwise break: `frontend/src/api/client.ts`'s generic `request<T>` helper unconditionally does `await response.json()` on any `response.ok` (`client.ts:237`), and a 204 has no body — that call throws, and gets turned into a spurious `invalid_response` `ApiError` on what was actually a successful logout. Every other endpoint in this API already returns a JSON body on success; giving logout one too keeps `request<T>` untouched rather than special-casing one status code across every call site that uses it.

### 2.6 Redis integration tests use a dedicated logical DB, not the guard chain from Step 12

Nothing about this feature is destructive the way Step 12's `TRUNCATE`/`DROP` risk was — `Revoke`/`IsRevoked` only ever touch keys under a `revoked:` prefix, which cannot collide with `services/market-data`'s `price:`/`prices:` keys (`redis_price_cache.go`) even on the same logical DB. But relying on prefix discipline alone to protect the dev cache is the same shape of mistake Step 12's pre-merge review flagged in a different guise: a convention that happens to hold isn't a guard.

**Recommendation:** tests connect to Redis logical DB 15 (the long-standing `redis-cli -n 15` testing convention), which nothing else in this app ever selects — `REDIS_URL` for both `market-data` and `auth` defaults to `/0`. Isolation by construction, no guard chain needed, and it's a plain `FLUSHDB` between tests instead of a truncate-and-reseed cycle.

---

## 3. API

### `POST /auth/logout`

| | |
|---|---|
| Request | `{"refresh_token": string}` |
| `200` | `{}` — the token is revoked (or the request raced a Redis outage and revocation was best-effort — §2.3; the client cannot tell the difference and does not need to) |
| `401 invalid_token` | malformed, expired, wrong-signature, or an access token presented as a refresh token — identical shape to `Refresh`'s existing 401 |
| `400 invalid_request` | missing `refresh_token`, mirrors `Refresh`'s existing check (`handler/auth.go:138`) |

### `POST /auth/refresh` (behavior change, same contract)

No change to the request/response shape. New failure mode: a syntactically-valid, unexpired refresh token whose `jti` is in the denylist now returns the same `401 invalid_token` as any other rejected token, rather than silently issuing a fresh pair.

---

## 4. Project structure

```
pkg/auth/jwt.go                                    # GenerateToken sets RegisteredClaims.ID

services/auth/internal/service/
  interfaces.go                                     # + RevocationStore interface
  auth.go                                            # Refresh checks revocation; + Logout method
  errors.go                                          # no new sentinel -- reuses ErrTokenInvalid
  mock/
    mock.go                                          # + in-memory RevocationStore double

services/auth/internal/store/
  redis_token_store.go                               # RevocationStore over go-redis, mirrors
                                                       # services/market-data/internal/cache/redis_price_cache.go

services/auth/internal/handler/
  auth.go                                            # + Logout handler
  router.go                                           # + POST /auth/logout, public group

services/auth/cmd/server/main.go                     # + REDIS_URL wiring, redis.NewClient

services/auth/integration/
  revocation_store_test.go                            # //go:build integration, DB 15 -- §2.6

services/auth/go.mod                                  # + github.com/redis/go-redis/v9

frontend/src/api/client.ts                            # + api.logout(refreshToken)
frontend/src/auth/AuthProvider.tsx                    # logout: best-effort api.logout, then clearSession
```

`Service.NewService`'s signature changes to accept a `RevocationStore`, which touches every call site: `services/auth/cmd/server/main.go`, and every test that builds a `*service.Service` directly (`handler/auth_test.go`'s `newTestRouter`, any `service`-package unit tests). Mechanical, but not small in file count — flagged so it isn't a surprise mid-implementation.

---

## 5. Configuration

| Variable | Meaning |
|---|---|
| `REDIS_URL` | **Not new** — `.env.example` already has this for `market-data`, and the Makefile's `-include .env` + `export` (line 1-2) puts it in every `make run-*` target's environment already. `services/auth`'s `main.go` just also reads it, required with boot-time `log.Fatal` if unset — mirrors `services/market-data/cmd/server/main.go`'s existing check on the same variable. Both services connect to the same Redis instance, same logical DB 0; no collision, since keys are namespaced (`price:`/`prices:` vs `revoked:`). |
| `TEST_REDIS_URL` | Optional override for the integration suite, same convention as `TEST_DATABASE_URL` (§4 of the archived Step 12 spec). Default: `REDIS_URL` with the DB index replaced by `15`. |

`.env.example`'s existing `REDIS_URL` comment gets a one-line addition noting it's now read by both services — no new variable, no new entry.

---

## 6. Testing strategy

**Unit (mock `RevocationStore`, no Docker):**
| Test | What it proves |
|---|---|
| `Refresh` with a `jti` the mock reports as revoked → `ErrTokenInvalid` | the check actually gates the flow |
| `Refresh` with an unrevoked `jti` → succeeds, unchanged from today | no regression on the common path |
| `Logout` with a valid refresh token → mock's `Revoke` called with that `jti` and a positive TTL | the write happens with the right key and a bounded lifetime |
| `Logout` with an access token → `ErrTokenInvalid`, mock's `Revoke` never called | mirrors `Refresh`'s existing `TokenTypeRefresh` check |
| `Logout` with a garbage token → `ErrTokenInvalid` | same as `Refresh`'s existing garbage-token case |
| Handler: `POST /auth/logout` success → `200`, body `{}` | §2.5's response-shape decision, pinned so it can't regress to `204` |
| Handler: missing `refresh_token` → `400 invalid_request` | mirrors the existing `Refresh` handler test |

**Integration (`//go:build integration`, real Redis, DB 15 — §2.6):**
| Test | What only a real Redis proves |
|---|---|
| `Revoke` then `IsRevoked` → `true` | the actual `SET`/`EXISTS` round-trip |
| `Revoke` with a 1-second TTL, `IsRevoked` after 2 seconds → `false` | Redis's TTL expiry actually fires — nothing about this is exercised by the mock |
| Two different `jti`s don't collide | key construction is correct |

**Mutation check**, following Step 12's standard: temporarily make `Refresh` skip the revocation check entirely, confirm the "revoked token still refreshes" unit test fails, then revert.

**Manual, end to end** (this step touches the frontend, and `agents.md` treats UI changes as needing a real run, not just green tests): `make docker-up && make run-auth && make run-gateway && make run-frontend`, log in, click sign out, confirm the network tab shows `POST /auth/logout` returning `200`, then manually replay the old refresh token with `curl` against `:8080/auth/refresh` and confirm `401`.

---

## 7. Code style

- `RevocationStore` mirrors `UserStore`'s existing shape: a small interface in `service/interfaces.go`, a real (here Redis-backed, not Postgres) implementation in `internal/store`, an in-memory double in `service/mock`, and a compile-time `var _ service.RevocationStore = (*store.RedisTokenStore)(nil)` assertion (Step 12 §3 established why this specific assertion matters — mock and real store drift apart silently without it).
- Errors compared with `errors.Is`, never string matching — house convention.
- No new sentinel error; `Logout` reuses `ErrTokenInvalid` for every rejection, matching `Refresh`'s existing "one failure vocabulary" comment in `errors.go`.
- Comments explain why, not what — house standard.

---

## 8. Commands

```bash
make docker-up          # Postgres + Redis
make test                # unit tests, all four modules, no Docker needed
make test-integration    # extends to cover services/auth/integration/revocation_store_test.go
make vet                 # includes the -tags=integration pass
```

No new Makefile target — the new integration test file lives in the package `make test-integration` already runs.

---

## 9. Decisions resolved before implementation

Resolved 2026-08-17, all as recommended:

| # | Decision | Resolution |
|---|---|---|
| 1 | Revocation design | **Denylist**, not rotation-with-reuse-detection — §2.1 |
| 2 | New dependency `go-redis` in `services/auth` | **Add it** — §2.2 |
| 3 | Redis unreachable at request time | **Fail open, log the error** — §2.3 |
| 4 | `jti` scope and where the check lives | **Both token types get one; only `services/auth` checks it, not `pkg/auth`** — §2.4 |
| 5 | Logout response | **`200 {}`, not `204`** — avoids a real bug in `client.ts`'s generic response handling — §2.5 |
| 6 | Redis test isolation | **Logical DB 15**, no guard chain — §2.6 |

---

## 10. Implementation

Not started. `tasks/plan.md` and `tasks/todo.md` get created once this spec is approved, per the gated workflow (`agents.md`: spec reviewed → plan → checkpoints).
