# Plan — QuantSim Auth Service (Phase 1, Step 4)

## Context

`SPEC.md` (approved, except its checkpoint plan) defines the remaining work to finish the Auth Service: four HTTP endpoints (`register`, `login`, `refresh`, `me`) on top of the already-implemented store layer. SPEC.md's checkpoint plan (§8) was **horizontal** — one checkpoint per code layer (all JWT helpers, then all service methods, then all handlers). Per the architect/implementer working agreement in `agents.md`, checkpoints should instead be **vertical**: each one a complete, curl-testable endpoint the architect can review and demo, not an isolated layer with nothing calling it yet.

A Plan agent validated this restructuring and found one real blocker: `pkg/auth/middleware.go` (meant to be reused by the gateway later) cannot import `services/auth/internal/service` — Go's `internal/` visibility rules forbid it. This was decided: **`pkg/auth` owns the actual JWT primitives** (`Claims`, `GenerateToken`, `ValidateToken`) as the single source of truth; the auth service imports `pkg/auth` and layers business rules (TTLs, refresh-type claim, token-pair assembly) on top. This also directly serves the stated goal of `pkg/auth` being reusable by the gateway later — the gateway will validate tokens issued by this exact code path.

This plan supersedes SPEC.md §4 (project structure) and §8 (checkpoint plan) with the vertical-slice version below. Everything else in SPEC.md (objective, stateless refresh tokens, code style, testing bar, ask-first boundaries) stands unchanged.

## Module wiring (do first, before Task 1)

`pkg` and `services/auth` are separate Go modules with no workspace today. Add a `go.work` at the repo root (`go work init ./pkg ./services/auth`) so `services/auth` resolves `github.com/kpeguero/quantsim/pkg` locally without a `replace` directive per consuming service — this is the standard modern pattern for a multi-module monorepo and stays out of each service's own `go.mod`/`go.sum`, so it doesn't affect how each service builds independently outside the workspace.

## Decided defaults (not asked as separate questions — flagged here for override at checkpoint review)

- **Port:** `PORT` env var, default `8081` if unset.
- **Required env at boot:** `DATABASE_URL`, `JWT_SECRET` — `main.go` fails fast with a clear log line if either is empty.
- **bcrypt cost:** 10 (named constant), not 12 — keeps the ~15-20 password-hashing test cases fast; trivial to bump later.
- **Login enumeration protection:** unknown-email and wrong-password return the *identical* 401 body; unknown-email path still runs `bcrypt.CompareHashAndPassword` against a fixed dummy hash so response timing doesn't leak whether the email exists.
- **Token errors:** single `ErrTokenInvalid` sentinel for expired/malformed/bad-signature — one 401 shape, no vocabulary that helps an attacker distinguish failure modes.
- **Token-type confusion:** an access token must be rejected at `/auth/refresh`; a refresh token must be rejected at `/auth/me`. Explicit test case in both directions.
- **Vanished user:** valid token, but the user row is gone → 401 (not 404), same shape as other auth failures.
- **JWT clock skew:** zero leeway (library default) — single server, Phase 1.
- **`Me` service signature:** `Me(ctx, userID uuid.UUID)`, not `Me(ctx, tokenString)`. The `pkg/auth` middleware is the sole JWT gatekeeper for `/auth/me`; it validates the Bearer token and puts `userID` on the request context. The service layer does no JWT parsing — plain store lookup + map to `MeResponse`. (Minor deviation from SPEC.md's literal wording, no external behavior change.)

## Fix needed before Task 1's verification works

`Makefile`'s `run-auth` target runs `cd services/auth && go run .`, but the entrypoint is `cmd/server/main.go`, not the module root. Fix to `cd services/auth && go run ./cmd/server` — otherwise Task 1's first curl check fails for a reason unrelated to the code being reviewed.

## Dependency graph

```
Task 1 (Register) — root, stands up all shared skeleton:
  main.go (pgx pool, fail-fast env, chi router), handler/errors.go (JSON error helper),
  pkg/auth/jwt.go (Claims, GenerateToken), bcrypt hashing
        │
        ├──► Task 2 (Login) — reuses skeleton + GenerateToken only. No dependency on Task 3.
        │
        └──► Task 3 (Refresh) — reuses skeleton + GenerateToken; adds pkg/auth.ValidateToken.
                    │
                    └──► Task 4 (Me) — reuses Task 3's ValidateToken inside pkg/auth/middleware.go
                                        (new); adds GetUserByID to UserStore + PostgresUserStore.
```

Tasks 2 and 3 only hard-depend on Task 1 — their relative order is a narrative choice (mirrors the real register→login→refresh→me journey), not a compile-time constraint. Task 4 is the only task with a genuine hard dependency on another task's code (Task 3's `ValidateToken`).

## Reuse — existing code this plan builds on, does not duplicate

