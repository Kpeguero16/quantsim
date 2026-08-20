# SPEC — QuantSim Minimal Frontend (Phase 1, Step 8)

Status: **Approved 2026-07-29** — all nine proposed decisions accepted as drafted (§9), on top of the four settled before drafting (§2.1 scope, §2.2 polling, §2.5 token storage, §2.8 charting). One item remains open and does not block this step: the auth-service validation gap in §2.12. Implementation is unblocked; checkpoint slicing is in `tasks/plan.md`.
Scope: `frontend/` — the minimal React UI that proves Phase 1 works end to end. Not a whole-project spec — see `agents.md` for that context. Prior specs archived at `docs/archive/phase1-step4-auth/` (Auth Service), `docs/archive/phase1-step5-market-data/` (historical ingestion), `docs/archive/phase1-step6-market-data-live/` (live polling + Redis), `docs/archive/phase1-step7-gateway/` (API Gateway) — all complete.

---

## 1. Objective

Per `PHASE1_CHECKLIST.md` Step 8, build `frontend/` (currently an empty `.gitkeep`) so that:

- A user can **register** and **log in** against `/auth/*` through the gateway
- After login, a **dashboard** lists the watchlist symbols with their latest prices from `/market-data/prices/:symbol`
- Selecting a symbol renders an **OHLC candlestick chart** from `/market-data/history/:symbol`
- The whole thing talks to **one origin** — `http://localhost:8080` — because Step 7 made that true

This is the last piece of Phase 1. Completing it satisfies the handoff criteria in `PHASE1_CHECKLIST.md`: *register → login → dashboard with prices → chart for one symbol works*, which unblocks Phase 2 (Trading Engine).

This step is also the **first real proof of the gateway's CORS configuration**. Step 7's spec (§6) explicitly deferred browser-driven CORS verification to here: `curl` does not enforce CORS, so until a browser at `http://localhost:5173` makes these calls, the hand-written middleware in `services/gateway/internal/middleware/cors.go` is untested against its actual client.

**Out of scope for this spec:** WebSocket streaming (the `prices:{symbol}` pub/sub channels exist and the gateway will host fan-out, but not in this step — same deferral as Step 7 §1); any trading UI (`/trading/*` answers `501` until Phase 2); portfolio/account/positions views (no endpoints exist yet); a design system, dark-mode theming, or responsive mobile layout; production build/deploy of the frontend (Phase 4); frontend automated tests (§6).

---

## 2. Decisions

### 2.1 Scope is exactly the checklist — two screens, nothing more

**Settled by the architect, 2026-07-29.** Login/register, dashboard with prices, one chart. No portfolio view, no live WebSocket prices, no extra pages.

`agents.md` ranks priorities as: resume impact, systems depth, backend complexity, infra exposure, **UI polish last**. The frontend's job in Phase 1 is to *prove the backend works*, not to be a product. Every hour spent here is an hour not spent on the trading engine, which is where the resume signal actually lives. The bar is "an interviewer can see the stack work end to end," not "this looks shipped."

### 2.2 Live prices by HTTP polling, not WebSockets

**Settled by the architect, 2026-07-29.** The dashboard polls `GET /market-data/prices/:symbol`.

The market-data service already publishes to Redis `prices:{symbol}` pub/sub channels (Step 6), and streaming that to the browser is the more impressive story. But the gateway does not host WebSocket fan-out yet, and building it is *backend* work that Step 7's spec (§1) deliberately deferred to a later phase. Pulling it into Step 8 would blow past "minimal frontend" into "new gateway feature," and it would land without its own spec. Polling is ~10 lines and is honestly labelled as a Phase 1 stopgap.

**Poll interval: 15 seconds**, matched to the backend poller's own 10–15s cadence. Polling faster only burns requests against a Redis key that has not changed. Seven watchlist symbols → seven requests per tick, which is fine against a local gateway.

### 2.3 One origin, one base URL, from `VITE_API_BASE_URL`

Everything goes to `http://localhost:8080`. The frontend never learns that auth is on `:8081` and market-data on `:8082` — that was the entire point of Step 7.

