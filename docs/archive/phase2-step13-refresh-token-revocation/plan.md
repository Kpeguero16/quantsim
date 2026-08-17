# Implementation Plan — Refresh-Token Revocation and Logout (Step 13)

## Overview

`SPEC.md` is **approved**; §9 records the six decisions. Build a real logout: `POST /auth/logout` revokes the presented refresh token via a Redis `jti` denylist, and `Refresh` rejects a revoked `jti` before issuing a new pair.

The step adds one new module dependency (`go-redis` in `services/auth`), one new store type, one new service method, one new endpoint, and a small frontend change. **No gateway changes, no rotation, no access-token revocation.**

**Nothing here is destructive the way Step 12's `TRUNCATE`/`DROP` risk was** (SPEC.md §2.6) — the main way this step can go wrong is quieter: shipping a revocation check that never actually fires (the exact failure shape Step 12's pre-merge review found once already, in a different guard), or a `NewService` signature change that leaves a call site silently uncompiled. Task ordering and the mutation check in Task 2 exist to rule both out.

## Architecture decisions

Restated from `SPEC.md` §2/§9, all resolved as recommended:

- **A `jti` denylist in Redis**, not rotation-with-reuse-detection — §2.1
- **`go-redis` is a new dependency in `services/auth/go.mod`**, approved — §2.2
- **Fail open on a Redis error at request time, and log it** — §2.3
- **Every token gets a `jti`; only `services/auth` checks it, never `pkg/auth`** — §2.4
- **`POST /auth/logout` is unauthenticated, keyed on the refresh token, returns `200 {}`** — §2.5
- **Redis integration tests use logical DB 15**, no guard chain — §2.6

## Dependency order

```
Task 1 (store layer: jti, interface, Redis impl, mock, integration tests)
   │
   v
Task 2 (service layer: Refresh check, Logout, NewService rewiring, mutation check)
   │
   v
Task 3 (handler + router: POST /auth/logout)
   │
   v
Task 4 (frontend: client.ts, AuthProvider)
   │
   v
Task 5 (docs + backlog closeout + full verification)
```

Linear on purpose. Each task's contract is defined in `SPEC.md` §3-§4, so Task 4 could in principle start against the documented API before Task 3 lands — but sequential keeps every checkpoint runnable end to end, which matters more than parallelism at this step's size.

---

## Phase 1 — The store layer

### Task 1 — `jti`, `RevocationStore`, the Redis implementation, the mock, and its integration tests

**Files:**
- `pkg/auth/jwt.go` — `GenerateToken` sets `RegisteredClaims.ID = uuid.NewString()`
- `pkg/auth/jwt_test.go` — new file; no such test exists today
- `services/auth/internal/service/interfaces.go` — `+ RevocationStore` interface
- `services/auth/internal/store/redis_token_store.go` — new file, `RedisTokenStore`
- `services/auth/internal/service/mock/revocation_store.go` — new file, in-memory double
- `services/auth/integration/main_test.go` — extended to also attempt a Redis connection
- `services/auth/integration/revocation_store_test.go` — new file, `//go:build integration`
- `services/auth/go.mod` — `+ github.com/redis/go-redis/v9`

**`RevocationStore` interface** (`interfaces.go`, alongside `UserStore`):
```go
type RevocationStore interface {
    Revoke(ctx context.Context, jti string, ttl time.Duration) error
    IsRevoked(ctx context.Context, jti string) (bool, error)
}
```

**`RedisTokenStore`** mirrors `services/market-data/internal/cache/redis_price_cache.go`'s shape: a thin wrapper over `*redis.Client`, key prefix `revoked:` + jti (parallel to that file's `price:`/`prices:` prefixes so the two services can never collide on the same Redis instance). `Revoke` is `SET key "1" EX ttl`; `IsRevoked` is `EXISTS key`.

**`mock.RevocationStore`**: an in-memory `map[string]bool` (TTL is not modeled — real expiry is what Task 1's integration test proves, not the mock; see `SPEC.md` §6's unit/integration split). Exported `RevokeErr` / `IsRevokedErr` fields, set-to-simulate-an-error, the way `mock.UserStore` already has `GetByEmailErr` / `GetByIDErr` — Task 2 needs these to test the fail-open path (§2.3) without a real Redis outage.