- `services/auth/internal/service/types.go` — `User`, `RegisterRequest`, `LoginRequest`, `TokenPair`, `RefreshTokenRequest`, `MeResponse` (unchanged)
- `services/auth/internal/service/interfaces.go` — `UserStore`, `AccountStore` (extended in Task 4 with `GetUserByID`, not replaced)
- `services/auth/internal/store/user_store.go` — `PostgresUserStore.CreateUser` / `GetUserByEmail`, `store.ErrDuplicateUser` (reused as-is; `GetUserByID` added alongside in Task 4)
- `services/auth/internal/store/account_store.go` — `PostgresAccountStore.CreateAccount` (reused as-is)
- `docs/TESTING_STRUCTURE.md` conventions — hand-written map-backed mocks (no mocking library), tests co-located as `*_test.go`

## Tasks

### Task 1 — Skeleton + `POST /auth/register` end-to-end

**New/modified files:**
- `services/auth/cmd/server/main.go` — env loading (fail-fast), pgx pool, wires stores → service → handlers, `http.ListenAndServe`
- `services/auth/internal/handler/router.go` — chi router; mounts `/healthz` and `/auth/register`
- `services/auth/internal/handler/errors.go` — `ErrorResponse{Code,Message}` + `WriteError` helper
- `services/auth/internal/handler/auth.go` — `RegisterHandler`
- `pkg/auth/jwt.go` — `Claims`, `GenerateToken(userID, tokenType, ttl)`
- `services/auth/internal/service/auth.go` — `Service` struct, `NewService(...)`, `Register`
- `services/auth/internal/service/auth_test.go` — success, duplicate email (409)
- `services/auth/internal/handler/auth_test.go` — 201 success, 409 duplicate, 400 malformed JSON
- `go.work` (repo root), `Makefile` (`run-auth` fix)

**Acceptance criteria:**
- `go build ./...` and `go vet ./...` clean in `services/auth` and `pkg`
- Server refuses to start without `DATABASE_URL`/`JWT_SECRET`
- Register creates a `users` row + an `accounts` row with `balance = 100000.0000`, returns 201 with `{access_token, refresh_token, expires_in: 900}`
- Duplicate email/username → 409 `{"code":"duplicate_user",...}`
- Malformed JSON → 400
- Stored password is a bcrypt hash (cost 10), never plaintext
- `pkg/auth/jwt.go` has zero imports from `services/auth/*`
- `go test ./...` passes (mocked stores, no live DB required)

**Verification:**
```
cd services/auth && go test ./...
make docker-up && make migrate-up
JWT_SECRET=devsecret DATABASE_URL=<...> PORT=8081 make run-auth

curl -i localhost:8081/healthz                                      # 200
curl -i -X POST localhost:8081/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"a@b.com","username":"alice","password":"pw12345678"}'  # 201 + tokens
curl -i -X POST localhost:8081/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"a@b.com","username":"alice","password":"pw12345678"}'  # 409 duplicate
curl -i -X POST localhost:8081/auth/register -d 'not-json'          # 400
```

---

### Task 2 — `POST /auth/login`

**New/modified files:**
- `services/auth/internal/service/errors.go` (new) — `ErrInvalidCredentials`
- `services/auth/internal/service/auth.go` (+`Login`)
- `services/auth/internal/service/auth_test.go` (+ success, wrong password, unknown email)
- `services/auth/internal/handler/auth.go` (+`LoginHandler`), `router.go` (+ mount)
- `services/auth/internal/handler/auth_test.go` (+ cases)

**Acceptance criteria:**
- Correct credentials → 200, `TokenPair`
- Wrong password → 401 `invalid_credentials`
- Unknown email → 401, **identical** body to wrong-password
- Malformed JSON → 400
- `go test ./...` passes, covers all three failure modes

**Verification:**
```
cd services/auth && go test ./...
curl -i -X POST localhost:8081/auth/login -d '{"email":"a@b.com","password":"pw12345678"}'  # 200
curl -i -X POST localhost:8081/auth/login -d '{"email":"a@b.com","password":"wrongpw"}'     # 401
curl -i -X POST localhost:8081/auth/login -d '{"email":"nouser@x.com","password":"x"}'      # 401, same body
```

---

### Task 3 — `POST /auth/refresh`

**New/modified files:**
- `pkg/auth/jwt.go` (+`ValidateToken(token) (*Claims, error)`)
- `services/auth/internal/service/errors.go` (+`ErrTokenInvalid`)
- `services/auth/internal/service/auth.go` (+`Refresh`)
- `services/auth/internal/service/auth_test.go` (+ success, expired token, garbage token, access-token-as-refresh)
- `services/auth/internal/handler/auth.go` (+`RefreshHandler`), `router.go` (+ mount)
- `services/auth/internal/handler/auth_test.go` (+ cases)

**Acceptance criteria:**
- Valid unexpired refresh token → 200, brand-new `TokenPair` (old refresh token remains valid until natural expiry — no revocation, per SPEC.md §2, expected not a bug)
- Expired refresh token → 401
- Malformed/garbage token → 401
- Access token submitted as refresh → 401 (explicit test)
- `go test ./...` passes; expired/garbage tokens fabricated directly in the test file via `golang-jwt`, no production test-only API added

