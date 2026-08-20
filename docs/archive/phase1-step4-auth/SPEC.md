# SPEC — QuantSim Auth Service (Phase 1, Step 4)

Status: **Draft — awaiting architect review**
Scope: finish the Auth Service HTTP layer only. Not a whole-project spec — see `agents.md` for that context.

---

## 1. Objective

`PHASE1_CHECKLIST.md` Step 4 is partially done: the store layer (`PostgresUserStore`, `PostgresAccountStore`), interfaces (`UserStore`, `AccountStore`), and request/response types already exist and are committed. What's missing is everything above the store layer:

Build a working Auth Service that exposes four HTTP endpoints backed by JWT auth, so that:
- A user can register (creates a `users` row + an `accounts` row with a $100k starting balance) and receive tokens
- A user can log in and receive tokens
- A user can refresh an expired access token
- A user can fetch their own profile via a protected endpoint

This unblocks the gateway (Step 7) and frontend (Step 8), which both depend on a working `/auth/*` API.

**Out of scope for this spec:** market-data service, trading engine, gateway routing, frontend, cloud deployment. Those are separate specs later.

---

## 2. Decision — refresh token storage: stateless (confirmed)

Refresh tokens are JWTs signed with the same secret as access tokens, just longer-lived (7 days) and carrying a `type: "refresh"` claim. No DB table, no revocation list — Phase 1 has no logout/revocation requirement. This can be swapped for DB-backed revocation later as a Phase 2+ hardening pass without changing the `TokenPair` API shape.

---

## 3. Commands

| Command | Purpose |
|---|---|
| `make docker-up` / `make docker-down` | Start/stop Postgres + Redis |
| `make migrate-up` / `make migrate-down` | Apply/rollback migrations |
| `cd services/auth && go run ./cmd/server` (or `make run-auth` once wired) | Run the auth service locally |
| `cd services/auth && go test ./...` | Run unit/integration tests |
| `cd services/auth && go mod tidy` | Sync deps after adding imports |

Manual verification (until integration tests cover it):
```
curl -X POST localhost:8081/auth/register -d '{"email":"a@b.com","username":"a","password":"pw12345"}'
curl -X POST localhost:8081/auth/login -d '{"email":"a@b.com","password":"pw12345"}'
curl -X POST localhost:8081/auth/refresh -d '{"refresh_token":"<token>"}'
curl localhost:8081/auth/me -H "Authorization: Bearer <access_token>"
```
(Port TBD when `main.go` is wired — will confirm in that checkpoint.)

---

## 4. Project structure

Existing (committed, unchanged by this spec):
```
services/auth/
  internal/service/types.go        # RegisterRequest, LoginRequest, TokenPair, MeResponse, User
  internal/service/interfaces.go   # UserStore, AccountStore
  internal/store/user_store.go     # PostgresUserStore (CreateUser, GetUserByEmail)
  internal/store/account_store.go  # PostgresAccountStore (CreateAccount)
```

New, to be created by this spec (checkpoint order below):
```
services/auth/
  internal/store/user_store.go     # + GetUserByID (extends existing file)
  internal/service/interfaces.go   # + GetUserByID on UserStore (extends existing file)
  internal/service/jwt.go          # generate/validate access + refresh tokens
  internal/service/auth.go         # Register, Login, Refresh, Me (business logic)
  internal/service/errors.go       # sentinel errors (ErrInvalidCredentials, etc.) mapped to HTTP status in handlers
  internal/handler/auth.go         # HTTP handlers: parse, call service, write JSON
  internal/handler/errors.go       # ErrorResponse{Code, Message} + JSON error writer
  cmd/server/main.go               # load env, build pool/stores/service/handlers, mount chi router, listen
pkg/auth/
  middleware.go                    # Bearer token validation middleware (used by /auth/me now, gateway later)
services/auth/internal/service/
  auth_test.go                     # unit tests, mocked UserStore/AccountStore
services/auth/internal/handler/
  auth_test.go                     # handler tests (httptest), or integration test against real Postgres — TBD at that checkpoint
```