**Extending `services/auth/integration/main_test.go`:** the package's one `TestMain` currently gates only on Postgres. Add a second, independent skip category — `redisSkipReason` alongside the existing `skipReason` — so a Redis-only test skips on "Redis unreachable" without requiring Postgres tests to also pass, and vice versa. This is a real, if small, change to shared test infra; called out explicitly rather than folded in silently.

`TEST_REDIS_URL` resolution mirrors `TEST_DATABASE_URL` (`SPEC.md` §5): explicit override if set, else `REDIS_URL` with the DB index replaced by `15`. No admin connection needed — Redis has no `CREATE DATABASE` step, just `SELECT 15` (or the DSN's path segment) and a `FLUSHDB` before each test.

**Acceptance criteria:**
- `pkg/auth/jwt_test.go`: two calls to `GenerateToken` produce different, non-empty `jti`s; `ValidateToken` round-trips the claim
- `RevocationStore` defined; both `RedisTokenStore` and `mock.RevocationStore` satisfy it, proven by `var _ service.RevocationStore = (*store.RedisTokenStore)(nil)` and the mock equivalent
- `services/auth/go.mod` diff is the `go-redis` require block and nothing else
- `go build ./...` in `services/auth` succeeds — `RedisTokenStore` compiles but is not wired into `main.go` yet, so this task changes no running behavior
- Integration: `Revoke` + `IsRevoked` round-trips true; a 1-second-TTL key reads `false` after 2 seconds (real expiry — the mock cannot prove this); two different `jti`s don't collide; the suite skips (not fails) when Redis is down, matching Postgres's existing skip behavior
- Every Redis integration test verifiably targets DB 15 — assert on the resolved DSN in a setup check, the same spirit as `assertTestDB` in Step 12, scaled to the actual (much lower) risk here

**Verify:**
```bash
cd pkg && go test ./...
cd services/auth && go build ./...
make test-integration   # new PASS lines for the Redis tests; still skips cleanly with Docker down
```

**Checkpoint — stop for review.** The store layer exists, compiles, and is proven against a real Redis. Nothing in the running service behaves differently yet.

---

## Phase 2 — Wiring the service

### Task 2 — `Refresh` checks revocation, `Logout` is added, `NewService` is rewired

**Files:**
- `services/auth/internal/service/auth.go` — `Refresh` gains the check; `+ Logout`
- `services/auth/internal/service/auth_test.go` — new test cases; **20 existing `NewService(users, testSecret)` call sites** need the new parameter
- `services/auth/internal/handler/auth_test.go` — `newTestRouter`'s one call site, same change
- `services/auth/cmd/server/main.go` — `REDIS_URL` read + `log.Fatal` if unset (mirrors `services/market-data/cmd/server/main.go`'s existing check on the same variable — this is not a new env var, `SPEC.md` §5), `redis.ParseURL` + `redis.NewClient`, `store.NewRedisTokenStore(...)`, passed into `NewService`

**This sounds bigger than it is.** Every existing call site is the identical literal string `service.NewService(users, testSecret)` (verified: 20 occurrences in `auth_test.go`, 1 in `handler/auth_test.go`). Updating it to `service.NewService(users, mock.NewRevocationStore(), testSecret)` is one find-and-replace across two files, not 21 separate edits. `main.go`'s call site is textually different (`userStore`, not `users`) and gets its own one-line change alongside the Redis client setup.

**`NewService` new signature:** `NewService(users UserStore, revocations RevocationStore, jwtSecret []byte) *Service` — inserted between the two existing parameters rather than appended, so the token-related parameters (`revocations`, `jwtSecret`) sit together.

**`Refresh` change:** after the existing `ValidateToken` + `TokenTypeRefresh` check and before the user-existence lookup (cheaper to reject on a Redis hit than to round-trip Postgres for a token that's about to fail anyway) —
```go
revoked, err := s.revocations.IsRevoked(ctx, claims.ID)
if err != nil {
    log.Printf("auth: revocation check unavailable, failing open: %v", err)
} else if revoked {
    return nil, ErrTokenInvalid
}
```
`log.Printf` inside `internal/service` has a precedent: `services/market-data/internal/service/live.go` already logs directly from that layer, so this isn't a new pattern for the codebase, just a new call site for it in `services/auth`.

**`Logout` method:** validates exactly like `Refresh`'s first two checks (`pkgauth.ValidateToken`, then `TokenTypeRefresh`), then:
```go
ttl := time.Until(claims.ExpiresAt.Time)
if ttl > 0 {
    if err := s.revocations.Revoke(ctx, claims.ID, ttl); err != nil {
        log.Printf("auth: revocation write failed: %v", err)
    }
}
return nil
```
An already-expired token is not revoked — there's nothing to protect, and a zero-or-negative TTL would be a malformed Redis call anyway. Errors are logged, never returned — §2.3's fail-open applies to the write path too, matching the frontend's own "genuinely all it can do" framing for sign-out.

**Acceptance criteria:**
- `Refresh` with a `jti` the mock reports revoked → `ErrTokenInvalid`; with the mock's `IsRevokedErr` set → still succeeds (fail-open, proven directly rather than assumed)
- `Refresh` with an unrevoked `jti` → unchanged from today (regression guard)
- `Logout` with a valid refresh token → mock's `Revoke` called with that exact `jti` and a `ttl` between 0 and `RefreshTokenTTL`
- `Logout` with an access token → `ErrTokenInvalid`, `Revoke` never called
- `Logout` with a garbage token → `ErrTokenInvalid`
- `Logout` with the mock's `RevokeErr` set → still returns `nil` (fail-open on the write path)
- `services/auth/go.mod` and `main.go` both compile with a real `REDIS_URL`; `go build ./...` succeeds

**Verification — the point of the task, mirroring Step 12's standard:** comment out the `IsRevoked` check in `Refresh` (leave `Logout` untouched), confirm the "revoked token still refreshes" test now fails, then restore. **A revocation check that can't be shown to matter is exactly the failure Step 12's pre-merge review found once already, in a different guard.**

**Checkpoint — stop for review.** Refresh actually rejects a revoked token; Logout actually revokes one; both proven, not assumed.

---

## Phase 3 — The endpoint

### Task 3 — `POST /auth/logout`

**Files:**
- `services/auth/internal/handler/auth.go` — `+ Logout`
- `services/auth/internal/handler/auth_test.go` — new test cases
- `services/auth/internal/handler/router.go` — `+ r.Post("/logout", auth.Logout)`, same unauthenticated group as `/refresh` and `/login`

**Handler shape**, deliberately identical to `Refresh`'s: `decodeJSON` into `service.RefreshTokenRequest` (no new request type — same `{"refresh_token": ...}` shape), the existing `req.RefreshToken == ""` → `400 invalid_request` check, then `h.service.Logout`; `ErrTokenInvalid` → `401 invalid_token`; success → `WriteJSON(w, http.StatusOK, struct{}{})`, which encodes as `{}` (`SPEC.md` §2.5 — this exact shape is what keeps `client.ts`'s generic `request<T>` helper from breaking on an empty body).

**Acceptance criteria:**
- `POST /auth/logout` with a valid refresh token → `200`, body `{}`
- Missing `refresh_token` → `400 invalid_request`
- Access token presented as `refresh_token` → `401 invalid_token`
- Garbage token → `401 invalid_token`
- End-to-end through the handler: register → capture refresh token → logout → `POST /auth/refresh` with the same token → `401` (this is the test that proves the whole feature works, not just its parts in isolation)

**Verify:**
```bash
cd services/auth && go test ./...
```

**Checkpoint — stop for review.** The endpoint exists, is reachable, and a logged-out token demonstrably cannot refresh.

---

## Phase 4 — Frontend

### Task 4 — `api.logout` and a real "sign out"

**Files:**
- `frontend/src/api/client.ts` — `+ api.logout(refreshToken: string)`
- `frontend/src/auth/AuthProvider.tsx` — `logout` becomes best-effort-revoke-then-clear

**`client.ts`**: `logout: (refreshToken: string) => request<Record<string, never>>('/auth/logout', { method: 'POST', body: { refresh_token: refreshToken }, authenticated: false })` — same shape as `login`/`register`/`refresh`'s calls, `authenticated: false` for the same reason `Refresh` is unauthenticated (§2.5: the access token may already be expired by the time someone signs out).

**`AuthProvider.tsx`**: capture `refreshToken.current` **before** calling `clearSession` (which nulls it), clear the session immediately so the UI reacts instantly regardless of network timing, then fire `api.logout` best-effort with errors swallowed — a failed revocation call must never block or visibly fail the sign-out button, matching the module's own existing docstring ("genuinely all it can do"). `AuthContextValue.logout`'s type stays `() => void`; no caller needs to await it.

**Acceptance criteria:**
- `npm run build` / `tsc` clean in `frontend/`
- No change to `AuthContextValue`'s shape — `LoginPage.tsx` and any other consumer need zero changes

**Verify (manual, real browser — `agents.md` treats UI changes as needing a run, not just a type-check):**
```bash
make docker-up && make run-auth && make run-gateway && make run-frontend
```
Log in, open the network tab, click sign out: confirm `POST /auth/logout` fires and returns `200`, confirm the UI returns to the login screen immediately (not gated on that response). Then, with the old refresh token copied from the network tab, `curl -X POST localhost:8080/auth/refresh -d '{"refresh_token":"<token>"}'` and confirm `401`.

**Checkpoint — stop for review.** Sign-out is real, verified against a running system, not just against tests.

---

## Phase 5 — Close-out

### Task 5 — Docs and backlog

**Files:** `.env.example`, `docs/security-backlog.md`, `docs/deferred-tuning.md`, `PHASE2_CHECKLIST.md`, `docs/NEXT_SESSION.md`

**Acceptance criteria:**
- `.env.example`'s existing `REDIS_URL` comment gets one line noting it's now also read by `services/auth` — no new variable (`SPEC.md` §5)
- `docs/security-backlog.md` item 2 marked **CLOSED**, in the same style item 1 already uses: what changed, and the two things worth correcting if anything in the original write-up turned out wrong in practice
- `docs/deferred-tuning.md` gains one entry: rotation-with-reuse-detection, rejected for now (§2.1), trigger = *the threat model needs theft detection, not just logout* — the same "decision + named trigger" convention Step 12 used for testcontainers and golang-migrate
- Step 13 written up in `PHASE2_CHECKLIST.md`, including the mutation-check result from Task 2
- `docs/NEXT_SESSION.md` rewritten per its own convention — with this closed, the trading engine becomes the sole next item

**Final verification pass:**
```bash
make test              # green, Docker down
make test-integration  # green, Docker up -- new Redis tests included
make vet                # tagged pass included
```

**Checkpoint — stop for review. Step 13 complete.**

---

## Risks

| Risk | Mitigation |
|---|---|
| A revocation check that compiles but never actually rejects anything (Step 12's pre-merge review found exactly this shape once, in a different guard) | Task 2's mutation check is an acceptance criterion, not optional |
| `NewService`'s signature change leaves a call site uncompiled | All 21 existing call sites are the identical literal string — a single find-and-replace, verified by `go build ./...` after |
| Redis unreachable in dev breaks `make run-auth` entirely | Boot-time `REDIS_URL` check fails loudly and immediately (§2.3 is about *request-time* errors after a successful boot, a different failure mode) |
| Fail-open silently masks a real outage | Both fail-open paths `log.Printf`; Task 2's tests assert the fail-open behavior directly rather than only checking the happy path |
| Frontend's sign-out button appears to hang on a slow/failed revocation call | `clearSession` runs before the network call, not after — Task 4 acceptance criteria pin the ordering |
| `main_test.go`'s new Redis skip category breaks the existing Postgres skip behavior | Independent variables (`skipReason`, `redisSkipReason`); Task 1's verification re-runs the Step 12 Postgres tests to confirm no regression |

## Out of scope

Refresh-token rotation; access-token revocation; "log out everywhere"; any other `docs/security-backlog.md` item; any gateway code change; extending Step 12's Postgres guard-chain pattern to Redis (§2.6 — the risk profile doesn't call for it).
