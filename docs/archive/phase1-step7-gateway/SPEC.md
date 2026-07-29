# SPEC — QuantSim API Gateway (Phase 1, Step 7)

Status: **Approved 2026-07-29** — open decisions in §9 delegated to the implementer with the instruction to decide with a security mindset; §2.4, §2.5, and §2.7 below reflect the resulting calls (two of which reverse the original draft).
Scope: the API Gateway service — reverse proxy, JWT enforcement, CORS. Not a whole-project spec — see `agents.md` and `docs/intent/quantsim-resume.md` for that context. Prior specs archived at `docs/archive/phase1-step4-auth/` (Auth Service), `docs/archive/phase1-step5-market-data/` (historical ingestion), `docs/archive/phase1-step6-market-data-live/` (live polling + Redis) — all complete.

---

## 1. Objective

Per `PHASE1_CHECKLIST.md` Step 7, build `services/gateway/` (currently an empty `go.mod`) so that:

- The frontend talks to **one** origin (`localhost:8080`) instead of knowing that auth lives on `:8081` and market data on `:8082`
- `/auth/*` proxies to the auth service and stays **public** (you can't present a token before you have one)
- `/market-data/*` proxies to the market-data service and is **JWT-protected** — the gateway validates the access token before the request ever reaches a backend
- `/trading/*` is wired and protected now, as a placeholder for the Phase 2 trading engine
- A browser at `http://localhost:5173` (Vite dev server) can call all of it without CORS errors

This is the last backend piece of Phase 1. It directly unblocks Step 8: the frontend's entire API surface becomes one base URL, and the "register → login → dashboard with prices → chart" E2E in the Phase 1 handoff criteria runs through this service.

**Out of scope for this spec:** the trading engine itself (Phase 2), WebSocket fan-out from the `prices:{symbol}` pub/sub channels (later phase — the gateway will host it, but not in this step), rate limiting, request logging/metrics, service discovery, TLS, per-route authorization beyond "authenticated or not."

---

## 2. Decisions

### 2.1 Reverse proxy: stdlib `net/http/httputil.ReverseProxy`, no new dependency

Go's standard library already does exactly this job — `httputil.NewSingleHostReverseProxy` plus a custom `ErrorHandler` (§2.8) covers every requirement in the checklist. Only `go-chi/chi/v5` gets added to `services/gateway/go.mod` (plus the local `pkg` module for JWT). No proxy library, no service mesh, nothing.

### 2.2 No path rewriting — the gateway forwards the URL path unchanged

The auth service already serves `/auth/register`; the market-data service already serves `/market-data/prices/{symbol}`. The gateway routes on the same prefix it forwards, so `POST localhost:8080/auth/register` → `POST localhost:8081/auth/register`, byte-identical path.

This is a deliberate choice against the common "strip the prefix at the edge" pattern (`/market-data/prices/AAPL` → `/prices/AAPL`). Keeping paths identical means a backend service behaves the same whether you curl it directly on `:8082` or through the gateway on `:8080` — which is exactly what makes the existing Step 5/6 manual verification commands still valid and debugging one layer at a time possible. The cost is that each service "owns" its prefix in its own router; that's already true today.

### 2.3 Downstream URLs from env, with working localhost defaults (not fail-fast)

`AUTH_SERVICE_URL` (default `http://localhost:8081`), `MARKET_DATA_SERVICE_URL` (default `http://localhost:8082`), `TRADING_SERVICE_URL` (no default — see §2.6). `PORT` defaults to `8080`.

Auth and market-data fail-fast on their required env because those are secrets or infra addresses with no sane default. Service URLs in a local-dev monorepo *do* have a sane default, and defaulting them means `make run-gateway` works with no extra setup. `JWT_SECRET` stays fail-fast — it's a secret, and a gateway that silently started with an empty signing key would accept forged tokens.

All three URL vars get added to `.env.example` as documentation, commented, with the localhost values.

### 2.4 The gateway is the only place JWT is enforced — so every backend binds to loopback

The market-data service has no auth of its own and this spec doesn't add any. Anyone who can reach `:8082` directly bypasses the gateway entirely.

The original draft accepted that on the grounds of "it's all localhost anyway, revisit in Phase 4." **That premise was false.** `services/auth/cmd/server/main.go:43` and `services/market-data/cmd/server/main.go:66` both call `http.ListenAndServe(":"+port, ...)`, and a bare `:port` binds `0.0.0.0` — every interface. On any shared network (campus wifi, a coffee shop), `:8082` is today a fully open market-data API and `:8081` is an open user-registration endpoint. That is a present exposure, not a Phase 4 one.

So this spec makes the premise true instead of deferring it: all three services default their listen address to `127.0.0.1`, with a `BIND_ADDR` env override for the day they're containerized and need `0.0.0.0` behind a real network boundary. Two lines per service. Only the gateway is meant to be reachable, and after this change only the gateway *is* — which is what makes "auth is enforced at the gateway" an actual boundary rather than a convention.

Service-to-service authentication (mTLS, signed internal tokens) remains a Phase 4 concern. Loopback binding is the correct Phase 1 control: it costs nothing and closes the hole that exists right now.

### 2.5 Identity forwarding: strip inbound `X-User-ID`, inject the validated one

`pkg/auth.RequireAuth` puts the token's subject on the Go request *context* — which vanishes at the process boundary. A proxied backend can't read it. So the gateway:

1. **Deletes any client-supplied `X-User-ID` header on every route**, as the outermost middleware — before routing, before CORS, before auth.
2. Sets `X-User-ID` from the validated JWT claims, on authenticated routes only, after `RequireAuth` passes.
3. Leaves the `Authorization` header intact on the forwarded request, so a backend that wants to re-validate independently can.

**The strip is global, and that's the correction that matters.** The draft scoped it to the authenticated group, which leaves a hole: `/auth/*` is public *and proxied*, so the day the auth service reads `X-User-ID` for anything at all, an unauthenticated caller sets it to whatever they like. A header the system treats as trusted identity must be scrubbed at the very edge, unconditionally — not at the point where it happens to be consumed. Injection stays narrow; stripping goes wide.

Nothing consumes `X-User-ID` yet — market-data is user-agnostic. It's here because the Phase 2 trading engine fundamentally needs "who is placing this order," and adding it now costs ~5 lines and one test, while retrofitting later means touching the gateway *and* whatever assumed it wasn't there.

### 2.6 `/trading/*` returns `501 Not Implemented`, it does not proxy into the void

There is no trading service — `services/trading-engine/` is an empty placeholder. Two options: point the proxy at a URL where nothing is listening (every request becomes a confusing connection-refused `502`), or have the gateway answer `501` in the JSON error shape.

This spec picks `501`, with the route still mounted **behind the JWT middleware** so the auth wiring is real and testable today. Swapping in a real proxy in Phase 2 is a one-line change in the router. `TRADING_SERVICE_URL` is not read in this step.

### 2.7 CORS: hand-written middleware, one allowed origin, no credentials

The checklist says "configure CORS for `localhost:5173`." `github.com/go-chi/cors` would do it, but that's a new dependency for a ~35-line problem with exactly one allowed origin, and `agents.md` requires sign-off on new deps.

"Don't hand-roll security code" is a good default, but it earns its keep against *cryptographic* primitives, where subtle errors are invisible. CORS is a handful of response headers, and the four ways it goes wrong are well-known and enumerable:

| Failure mode | How this implementation avoids it |
|---|---|
| Reflecting an attacker-supplied `Origin` | Exact string equality against one compile-time constant; the request's `Origin` is compared, never echoed unvalidated |
| `*` combined with `Allow-Credentials` | Neither is ever sent |
| Missing `Vary: Origin` → a shared cache serves one origin's response to another | `Vary: Origin` set unconditionally, on every response, match or not |
| Accepting the literal `null` origin (sandboxed iframes, `file://`) | Falls out of exact-match — `null` simply isn't the constant |

Thirty-five lines a reviewer confirms in one screen beats a config surface that has to be trusted. Concretely:

- `Access-Control-Allow-Origin: http://localhost:5173` — echoed only when the request's `Origin` matches exactly. Never `*`.
- `Access-Control-Allow-Methods: GET, POST, OPTIONS`
- `Access-Control-Allow-Headers: Authorization, Content-Type`
- `Access-Control-Max-Age: 300` (cuts preflight chatter in dev)
- `Vary: Origin` — **always**, including on non-matching origins and non-CORS requests.
- Preflight `OPTIONS` is answered `204` by the middleware and **never proxied** downstream.
- **No** `Access-Control-Allow-Credentials`. QuantSim's JWT rides in the `Authorization` header, not a cookie, so credentialed CORS is unnecessary — and it's the setting that turns a lax origin check into an account-takeover primitive.

The allowed origin is a constant, not env-configurable, for the same reason the poll interval was (Step 6 §2.3) — Phase 1 doesn't need the knob. If deployment needs it, it becomes `CORS_ALLOWED_ORIGIN` later.

**Ordering matters:** CORS middleware runs *before* `RequireAuth`. A `401` from an unauthenticated XHR still needs CORS headers, or the browser reports an opaque network error instead of the real 401 and you debug the wrong thing.

### 2.8 Upstream failure → `502` in the standard JSON error shape

`httputil.ReverseProxy`'s default `ErrorHandler` writes a bare `502` with an empty body and logs to stderr — which violates `agents.md`'s standing "consistent JSON error response shape" rule and would give the frontend nothing to display. The gateway sets a custom `ErrorHandler` returning:

```json
{"code": "upstream_unavailable", "message": "market-data service is unavailable"}
```

with `502`. This is the error the frontend will actually hit most often in dev (forgot to start a backend), so it's worth making legible.

### 2.9 Gateway `/healthz` is shallow — it does not check downstreams

`GET /healthz` returns `200 ok` if the gateway process is up, matching auth's and market-data's existing `/healthz`. No fan-out health aggregation. A gateway that reports unhealthy because a backend is down conflates two different failures, and nothing in Phase 1 consumes health checks anyway. `/healthz` is not proxied and not authenticated.

### 2.10 Gateway follows the `cmd/server` + `internal/` layout; `make run-gateway` gets fixed

The Makefile currently has `run-gateway: cd services/gateway && go run .` — written before any layout existed. Auth and market-data both use `cmd/server/main.go` with logic in `internal/`, per `docs/TESTING_STRUCTURE.md`. The gateway matches them, and the Makefile target changes to `go run ./cmd/server`. Small, but three services with two conventions is the kind of inconsistency that costs more later than it saves now.

### 2.11 Proxy transport: explicit timeouts, shared transport

One `http.Transport` shared by both proxies (connection pooling is the whole point), with `ResponseHeaderTimeout: 30s` and `DialContext` timeout `5s`. Without a response-header timeout, a hung backend holds a gateway goroutine and a client connection indefinitely. No overall request timeout — that would break any future streaming/SSE response, and 30s-to-first-header is the meaningful bound here.

### 2.12 Gateway server: `ReadHeaderTimeout`, and a minimum `JWT_SECRET` length

Two edge-specific hardening items the draft missed:

- **`http.Server{ReadHeaderTimeout: 10s}` instead of bare `http.ListenAndServe`.** The gateway is the only process meant to accept outside connections (§2.4), and `ReadHeaderTimeout` is the specific mitigation for Slowloris — a client that opens connections and dribbles header bytes forever to exhaust the accept pool. Auth and market-data keep their plain `ListenAndServe`; behind loopback they aren't reachable by an attacker who could mount it.
- **Reject `JWT_SECRET` shorter than 32 bytes at startup**, via a small exported helper in `pkg/auth` used by both the gateway and the auth service. HS256's security reduces entirely to secret entropy, and a short one is offline-brute-forceable from a single captured token — after which an attacker mints tokens the gateway accepts as valid indefinitely. 32 bytes matches HMAC-SHA256's block-size-appropriate key length and what `openssl rand -base64 32` produces, which is what `.env.example` already tells you to use.

**Operational note:** if the current `.env` holds a `JWT_SECRET` under 32 characters, auth and the gateway will refuse to start until it's rotated. That's intended, but it's a startup-breaking change rather than a silent one — rotating also invalidates any tokens already issued, which for a dev database with test users is a non-event.

---

## 3. Commands

| Command | Purpose |
|---|---|
| `make docker-up` / `make docker-down` | Start/stop Postgres + Redis |
| `make run-auth` (terminal 1) | Auth service on `:8081` |
| `make run-market-data` (terminal 2) | Market-data service on `:8082` |
| `make run-gateway` (terminal 3) | Gateway on `:8080` |
| `cd services/gateway && go test ./...` | Run unit tests |
| `cd services/gateway && go mod tidy` | Sync deps |

Manual verification — the checklist's "all backend requests work through the single gateway port":

```bash
curl -i localhost:8080/healthz                                    # 200 ok, gateway itself

# /auth/* is public and proxied
curl -s -X POST localhost:8080/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"a@b.com","username":"a","password":"password123"}'   # 201 + token pair

TOKEN=$(curl -s -X POST localhost:8080/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"a@b.com","password":"password123"}' | jq -r .access_token)

curl -i localhost:8080/auth/me -H "Authorization: Bearer $TOKEN"  # 200 (auth service validates)

# /market-data/* is gateway-protected
curl -i localhost:8080/market-data/symbols                        # 401, JSON error shape, never reaches :8082
curl -i localhost:8080/market-data/symbols -H "Authorization: Bearer $TOKEN"     # 200
curl -i localhost:8080/market-data/prices/AAPL -H "Authorization: Bearer $TOKEN" # 200 (poller has ticked)

# spoofing is blocked
curl -i localhost:8080/market-data/symbols -H "X-User-ID: someone-else"          # 401 — no token, header ignored

# placeholder + CORS
curl -i localhost:8080/trading/orders -H "Authorization: Bearer $TOKEN"          # 501
curl -i -X OPTIONS localhost:8080/market-data/symbols \
  -H 'Origin: http://localhost:5173' \
  -H 'Access-Control-Request-Method: GET'                                        # 204 + CORS headers + Vary: Origin
curl -i -X OPTIONS localhost:8080/market-data/symbols \
  -H 'Origin: http://evil.example' \
  -H 'Access-Control-Request-Method: GET'                                        # no Allow-Origin echoed (§2.7)

# upstream down (stop market-data, then)
curl -i localhost:8080/market-data/symbols -H "Authorization: Bearer $TOKEN"     # 502 + JSON error shape

# §2.4 — backends must not be reachable from the network
lsof -nP -iTCP:8081 -sTCP:LISTEN   # 127.0.0.1:8081, not *:8081
lsof -nP -iTCP:8082 -sTCP:LISTEN   # 127.0.0.1:8082, not *:8082
```

Note `JWT_SECRET` must be **identical** for the auth service and the gateway — both read it from the same `.env`, so this is automatic locally, but it's the single most likely misconfiguration when these move to separate deploy targets.

---

## 4. Project structure

Existing, reused as-is (this spec writes no new JWT code):
```
pkg/auth/
  jwt.go                          # ValidateToken, GenerateToken, Claims{TokenType, RegisteredClaims}
  middleware.go                   # RequireAuth(secret), UserIDFromContext(ctx)
```

New, to be created by this spec:
```
services/gateway/
  cmd/server/main.go              # env load (JWT_SECRET fail-fast + length check, URLs defaulted),
                                  #   shared transport, proxies, router, http.Server w/ ReadHeaderTimeout (§2.12)
  internal/proxy/
    proxy.go                      # New(target *url.URL, transport http.RoundTripper, serviceName string) *httputil.ReverseProxy
                                  #   — sets ErrorHandler → 502 JSON (§2.8); no path rewriting (§2.2)
    proxy_test.go                 # httptest backend: path/method/body/header forwarding; dead upstream → 502 JSON
  internal/middleware/
    cors.go                       # CORS(allowedOrigin string) — preflight short-circuit, exact-match, Vary: Origin (§2.7)
    cors_test.go                  # preflight 204 + headers; simple request passthrough; non-matching origin; Vary always
    identity.go                   # StripUserID() — global, outermost; InjectUserID() — authenticated routes only (§2.5)
    identity_test.go              # injection after auth; inbound spoof header stripped on an unauthenticated route
  internal/handler/
    router.go                     # chi: StripUserID → CORS → /healthz, /auth/* public proxy,
                                  #   /market-data/* + /trading/* behind RequireAuth + InjectUserID
    router_test.go                # route wiring, 401 without token, 200 with, 501 on /trading/*
    errors.go                     # ErrorResponse{Code,Message} + WriteError — third copy, see §7
```

Modified elsewhere (§2.4, §2.12):
```
pkg/auth/jwt.go                              # + exported minimum-secret-length check
services/auth/cmd/server/main.go             # BIND_ADDR (default 127.0.0.1); adopt secret-length check
services/market-data/cmd/server/main.go      # BIND_ADDR (default 127.0.0.1)
```

`go.work` gains `./services/gateway`. `services/gateway/go.mod` gains `github.com/go-chi/chi/v5` and `github.com/kpeguero/quantsim/pkg` with `replace ... => ../../pkg`, matching auth's go.mod exactly.

`Makefile`: `run-gateway` recipe changes to `go run ./cmd/server` (§2.10).
`.env.example`: gains a commented "Service URLs (gateway)" block and `BIND_ADDR` (§2.3, §2.4).

---

## 5. Code style / conventions

- **Layering:** the gateway has no service/store layer to speak of — it's middleware → proxy. The equivalent discipline here is that `internal/handler/router.go` composes middleware and proxies but contains no proxy or CORS *logic* itself; both live in their own testable packages.
- **Errors:** same JSON shape as auth and market-data, `{"code": "...", "message": "..."}`. Codes used by this service: `invalid_token` (401 — emitted by `pkg/auth.RequireAuth`, already implemented), `upstream_unavailable` (502), `not_implemented` (501).
- **Router:** `go-chi/chi/v5`, `r.Handle("/auth/*", proxy)` style prefix mounts — the proxy handles the whole subtree, chi does not need to know the backend's individual routes. That's the point of §2.2.
- **Middleware order (fixed, and load-bearing):** `StripUserID` → `CORS` → (route group) → `RequireAuth` → `InjectUserID` → proxy. `StripUserID` is outermost so no route can ever receive a client-set identity header (§2.5); CORS sits outside `RequireAuth` so a `401` still carries CORS headers, otherwise the browser surfaces an opaque network error instead of the real status (§2.7).
- **Logging:** `log.Printf` on startup (port, resolved backend URLs) and inside the proxy `ErrorHandler`. No request logging middleware in Phase 1. **Never log the `Authorization` header, a raw token, or `JWT_SECRET`.**
- **New dependencies beyond `go-chi/chi/v5` require sign-off first** — none expected; the proxy, CORS, and JWT pieces are all stdlib or existing `pkg/`.

---

## 6. Testing strategy

The gateway is unusually well-suited to unit testing — every dependency is an HTTP endpoint, and `httptest` fakes those natively. No mocking library, hand-written fakes only, matching `docs/TESTING_STRUCTURE.md`.

- **Proxy (`internal/proxy`):** spin up an `httptest.Server` as the "backend," point a proxy at it, assert the backend received the exact path, method, body, and `Authorization` header (§2.2). Separate case: target a closed port, assert `502` + the JSON error body (§2.8).
- **CORS (`internal/middleware`):** `OPTIONS` preflight with a matching `Origin` → `204` + all four headers, and the wrapped handler was **not** called; a simple `GET` with matching `Origin` → headers present and handler called; a non-matching `Origin` → no `Allow-Origin` header echoed.
- **Identity (`internal/middleware`):** with a valid token on the context, `X-User-ID` equals the subject; with a client-supplied `X-User-ID` **and no valid token**, the header never reaches the backend (§2.5) — this is the spoofing regression test and is the one that matters most.
- **Router (`internal/handler`):** full stack against `httptest` backends — `/auth/*` reachable with no token; `/market-data/*` → `401` + JSON shape with no token, `200` with a token generated by `pkg/auth.GenerateToken`; `/trading/*` → `501`; `/healthz` → `200` without touching any backend.
- **Not in scope:** real cross-service integration tests (the §3 curl sequence is the real-dependency proof, per `agents.md`'s "Phase 1: manual/curl verification is sufficient"), load/concurrency testing, browser-driven CORS verification (Step 8's frontend is the real proof there).
- `go test ./...` passes before any checkpoint is marked done.

---

## 7. Resolved: the third copy of `errors.go` stays

Step 5's spec (§7) flagged that auth's `ErrorResponse`/`WriteError` was duplicated rather than extracted into `pkg/`, and the call was "leave duplicated, two data points isn't enough." This step makes it **three**, which is the usual trigger to extract.

**Decision: still defer, narrowly.** Extracting `pkg/httputil` means editing two working, tested services for a ~15-line move, inside a step whose whole job is adding a third service — it mixes a refactor into a feature diff and makes both harder to review. Better as its own mechanical commit once Step 7 lands. Security-neutral either way; nothing about the duplication affects the error shape's correctness.

---

## 8. Boundaries

**Always do:**
- Strip inbound `X-User-ID` globally, as the outermost middleware, on every route including public ones (§2.5) — security invariant, not a nicety
- Keep CORS outside `RequireAuth`, so `401`s carry CORS headers (§2.7)
- Send `Vary: Origin` on every response (§2.7)
- Bind every service to `127.0.0.1` by default; only `BIND_ADDR` may widen it (§2.4)
- Use the standard JSON error shape for every 4xx/5xx the gateway itself emits
- Forward paths unchanged (§2.2) — no prefix stripping
- Run `go test ./...` before flagging a checkpoint done

**Ask first:**
- Any dependency beyond `go-chi/chi/v5`
- Making the CORS origin, or any backend URL, behave differently than §2.3/§2.7 describe
- Adding request logging, rate limiting, request body size limits, or metrics middleware — all deliberately out of scope
- Service-to-service authentication between gateway and backends (§2.4) — Phase 4 infra decision
- Extracting `pkg/httputil` (§7, resolved as deferred — reopen deliberately, not incidentally)

**Never do:**
- Commit `.env` or a real `JWT_SECRET`
- Log the `Authorization` header, a raw/decoded token, or the signing secret
- Use `Access-Control-Allow-Origin: *`, reflect an unvalidated `Origin`, or enable `Allow-Credentials` (§2.7)
- Trust any client-supplied identity header
- Let `/market-data/*` or `/trading/*` be reachable through the gateway without a valid access token
- Bind a backend service to `0.0.0.0` by default (§2.4)

---

## 9. Confirm before I start

Khalil delegated the open decisions to the implementer on 2026-07-29 with the instruction to decide with a security mindset. Resolutions:

- [x] Port `8080`; `/auth/*` public, `/market-data/*` + `/trading/*` protected (§1) — as drafted
- [x] No path rewriting — gateway and backends share the same path space (§2.2) — as drafted
- [x] Backend URLs defaulted to localhost; `JWT_SECRET` fail-fast (§2.3) — as drafted, plus a 32-byte minimum (§2.12)
- [x] **Reversed (§2.4):** the draft deferred backend exposure to Phase 4 on the premise that backends were localhost-only. They weren't — a bare `:port` binds `0.0.0.0`, so both services are currently open on the LAN. All three now default to `127.0.0.1` with a `BIND_ADDR` override.
- [x] **Tightened (§2.5):** `X-User-ID` stripping moves from the authenticated group to the outermost middleware. The draft left public, proxied `/auth/*` spoofable the moment anything downstream reads that header.
- [x] `/trading/*` answers `501` rather than proxying to a dead URL (§2.6) — as drafted
- [x] Hand-written CORS kept over `go-chi/cors` (§2.7), on the reasoning in that section's table — plus `Vary: Origin`, which the draft omitted
- [x] Shallow `/healthz`, no downstream health aggregation (§2.9) — as drafted
- [x] Gateway adopts `cmd/server` layout; `make run-gateway` recipe updated (§2.10) — as drafted
- [x] `errors.go` third copy stays; `pkg/httputil` extraction deferred to its own commit (§7)
- [x] **Added (§2.12):** `ReadHeaderTimeout` on the gateway server, 32-byte minimum `JWT_SECRET` — neither was in the draft

Checkpoint slicing lives in `tasks/plan.md`, mirroring how Steps 5 and 6 were sliced.