---

## 5. Code style / conventions

- **Layering:** handler → service → store, one direction. Handlers stay thin (parse request, call service, write response); business logic lives in service; service depends only on the `UserStore`/`AccountStore` interfaces, never on `*PostgresUserStore` directly.
- **Errors:** service layer returns sentinel errors (`ErrInvalidCredentials`, `ErrDuplicateUser` — already exists in `store`, `ErrTokenInvalid`, etc.); handlers map these to HTTP status + `{"code": "...", "message": "..."}` JSON, per `agents.md`'s standing rule.
- **Password hashing:** bcrypt, cost 10–12 (`golang.org/x/crypto/bcrypt`).
- **JWT:** HS256, secret from `JWT_SECRET` env var. Access token TTL ~15min, refresh ~7 days (per checklist).
- **Router:** `go-chi/chi/v5`, routes mounted under `/auth` via `r.Route("/auth", ...)`.
- **New dependencies beyond chi / golang-jwt / bcrypt require your sign-off first** (per your ask-first boundary) — I don't expect to need any for this spec.

---

## 6. Testing strategy

Per your call to use automated tests as the review safety net (since you're reviewing at checkpoints, not reading every line):

- **Service layer:** table-driven unit tests in `internal/service/auth_test.go`, using hand-written mocks of `UserStore`/`AccountStore` (interfaces already support this — no mocking library needed). Cover: successful register/login/refresh/me, duplicate email on register, wrong password on login, expired/invalid token on refresh and me.
- **Handlers:** `httptest`-based tests hitting the chi router directly, asserting status codes and JSON bodies for the same cases above.
- **Not in scope for this spec:** end-to-end tests against a real Postgres instance (that's what the curl checklist step is for); load/concurrency testing.
- `go test ./...` should pass before any checkpoint is marked done.

---

## 7. Boundaries

**Always do:**
- Follow the checkpoint order in §8 — stop after each one for review, don't chain into the next without a go-ahead
- Keep handler/service/store layering; service depends on interfaces only
- Use the JSON error shape for every 4xx/5xx response
- Run `go test ./...` before flagging a checkpoint as done

**Ask first:**
- Any dependency beyond `go-chi/chi/v5`, `golang-jwt/jwt/v5`, `golang.org/x/crypto/bcrypt`
- Any DB schema or migration change (this spec shouldn't need one — `GetUserByID` uses the existing `users` table)
- Switching refresh tokens off stateless (§2) to DB-backed, if that ever comes up later

**Never do:**
- Commit `.env` or real secrets
- Log passwords or tokens in plaintext
- Bypass the JSON error shape or the interface-based store dependency

---

## 8. Checkpoint plan (build order)

Each is a stop-and-review point, sized to "one logical piece" per your earlier guidance.

1. **`GetUserByID`** — add to `UserStore` interface + implement in `PostgresUserStore`. Needed by `/auth/me`.
2. **JWT helpers** (`internal/service/jwt.go`) — generate access/refresh (stateless, per §2), validate + parse claims.
3. **Service layer** (`internal/service/auth.go` + `errors.go`) — `Register`, `Login`, `Refresh`, `Me`, using bcrypt + JWT helpers + stores.
4. **Handlers** (`internal/handler/auth.go` + `errors.go`) — one handler per endpoint, JSON error shape.
5. **Router + `main.go`** — wire DB pool, stores, service, handlers; mount `/auth` routes; start server.
6. **`pkg/auth` middleware** — Bearer token validation, protects `GET /auth/me`.
7. **Tests** — service-layer unit tests + handler tests (interleaved with 3–4 rather than saved entirely for the end, so each checkpoint ships with its own coverage).

---

## Confirm before I start

- [x] Refresh token strategy (§2): stateless — confirmed
- [ ] Checkpoint plan (§8) — right granularity, right order?
- [ ] Anything in scope/out-of-scope (§1) you'd change?