`VITE_API_BASE_URL` defaults to `http://localhost:8080` when unset, so `npm run dev` works with no setup. It goes in `frontend/.env.example` (Vite only exposes `VITE_`-prefixed vars to client code, and `frontend/.env` is already covered by the root `.gitignore`). It is **not** added to the root `.env.example` — that file is consumed by the `Makefile` and the Go services; a Vite variable there would be misleading.

### 2.4 Vite must run on port 5173 — the gateway's CORS origin is hardcoded

`services/gateway/cmd/server/main.go:18` sets `allowedOrigin = "http://localhost:5173"` as a constant, deliberately (Step 7 §2.7: "a CORS origin is not a knob"). Vite's default dev port is 5173, so this works out of the box — but if 5173 is occupied Vite silently falls back to 5174 and **every API call will fail CORS**.

So: `vite.config.ts` sets `server: { port: 5173, strictPort: true }`. Failing loudly on a busy port is much better than a confusing wall of CORS errors that sends you debugging the gateway when the real problem is a stale dev server.

The gateway allows methods `GET, POST, OPTIONS` and headers `Authorization, Content-Type` (`cors.go`). Every call this spec makes fits inside that. Any future `PUT`/`DELETE`/`PATCH` needs a gateway change first.

### 2.5 Both tokens live in memory only — nothing is persisted

**Settled by the architect, 2026-07-29.** Access token and refresh token are held in React state (an `AuthContext`), never written to `localStorage`, `sessionStorage`, or a cookie.

The checklist left this open ("in memory or localStorage/sessionStorage"). Persisting the 7-day refresh token makes it readable by any injected script — and this is a *fintech* app, where the demo story is that the author thinks about credential handling. In-memory storage means an XSS payload has to win a race against a live page rather than simply reading a key out of storage at leisure.

**Accepted cost, stated plainly: a page refresh logs you out.** For a local demo that is a non-issue; for a real product you would move to a `HttpOnly; Secure; SameSite` refresh cookie, which is a *backend* change (the gateway currently sets no CORS credentials, so cookies are not even possible today). The spec records this as the known upgrade path, not as an oversight.

Corollary: **no token is ever logged**, and the access token appears only in the `Authorization` header.

### 2.6 The API client refreshes on 401, once, with a shared in-flight promise

Access tokens live 15 minutes (`services/auth/internal/service/auth.go:15`); refresh tokens 7 days. A dashboard left open past 15 minutes will start 401-ing. With no persistence (§2.5) the refresh token is still in memory, so the client can recover silently.

`src/api/client.ts` wraps `fetch` and, on a `401`:
1. Calls `POST /auth/refresh` with the in-memory refresh token
2. On success, stores the new pair and **retries the original request exactly once**
3. On failure, clears auth state and drops the user back to the login screen

Three details that are easy to get wrong and are therefore requirements, not suggestions:

- **A 401 from `/auth/refresh` itself must never trigger another refresh.** Otherwise an expired refresh token produces an infinite loop. The refresh call bypasses the interceptor.
- **Retry exactly once.** A second 401 after a successful refresh means something else is wrong; retrying again just loops.
- **Concurrent 401s share one refresh.** The dashboard fires seven price requests per tick, so an expiry mid-tick 401s all seven at once. Without a shared in-flight promise that is seven parallel refresh calls. The client holds a module-level `Promise | null` that all callers await.

Verified while drafting: `Service.Refresh` (`services/auth/internal/service/auth.go:91`) is **stateless** — it validates the signature, checks `TokenType == refresh`, and confirms the user still exists. There is no token store and no rotation, so the old refresh token stays valid until it expires and concurrent refreshes are harmless. The shared promise is therefore an efficiency measure, not a correctness one. **If Phase 2 adds refresh-token rotation or revocation, this becomes a correctness requirement** — noted here so the change is not made blind.

### 2.7 `404 price_not_cached` is a normal state and renders as `—`, not an error