**Verification:**
```
cd services/auth && go test ./...
curl -i -X POST localhost:8081/auth/refresh -d '{"refresh_token":"<refresh>"}'      # 200 + new pair
curl -i -X POST localhost:8081/auth/refresh -d '{"refresh_token":"<access_token>"}' # 401 type confusion
curl -i -X POST localhost:8081/auth/refresh -d '{"refresh_token":"garbage"}'        # 401
```

---

### Task 4 — `GET /auth/me` (protected) + `pkg/auth` middleware

**New/modified files:**
- `services/auth/internal/service/interfaces.go` (+`GetUserByID` on `UserStore`)
- `services/auth/internal/store/user_store.go` (+`GetUserByID` impl)
- `services/auth/internal/service/errors.go` (+`ErrUserNotFound` → maps to 401)
- `services/auth/internal/service/auth.go` (+`Me(ctx, userID uuid.UUID)`, no JWT logic)
- `services/auth/internal/service/auth_test.go` (+ success, user-not-found-after-token-issued)
- `pkg/auth/middleware.go` (new) — `RequireAuth` chi middleware: Bearer parse, `ValidateToken`, rejects non-`access` type, injects `userID` into context
- `pkg/auth/middleware_test.go` (new) — valid token, missing header, malformed header, expired token, refresh-as-access
- `services/auth/internal/handler/auth.go` (+`MeHandler`), `router.go` (+ mount `GET /auth/me` behind `RequireAuth`)
- `services/auth/internal/handler/auth_test.go` (+ cases through the full router+middleware stack)

**Acceptance criteria:**
- Valid access token → 200, `MeResponse` matching the registered user
- No `Authorization` header → 401
- Malformed header (no `Bearer ` prefix) → 401
- Expired/bad-signature access token → 401
- Refresh token used at `/auth/me` → 401 (type confusion)
- `pkg/auth/middleware.go` has zero imports from `services/auth/*`
- `go test ./...` passes in both `services/auth` and `pkg` modules (real-Postgres integration test for `GetUserByID` not required — mock-store unit test is sufficient per `docs/TESTING_STRUCTURE.md`, Phase 1)

**Verification:**
```
cd pkg && go test ./...
cd services/auth && go test ./...
curl -i localhost:8081/auth/me -H "Authorization: Bearer <access_token>"    # 200 + profile
curl -i localhost:8081/auth/me                                             # 401 no header
curl -i localhost:8081/auth/me -H "Authorization: Bearer <refresh_token>"  # 401 type confusion
curl -i localhost:8081/auth/me -H "Authorization: Bearer garbage"          # 401
```

---

## What changed vs. SPEC.md

- §8's 7 horizontal checkpoints → 4 vertical ones (Register, Login, Refresh, Me), each independently curl-testable and shipping its own tests in the same task (no separate final "testing" checkpoint).
- §4's file layout gains `go.work`, `handler/router.go`, `handler/errors.go`, `service/errors.go` as explicit files (implied but not enumerated in SPEC.md).
- One structural fix to §4: `pkg/auth` owns JWT primitives, not `services/auth/internal/service` — required for `pkg/auth` to be importable at all given Go's `internal/` visibility rules.
- SPEC.md's objective, stateless-refresh-token decision, code style, testing bar, and ask-first boundaries are unchanged.

## Post-implementation amendment (2026-07-29, pre-push review)

A code review before pushing surfaced three correctness gaps not caught by the checkpoint plan above:

1. **`Register` wasn't atomic.** `CreateUser` and `CreateAccount` were two independent store calls; a failure between them could leave a user with no funded account, with no recovery path (email already taken). Fixed by merging them into `UserStore.CreateUserWithAccount`, a single Postgres transaction. `AccountStore`/`PostgresAccountStore` (§4's file layout above) no longer exist — `services/auth/internal/store/account_store.go` was deleted, its logic absorbed into `user_store.go`.
2. **`Login`/`Me` folded every store error into an auth failure.** A DB outage during `GetUserByEmail`/`GetUserByID` would surface as "invalid credentials" / "invalid token" instead of a 500, masking real infra failures as user-facing auth errors. Fixed by having the store return the sentinel `service.ErrUserNotFound` only for an actual missing row (mapped from `pgx.ErrNoRows`); any other error now propagates to a 500 as it already did in `Register`.
3. **`Refresh` never checked the user still exists.** Unlike `Me`, a deleted user's refresh token could keep minting valid access tokens until natural (7-day) expiry. Fixed by adding the same existence check `Me` already had.

All four tasks' tests were updated (`mock.UserStore` gained `CreateAccountErr`/`GetByEmailErr`/`GetByIDErr` injection points) and four new regression tests added; all verified against real Postgres via curl, including deleting a user mid-session and confirming their refresh token is rejected.

## Status

Approved 2026-07-29. See `tasks/todo.md` for live checkpoint status.
