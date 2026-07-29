# Plan — QuantSim API Gateway (Phase 1, Step 7)

## Context

`SPEC.md` (approved 2026-07-29) defines the API Gateway: a reverse proxy fronting auth and market-data on one port, JWT enforcement via `pkg/auth`, CORS for the Vite dev server, and the loopback/timeout hardening that came out of deciding the spec's open questions with a security lens. Per the working agreement in `agents.md`, checkpoints are vertical slices sized to "one logical piece," reviewed before the next starts.

Lesson carried over from Steps 5 and 6: order checkpoints so the lowest-dependency, most-reviewable piece goes first, and give the risky/subtle piece its own isolated checkpoint rather than folding it into the thing that consumes it. Here the subtle piece is the middleware — CORS and identity-header handling are where this service's security properties actually live, so they get their own checkpoint with their own tests rather than arriving buried in a router diff.

## Decided defaults (from SPEC.md §2, restated for quick reference while implementing)

- **Proxy:** stdlib `net/http/httputil.ReverseProxy`, no path rewriting — SPEC.md §2.1, §2.2.
- **Ports:** gateway `8080`, auth `8081`, market-data `8082`; backend URLs from env with localhost defaults — SPEC.md §2.3.
- **Bind address:** all three services default to `127.0.0.1`, `BIND_ADDR` overrides — SPEC.md §2.4.
- **Identity headers:** `StripUserID` global/outermost; `InjectUserID` authenticated routes only — SPEC.md §2.5.
- **`/trading/*`:** `501 not_implemented`, mounted behind `RequireAuth` — SPEC.md §2.6.
- **CORS:** hand-written, exact-match `http://localhost:5173`, `Vary: Origin` always, no credentials, never `*` — SPEC.md §2.7.
- **Upstream failure:** `502` + `{"code":"upstream_unavailable",...}` via a custom `ErrorHandler` — SPEC.md §2.8.
- **Middleware order (load-bearing):** `StripUserID` → `CORS` → route group → `RequireAuth` → `InjectUserID` → proxy — SPEC.md §5.
- **Timeouts:** transport dial 5s / response-header 30s; gateway `http.Server{ReadHeaderTimeout: 10s}` — SPEC.md §2.11, §2.12.
- **`JWT_SECRET`:** fail-fast, minimum 32 bytes, checked via a shared `pkg/auth` helper — SPEC.md §2.12.
- **Deps:** `go-chi/chi/v5` only (plus local `pkg` via `replace`) — SPEC.md §5.

---

## Task 1 — Module setup + proxy package

**Files:** `services/gateway/go.mod`, `go.work`, `internal/handler/errors.go`, `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go`

- `go.mod`: `github.com/go-chi/chi/v5 v5.3.1` + `github.com/kpeguero/quantsim/pkg` with `replace => ../../pkg`, mirroring `services/auth/go.mod`.
- `go.work`: add `./services/gateway`.
- `errors.go`: copy of `services/market-data/internal/handler/errors.go` (SPEC.md §7 — deliberate third copy).
- `proxy.go`: `New(target *url.URL, transport http.RoundTripper, serviceName string) *httputil.ReverseProxy`. Wraps `NewSingleHostReverseProxy`, no path rewriting, custom `ErrorHandler` → `502` JSON. `ReverseProxy` already strips hop-by-hop headers per RFC 7230 — do not re-add them.

**Acceptance:** `go test ./...` passes in `services/gateway`. Tests cover: path/method/body/`Authorization` forwarded byte-identical to an `httptest` backend; closed port → `502` with the JSON error body and `Content-Type: application/json`.

**Depends on:** nothing.

---

## Task 2 — Middleware (the security-critical slice)

**Files:** `internal/middleware/cors.go`, `cors_test.go`, `identity.go`, `identity_test.go`