`GET /market-data/prices/:symbol` returns `404` with code `price_not_cached` when Redis has no entry for that symbol (`services/market-data/internal/handler/market_data.go:85`). This happens routinely: markets closed, the poller only just started, or a symbol Alpaca did not return in the last snapshot.

Treating that as an error would make the dashboard look broken every evening and all weekend. So the price cell renders an em-dash and the row stays healthy. Only a non-404 failure surfaces as an error state.

This is the single most likely thing to get wrong in this step, which is why it has its own section.

### 2.8 Charting: TradingView Lightweight Charts

**Settled by the architect, 2026-07-29.** `lightweight-charts` over Recharts.

It is purpose-built for financial series, has a native candlestick type, weighs ~45KB, and looks like a trading terminal — which is exactly the fintech realism `agents.md` optimizes for. Recharts has no candlestick primitive; OHLC has to be faked with custom bar shapes, which is more code for a worse result.

Two API facts confirmed against the current v5 docs while drafting, because they changed from v4 and most examples online are stale:

- Series are created with **`chart.addSeries(CandlestickSeries, options)`** — the v4 `chart.addCandlestickSeries(options)` method is gone.
- `Time` accepts a **business-day string** like `'2026-07-28'`. Our bars are daily, so mapping the API's RFC3339 `timestamp` to its `YYYY-MM-DD` prefix is the correct conversion — not a UTC timestamp, which would introduce timezone drift on a daily series.

Lightweight Charts requires data **sorted ascending by time with no duplicate timestamps** or it throws. The mapping step sorts and de-duplicates rather than trusting the API's ordering.

### 2.9 No router — two states behind a conditional render

There are exactly two views: logged out and logged in. `react-router-dom` earns its place at three-plus routes with deep links and history; here it is a dependency, a `<BrowserRouter>`, and route config for something `authState.user ? <Dashboard/> : <LoginPage/>` does in one line.

Consequence accepted: no URL for "the AAPL chart," no browser back button between views. Neither is in the checklist. When Phase 2 adds trading, positions, and backtest views, adding the router is a contained change.

### 2.10 Dependency budget: four runtime packages

`react`, `react-dom`, `lightweight-charts`, and `tailwindcss` + `@tailwindcss/vite` (build-time). Plus what `npm create vite` scaffolds for TypeScript and linting.

**No** state library (two screens of `useState`), **no** data-fetching library (§2.6's wrapper is ~60 lines and the retry semantics are custom anyway), **no** component kit (Tailwind utilities are the whole point), **no** form library (two forms, four fields). Anything beyond this list needs sign-off — same rule Step 7 ran under.

### 2.11 Tailwind v4 via the Vite plugin

Verified against current docs while drafting, because v4's setup differs materially from v3 and most recalled knowledge describes v3:

```
npm install tailwindcss @tailwindcss/vite
```
```ts
// vite.config.ts
import tailwindcss from '@tailwindcss/vite'
export default defineConfig({ plugins: [react(), tailwindcss()] })
```
```css
/* src/index.css */
@import "tailwindcss";
```

There is **no `tailwind.config.js`** and **no PostCSS config** in v4 — if the implementation produces either, it has followed a v3 guide and is wrong. Theme customization, if ever needed, happens in CSS via `@theme`.

### 2.12 Client-side validation is UX only — and the backend currently validates less than you'd expect

**Finding, surfaced while drafting this spec.** The auth service's only registration validation is a non-empty check: `services/auth/internal/handler/auth.go:28` rejects blank `email`, `username`, or `password` with `400 invalid_request`, and that is all. There is **no password minimum length** and **no email format validation** anywhere in the handler or service layer — `Register` (`auth.go:36`) goes straight from the request to `bcrypt.GenerateFromPassword`. A one-character password and `email = "x"` are both accepted today.

That is a real gap for a fintech app, but fixing it is a **backend change and therefore out of scope for this step** (§8, "Ask first"). It needs its own small spec against the auth service. It is recorded in §9 for you to schedule rather than silently absorbed into frontend work.

For Step 8, the consequence is what the form may claim:

- The form applies light client-side checks (non-empty, a basic email shape, a suggested 8-character minimum) purely for **fast feedback**.
- It must **not** present those as security guarantees, because the server does not enforce them — anyone posting directly to the gateway bypasses the form entirely.
- **The server's response is authoritative.** On rejection, display the backend's `{code, message}` verbatim rather than a client-invented message. Codes to expect: `invalid_request` (400), `duplicate_user` (409), `invalid_credentials` (401 on login).

One edge worth knowing: bcrypt errors on inputs over 72 bytes, and `Register` propagates that as a generic error, so a very long password surfaces as a `500` rather than a `400`. The form's max-length attribute avoids tripping it. That, too, is a backend fix, not a frontend one.

---

## 3. Commands

Prerequisites — the full backend stack running (Node 24.5.0 / npm 11.18.0 confirmed present):

```bash
make docker-up          # Postgres + Redis
make run-auth           # :8081
make run-market-data    # :8082  (also starts the price poller)
make run-gateway        # :8080
make run-frontend       # :5173  (new target, this step)
```

If the price cells all show `—`, the poller has not populated Redis yet. Confirm with:

```bash
redis-cli GET price:AAPL
# and seed history if the chart is empty:
curl -X POST localhost:8080/market-data/ingest \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{}'
```

Manual E2E (the actual acceptance test for this step — §6):

1. Open `http://localhost:5173` → login screen, no console errors
2. Register a new user → lands on the dashboard
3. Dashboard lists all seven watchlist symbols; prices populate (or show `—` when markets are closed)
4. Select a symbol → candlestick chart renders
5. Wait ~15s → prices tick
6. Open DevTools → Network: every request goes to `localhost:8080`, carries `Authorization: Bearer …`, and has no CORS error
7. Reload the page → back to the login screen (§2.5, expected)
8. Log out → back to the login screen, no stale data

The 15-minute refresh path (§2.6) is slow to test by waiting. Force it by restarting the auth service with a different `JWT_SECRET`, or temporarily lowering `AccessTokenTTL` — whichever is done, do it during the checkpoint and note the result rather than marking the path "probably fine."

---

## 4. Project structure

Existing, consumed but not modified (this spec writes **no Go code**):

```
services/gateway/   # single origin :8080 — /auth/* public, /market-data/* JWT-gated
services/auth/      # /auth/register, /auth/login, /auth/refresh, /auth/me
services/market-data/  # /market-data/symbols, /history/{symbol}, /prices/{symbol}
```

New, to be created by this spec:

```
frontend/
  index.html
  package.json
  tsconfig*.json
  vite.config.ts             # react + tailwindcss plugins; port 5173, strictPort (§2.4, §2.11)
  .env.example               # VITE_API_BASE_URL=http://localhost:8080 (§2.3)
  src/
    main.tsx                 # mount, <AuthProvider>
    index.css                # @import "tailwindcss";  (§2.11)
    App.tsx                  # authState.user ? <Dashboard/> : <LoginPage/>  (§2.9)
    api/
      client.ts              # fetch wrapper: base URL, bearer, {code,message} parsing,
                             #   401 → shared-promise refresh → retry once (§2.6)
      types.ts               # TokenPair, MeResponse, Bar, HistoryResponse, Price, SymbolsResponse
                             #   — mirrors the Go json tags exactly (§5)
    auth/
      AuthContext.tsx        # in-memory tokens + user; login/register/logout/refresh (§2.5)
      LoginPage.tsx          # login + register, one screen, mode toggle (§2.12)
    market/
      Dashboard.tsx          # symbol list + 15s price poll + selection state (§2.2)
      PriceList.tsx          # rows; 404 price_not_cached → "—" (§2.7)
      CandlestickChart.tsx   # lightweight-charts v5; bars → business-day strings (§2.8)
```

Modified elsewhere:

```
Makefile              # + run-frontend target
PHASE1_CHECKLIST.md   # Step 8 boxes checked at close-out
```

The root `.gitignore` already covers `node_modules/` and `.env` (Step 1). `frontend/` is not a Go module and does not touch `go.work`.

---

## 5. Code style / conventions

- **Layering mirrors the backend's discipline:** `api/client.ts` owns transport (base URL, auth headers, error parsing, retry) and nothing above it calls `fetch` directly. Components own rendering. `AuthContext` owns credential state. A component that does its own `fetch` has broken the layering the same way a Go handler doing its own SQL would.
- **Types mirror the Go structs exactly** — `snake_case` field names as they appear on the wire (`access_token`, not `accessToken`). No camelCase remapping layer; it is one more place for a typo to hide, and matching the wire makes the two sides diffable by eye. Source of truth: `services/auth/internal/service/types.go` and `services/market-data/internal/service/types.go`.
- **Errors:** one `ApiError { status, code, message }` thrown by the client, built from the backend's `{code, message}` body. Every 4xx/5xx path in the stack returns that shape — including the gateway's 404/405 (`router.go:35-41`) — so one parser covers everything. Display `message`; branch on `code` (that is what `price_not_cached` is for).
- **Styling:** Tailwind utilities inline. No CSS modules, no styled-components, no `@apply` soup. Dark UI, since it is a trading dashboard.
- **State:** `useState` / `useEffect` / one context. Polling lives in a `useEffect` with a **cleanup that clears the interval** — a leaked interval that keeps firing after logout would send authenticated requests with a cleared token.
- **Logging:** no `console.log` of tokens, headers, or full auth responses (§2.5). Errors may log `code` and `message`.
- **New dependencies beyond §2.10 require sign-off first.**

---

## 6. Testing strategy

**No frontend test framework in this step** — no Vitest, no Testing Library, no Playwright. This is a deliberate deviation and is called out for explicit approval in §9.

The reasoning: `agents.md` states *"Phase 1: manual/curl verification is sufficient; automated tests are optional or Phase 2+"*, and the checklist's stated verification for Step 8 is the manual end-to-end run. Steps 4–7 all shipped Go unit tests because they were testing *logic* — token validation, CORS matching, identity stripping — where a test encodes a security invariant. Step 8 is two screens of glue over an already-tested API; standing up a test runner plus a DOM environment to assert that a list renders is scope the checklist does not ask for, against 3–5 hrs/week.

**The cost, stated so the decision is informed:** the two pieces here that *do* hold real logic — the 401-refresh-retry in `client.ts` (§2.6) and the bar-to-candlestick mapping in §2.8 — are exactly the kind of thing unit tests catch and manual clicking does not. Both get explicit manual verification steps in their checkpoints instead (§3 step 8 for refresh; a visual check against known OHLC values for the mapping). If either turns out fiddly during implementation, adding Vitest for those two modules alone is the right call and I will flag it rather than proceed untested.

**What does get verified, at every checkpoint:**
- `npm run build` passes — TypeScript compiles with no errors. This is the type-level regression net and is non-negotiable.
- `npm run lint` passes on the scaffolded oxlint config (`frontend/.oxlintrc.json`). Note: current Vite `react-ts` scaffolds ship **oxlint**, not ESLint — an earlier draft of this line said ESLint.
- The §3 manual E2E sequence runs clean against the live stack.
- Browser DevTools shows no CORS errors and no failed requests — the real proof of Step 7's CORS middleware (§1).

---

## 7. Note: this step is the browser-side proof Step 7 deferred

Step 7 §6 listed "browser-driven CORS verification" as out of scope with the note *"Step 8's frontend is the real proof there."* This is that step. If the CORS middleware has a defect — a missing header on the preflight, a `Vary` omission, an origin mismatch — it surfaces here as a browser error, not in Go tests.

If that happens, **the fix belongs in the gateway, not worked around in the frontend.** Disabling CORS, proxying through Vite's dev server to dodge it, or relaxing the origin check would hide a real bug in a security control. Vite's `server.proxy` in particular is tempting and wrong: it would make the browser same-origin and leave the gateway's CORS untested, which defeats the purpose of this step.

---

## 8. Boundaries

**Always do:**
- Route every request through `VITE_API_BASE_URL` (`localhost:8080`) — never call `:8081` or `:8082` directly (§2.3)
- Keep both tokens in memory only (§2.5)
- Render `404 price_not_cached` as `—`, not as an error (§2.7)
- Clear polling intervals in `useEffect` cleanup (§5)
- Sort and de-duplicate bars before handing them to Lightweight Charts (§2.8)
- Run `npm run build` and `npm run lint` before flagging a checkpoint done (§6)
- Mirror Go JSON field names exactly in TypeScript types (§5)

**Ask first:**
- Any dependency beyond the four in §2.10
- Adding a test framework (§6 — proposed as deferred; reopen deliberately)
- Adding a router (§2.9)
- Anything that requires a **gateway or backend change** — a new endpoint, a new HTTP method, a CORS adjustment. Those need their own spec; they are not frontend work.
- Persisting any token to storage or a cookie (§2.5)

**Never do:**
- Commit `frontend/.env`, a real credential, or a token
- `console.log` a token, an `Authorization` header, or a full auth response
- Work around a CORS failure in the frontend — including via Vite's `server.proxy` (§7)
- Retry a 401 more than once, or let `/auth/refresh` recurse into itself (§2.6)
- Trust client-side validation as a security boundary (§2.12)
- Build UI for `/trading/*` — it answers `501` until Phase 2 (§1)

---

## 9. Confirm before I start

Settled by the architect on 2026-07-29, written in as decided:

- [x] Scope is strictly the checklist — two screens, no portfolio view, no extra pages (§2.1)
- [x] HTTP polling for prices; WebSocket fan-out stays deferred (§2.2)
- [x] Both tokens in memory only; page refresh logs you out (§2.5)
- [x] Lightweight Charts over Recharts (§2.8)

Proposed by me — **all accepted as drafted by the architect on 2026-07-29**, no reversals:

- [x] **§2.4** — Vite pinned to port 5173 with `strictPort: true`, because the gateway's CORS origin is a hardcoded constant and a silent fallback to 5174 breaks every request confusingly
- [x] **§2.6** — 401 → refresh → retry-once, with a shared in-flight promise so a tick's seven parallel requests trigger one refresh. Verified the backend's refresh is stateless, so this is efficiency today but becomes correctness if Phase 2 adds rotation
- [x] **§2.7** — `price_not_cached` renders `—`; only non-404 failures show as errors
- [x] **§2.9** — No router; conditional render on auth state. Costs deep links and the back button, neither of which the checklist asks for
- [x] **§2.10** — Four runtime deps, nothing else: react, react-dom, lightweight-charts, tailwindcss
- [x] **§6** — **No frontend test framework in Step 8.** The deviation most worth your attention: Steps 4–7 all shipped tests. Rationale and the accepted cost are in §6; I will flag mid-build if `client.ts` proves fiddly enough to warrant Vitest for that module alone
- [x] **§2.3** — `VITE_API_BASE_URL` lives in `frontend/.env.example`, not the root `.env.example`, since the root file feeds the Makefile and the Go services
- [x] **§5** — TypeScript types use wire-format `snake_case` rather than a camelCase remapping layer
- [x] **§7** — A CORS failure gets fixed in the gateway; using Vite's `server.proxy` to dodge it is explicitly forbidden, since it would leave Step 7's middleware unproven

Contract for the build: these are now settled. Reversing one mid-implementation is a spec edit, not an in-flight judgment call — the §8 "Ask first" list governs.

Resolved separately — does **not** block this step:

- [x] **§2.12 — backend finding.** The auth service enforces *only* non-empty email/username/password on registration: no password minimum, no email format check, so a one-character password registers today. **Decided 2026-07-29: fix it as a small auth-hardening step after Step 8 closes and before Phase 2 begins**, with its own spec. Rationale — finishing Phase 1 keeps the E2E momentum, and the fix lands while the auth service is still fresh rather than buried inside the trading-engine spec. Step 8 is unaffected; the register form simply won't claim guarantees the server doesn't make (§2.12).

Checkpoint slicing lives in `tasks/plan.md`, mirroring how Steps 5, 6, and 7 were sliced.