- `cors.go`: `CORS(allowedOrigin string) func(http.Handler) http.Handler`. Exact string match on `Origin`; sets `Allow-Origin` only on match; `Allow-Methods: GET, POST, OPTIONS`; `Allow-Headers: Authorization, Content-Type`; `Max-Age: 300`; **`Vary: Origin` unconditionally**. Preflight `OPTIONS` short-circuits `204` without calling the next handler. No `Allow-Credentials`, never `*`.
- `identity.go`: `StripUserID()` deletes any inbound `X-User-ID`; `InjectUserID()` sets it from `pkgauth.UserIDFromContext`, and deletes it if the context somehow has no user ID (fail closed, never pass through).

**Acceptance:** `go test ./...` passes. Required cases:
- Preflight with matching `Origin` → `204`, all headers present, wrapped handler **not** called.
- Simple `GET` with matching `Origin` → headers present, handler called.
- Non-matching `Origin` → no `Allow-Origin` echoed, but `Vary: Origin` still set.
- **Spoofing regression (the one that matters):** a request with `X-User-ID: victim` and no token must not deliver that header downstream.
- `InjectUserID` with a valid user ID on context → header equals the subject.

**Depends on:** Task 1 (module must exist).

---

## Task 3 — Router, wiring, and the manual E2E

**Files:** `internal/handler/router.go`, `router_test.go`, `cmd/server/main.go`, `Makefile`, `.env.example`

- `router.go`: chi. `StripUserID` → `CORS` → routes. `/healthz` local `200 ok`, unauthenticated, no backend touched. `/auth/*` public proxy. `/market-data/*` and `/trading/*` in a group under `RequireAuth` + `InjectUserID`; `/trading/*` → `501 not_implemented`.
- `main.go`: `JWT_SECRET` fail-fast + length check; `AUTH_SERVICE_URL` / `MARKET_DATA_SERVICE_URL` defaulted; `PORT` default `8080`; shared `http.Transport`; `http.Server{ReadHeaderTimeout: 10s}`.
- `Makefile`: `run-gateway` → `go run ./cmd/server`.
- `.env.example`: commented "Service URLs (gateway)" block + `BIND_ADDR`.

**Acceptance:** `go test ./...` passes (full stack against `httptest` backends, real tokens from `pkgauth.GenerateToken`), **and** the SPEC.md §3 curl sequence runs clean against all three services live.

**Depends on:** Tasks 1 and 2.

---

## Task 4 — Loopback binding + secret guard

**Files:** `pkg/auth/jwt.go` (or a new small file), `services/auth/cmd/server/main.go`, `services/market-data/cmd/server/main.go`, gateway `main.go`

- Exported minimum-secret-length check in `pkg/auth`; adopted by auth and gateway where they read `JWT_SECRET`.
- `BIND_ADDR` env (default `127.0.0.1`) in all three services; listen on `BIND_ADDR + ":" + PORT`; startup log prints the real bind address.

**Acceptance:** `lsof -nP -iTCP:8081 -sTCP:LISTEN` and `-iTCP:8082` show `127.0.0.1:<port>`, not `*:<port>`, for all three services. `go test ./...` still passes in `pkg`, `services/auth`, `services/market-data`, `services/gateway`.

**Note:** if the current `.env` `JWT_SECRET` is under 32 characters, auth and gateway will refuse to start until it's rotated — intended, per SPEC.md §2.12.

**Depends on:** Task 3 (do the gateway's own binding in the same pass).

---

## Task 5 — Close out the step

- Check off Step 7 in `PHASE1_CHECKLIST.md`.
- Archive `SPEC.md`, `tasks/plan.md`, `tasks/todo.md` to `docs/archive/phase1-step7-gateway/`, per the Steps 4–6 convention.

**Depends on:** Task 4.

---

## Dependency graph

```
Task 1 (proxy) ──┬─→ Task 2 (middleware) ──┐
                 │                          ├─→ Task 3 (router + wiring) ─→ Task 4 (hardening) ─→ Task 5 (close out)
                 └──────────────────────────┘
```

Tasks 1 and 2 are independent of each other past module setup, but the review-one-at-a-time rule means they still land in order.
