# Implementation Plan — QuantSim Minimal Frontend (Phase 1, Step 8)

## Overview

Build `frontend/` — the minimal React UI that proves Phase 1 works end to end. Two screens (login/register, dashboard), talking to the gateway's single origin on `localhost:8080`, with polled prices and one candlestick chart. Completing it satisfies the `PHASE1_CHECKLIST.md` handoff criteria and unblocks Phase 2.

`SPEC.md` is **approved as of 2026-07-29** — all nine proposed decisions accepted, no reversals. This plan slices that spec into six checkpoints.

**Two levels of review gate this work, and they are different things:**

- **Per-task stops** — required by `agents.md`: *"Checkpoints are scoped to one logical piece of implementation at a time... small enough for Khalil to meaningfully review."* Implement one task, verify it, **stop**. Do not start the next task before review.
- **Phase checkpoints** — integration gates after every 2–3 tasks, where the whole app is exercised end to end rather than just the new slice. These catch the problems that only appear once pieces meet.

## Architecture decisions

Restated from `SPEC.md` §2 for quick reference while implementing. All are settled; reversing one is a spec edit, not an in-flight call.

- **Scope:** login/register + dashboard + one chart. Nothing else — §2.1.
- **Prices:** poll `GET /market-data/prices/:symbol` every **15s** — §2.2.
- **Base URL:** `VITE_API_BASE_URL`, default `http://localhost:8080`, in `frontend/.env.example` only — §2.3.
- **Port:** 5173 with `strictPort: true` — the gateway's CORS origin is a hardcoded constant — §2.4.
- **Tokens:** access + refresh in memory only, never persisted — §2.5.
- **401 handling:** refresh → retry **once**; shared in-flight promise; `/auth/refresh` never recurses — §2.6.
- **`404 price_not_cached`:** renders `—`, is not an error — §2.7.
- **Chart:** `lightweight-charts` v5, `chart.addSeries(CandlestickSeries, …)`, business-day `YYYY-MM-DD` times, sorted + de-duplicated — §2.8.
- **No router**, conditional render on auth state — §2.9.
- **Deps:** react, react-dom, lightweight-charts, tailwindcss + @tailwindcss/vite. Nothing else — §2.10.
- **Tailwind v4:** Vite plugin + `@import "tailwindcss"`. No `tailwind.config.js`, no PostCSS — §2.11.
- **Client-side validation is UX only** — the backend validates non-empty and nothing more — §2.12.
- **Types:** wire-format `snake_case`, mirroring the Go json tags — §5.
- **Verification:** `npm run build` + `npm run lint` + manual E2E. No test framework — §6.

## Dependency graph

```
Task 1 (scaffold + Tailwind)
    │
    └── Task 2 (api/types.ts, api/client.ts)
            │
            └── Task 3 (AuthContext + LoginPage + App)   ← CORS proven here
                    │
                    └── Task 4 (Dashboard + PriceList)
                            │
                            └── Task 5 (CandlestickChart)
                                    │
                                    └── Task 6 (E2E + close out Phase 1)
```

Strictly linear, unlike Step 7's. Each slice renders something the next one needs, and no independent pair is worth parallelizing — the frontend is a single dependency chain from transport up to view. **Nothing here can be parallelized across sessions.**

### On vertical slicing

The usual guidance is to slice vertically — one complete user-visible path per task. Tasks 3, 4, and 5 do exactly that ("user can log in," "user sees prices," "user sees a chart").

Tasks 1 and 2 are deliberately horizontal, and it is worth being explicit about why rather than pretending otherwise. Task 2 (`client.ts`) holds the only genuinely subtle logic in this step — 401 → refresh → retry-once with a shared in-flight promise — and it ships **without unit tests** (§6). Burying it inside Task 3's auth diff would mean the trickiest code in the step arrives inside the largest diff, reviewed last. Isolating it puts the highest-risk work early where it fails fast, and gives it verification criteria of its own. This is the same reasoning that gave the gateway's middleware its own checkpoint in Step 7.

---

# Phase 1: Foundation

## Task 1: Scaffold Vite + Tailwind and pin the dev-server contract

**Description:** Stand up the React/TypeScript project with Tailwind v4 and lock the dev server to port 5173, so that every later task has a working build and a browser origin the gateway will actually accept.

**Acceptance criteria:**
- [ ] `npm run dev` serves on **5173** — confirm the number printed in the terminal, not the assumption
- [ ] A Tailwind utility class visibly applies in the browser
- [ ] **No `tailwind.config.js` and no PostCSS config exist** — if either does, a v3 guide was followed and §2.11 was not

**Verification:**
- [ ] Build succeeds: `cd frontend && npm run build`
- [ ] Lint passes: `cd frontend && npm run lint`
- [ ] Manual check: `make run-frontend` opens a styled page at `http://localhost:5173`
- [ ] Manual check: occupy 5173 first (`nc -l 5173`), confirm Vite **fails loudly** rather than sliding to 5174

**Dependencies:** None

**Files likely touched:**
- `frontend/` (Vite `react-ts` scaffold)
- `frontend/vite.config.ts`
- `frontend/src/index.css`
- `frontend/.env.example`
- `Makefile`

**Estimated scope:** Medium

**Notes:** `frontend/` already contains `.gitkeep`, so the scaffolder will warn the directory is not empty — choose the option that keeps existing files, then delete `.gitkeep`. Strip the Vite demo styles and `App.css`. `frontend/.env.example` is committable despite `.gitignore`'s `.env.*` rule — the `!.env.example` negation applies at any depth (verified).

---

## Task 2: API types and fetch client

**Description:** Build the transport layer every other task sits on: typed wire models, a `fetch` wrapper that injects the bearer token and parses the backend's `{code, message}` error shape, and the 401 → refresh → retry-once path. This is the highest-risk task in the step and it ships without unit tests.

**Acceptance criteria:**
- [ ] Types mirror the Go json tags exactly, in wire-format `snake_case` — transcribed from `services/auth/internal/service/types.go` and `services/market-data/internal/service/types.go`
- [ ] A non-2xx response throws a typed `ApiError { status, code, message }` built from the body
- [ ] On 401: refresh, then retry the original request **exactly once**; `/auth/refresh` bypasses the interceptor so it can never recurse; concurrent 401s share **one** in-flight refresh promise

**Verification:**
- [ ] Build succeeds: `cd frontend && npm run build`
- [ ] Lint passes: `cd frontend && npm run lint`
- [ ] Manual check — **corrupt the access token in memory, then make a call.** DevTools Network shows exactly one `POST /auth/refresh`, followed by the original request succeeding
- [ ] Manual check — **fire seven concurrent calls with an expired token.** Network shows **one** refresh, not seven
- [ ] Manual check — **expire the refresh token too.** Auth state clears; no infinite refresh loop

**Dependencies:** Task 1

**Files likely touched:**
- `frontend/src/api/types.ts`
- `frontend/src/api/client.ts`

**Estimated scope:** Small (2 files, but the densest logic in the step)

**Notes:** The client takes a token getter injected by the auth layer rather than importing `AuthContext` — importing it would create a cycle, since the context calls the client. Verified while drafting: the backend's refresh is stateless with no rotation, so a duplicate refresh is wasteful but harmless *today*. The shared promise is therefore efficiency now and correctness later — do not remove it as "unnecessary."

---

## ✅ Checkpoint: Foundation (after Tasks 1–2)

- [ ] `npm run build` and `npm run lint` both clean
- [ ] The refresh-retry path has been exercised against the **live stack**, not reasoned about
- [ ] No token appears in any `console.log` output
- [ ] **Stop for architect review before Phase 2**

---

# Phase 2: Authentication

## Task 3: Auth context and the login/register screen

**Description:** Hold credentials in memory, render the login/register screen, and switch the app between logged-out and logged-in. This is the first task that runs a browser against the gateway, so it is where Step 7's CORS middleware gets its first real proof.

**Acceptance criteria:**
- [ ] `AuthContext` holds `accessToken`, `refreshToken`, and `user` in React state — **nothing written to `localStorage`, `sessionStorage`, or a cookie**
- [ ] One screen toggles between login and register; register posts `{email, username, password}`, login posts `{email, password}`
- [ ] On rejection the **backend's** `message` is displayed verbatim — not a client-invented string
- [ ] `App.tsx` renders `user ? <Dashboard/> : <LoginPage/>`

**Verification:**
- [ ] Build succeeds: `cd frontend && npm run build`
- [ ] Lint passes: `cd frontend && npm run lint`
- [ ] Manual check: register a brand-new user → lands logged in; log out → back to login; log in again → works
- [ ] Manual check: a duplicate email shows the backend's `duplicate_user` message; a wrong password shows `invalid_credentials`
- [ ] Manual check: reload the page → back to the login screen (**expected**, §2.5 — not a bug)
- [ ] Manual check: **DevTools Console and Network show zero CORS errors** — the real proof of Step 7 §6

**Dependencies:** Task 2

**Files likely touched:**
- `frontend/src/auth/AuthContext.tsx`
- `frontend/src/auth/LoginPage.tsx`
- `frontend/src/App.tsx`
- `frontend/src/main.tsx`

**Estimated scope:** Medium

**Notes:** `GET /auth/me` needs the bearer header — the gateway passes `/auth/*` through publicly, and the auth service enforces its own middleware on that route. Client-side validation is UX only (§2.12): the backend checks non-empty and nothing else, so the form must not present its checks as guarantees.

---

## ✅ Checkpoint: Authentication (after Task 3)

- [ ] Full register → login → logout cycle works through the gateway
- [ ] **CORS confirmed working in a real browser.** If it is not, the fix goes in the gateway and needs its own spec — do **not** work around it in the frontend, and specifically do not reach for Vite's `server.proxy` (§7)
- [ ] Nothing persisted: DevTools → Application → Storage is empty of tokens
- [ ] **Stop for architect review before Phase 3**

---

# Phase 3: Market data

## Task 4: Dashboard with polled prices

**Description:** List the watchlist symbols and keep their latest prices current on a 15-second poll, degrading gracefully when the cache is empty.

**Acceptance criteria:**
- [ ] `GET /market-data/symbols` renders all seven watchlist symbols
- [ ] Each symbol's price comes from `GET /market-data/prices/{symbol}`, refreshed every 15s
- [ ] **`404 price_not_cached` renders `—` and the row stays healthy**; only a non-404 failure shows an error state
- [ ] Selecting a row sets the symbol Task 5's chart consumes

**Verification:**
- [ ] Build succeeds: `cd frontend && npm run build`
- [ ] Lint passes: `cd frontend && npm run lint`
- [ ] Manual check: prices populate and visibly tick on the 15s interval (watch the Network tab)
- [ ] Manual check: **stop the market-data poller** → cells degrade to `—`, no error banner
- [ ] Manual check: **log out → the Network tab goes quiet.** This is the interval-cleanup test; a leaked interval would keep firing requests with a cleared token

**Dependencies:** Task 3

**Files likely touched:**
- `frontend/src/market/Dashboard.tsx`
- `frontend/src/market/PriceList.tsx`

**Estimated scope:** Small

**Notes:** The poll lives in a `useEffect` whose cleanup clears the interval (§5). Seven symbols → seven requests per tick, which is fine against a local gateway. If every cell shows `—`, confirm with `redis-cli GET price:AAPL` before assuming the code is wrong — markets being closed produces exactly this.

---

## Task 5: Candlestick chart

**Description:** Render OHLC candles for the selected symbol from the historical bars endpoint, using the Lightweight Charts v5 API.

**Acceptance criteria:**
- [ ] Candles render for the selected symbol from `GET /market-data/history/{symbol}`
- [ ] Bars map to `CandlestickData` with `time` as the timestamp's **`YYYY-MM-DD` business-day prefix** — not a UTC timestamp, which would drift on a daily series
- [ ] Data is **sorted ascending and de-duplicated** before `setData`, or the library throws
- [ ] Selecting a different symbol re-renders with that symbol's data

**Verification:**
- [ ] Build succeeds: `cd frontend && npm run build`
- [ ] Lint passes: `cd frontend && npm run lint`
- [ ] Manual check — **spot-check the most recent candle's OHLC against the raw JSON from `curl`.** This is the manual substitute for the unit test §6 declines to write; do it deliberately, do not eyeball the shape and move on
- [ ] Manual check: no console errors about unsorted or duplicate data
- [ ] Manual check: switch symbols ten times → no chart instances leak (the `useEffect` cleanup disposes)

**Dependencies:** Task 4

**Files likely touched:**
- `frontend/src/market/CandlestickChart.tsx`
- `frontend/src/market/Dashboard.tsx` (wiring only)

**Estimated scope:** Small

**Notes:** v5 API — `chart.addSeries(CandlestickSeries, {...})`. The v4 `chart.addCandlestickSeries()` method is **gone**; most examples online still show it. Call `chart.timeScale().fitContent()` after `setData`. If the chart is blank, run the ingest curl in `SPEC.md` §3 before concluding the code is broken — `historical_prices` may simply be empty.

---

## ✅ Checkpoint: Market data (after Tasks 4–5)

- [ ] The full Phase 1 story runs in a browser: register → login → prices → chart
- [ ] Prices tick; the chart matches the API's raw values
- [ ] No leaked intervals, no leaked chart instances
- [ ] **Stop for architect review before Phase 4**

---

# Phase 4: Close out

## Task 6: Full E2E and Phase 1 handoff

**Description:** Run the complete verification sequence including the slow path, confirm every Phase 1 handoff criterion, and mark the phase done.

**Acceptance criteria:**
- [ ] The `SPEC.md` §3 manual E2E sequence runs clean, all eight steps
- [ ] **The 15-minute refresh path is actually exercised**, not assumed — force it by restarting auth with a different `JWT_SECRET`, or by temporarily lowering `AccessTokenTTL`. Record the observed result
- [ ] Every `PHASE1_CHECKLIST.md` handoff criterion is confirmed: migrations clean, `.env.example` complete, gateway routes all three prefixes, E2E works
- [ ] Step 8 checked off in `PHASE1_CHECKLIST.md`

**Verification:**
- [ ] Build succeeds: `cd frontend && npm run build`
- [ ] Lint passes: `cd frontend && npm run lint`
- [ ] Backend unaffected: `go test ./...` still passes in `pkg`, `services/auth`, `services/market-data`, `services/gateway`
- [ ] Manual check: the full §3 sequence from a cold start (`make docker-up` onward)

**Dependencies:** Task 5

**Files likely touched:**
- `PHASE1_CHECKLIST.md`
- `docs/archive/phase1-step8-frontend/` (deferred — see notes)

**Estimated scope:** Extra small

**Notes:** Archiving `SPEC.md` / `tasks/` to `docs/archive/phase1-step8-frontend/` happens when the **next spec is drafted**, not here — matching how Steps 4→5, 5→6, and 6→7 were handled. The docs stay at the repo root until then.

**Hand off, do not drop:** the auth-hardening step (`SPEC.md` §2.12) comes next, **before** Phase 2 — password minimum and email format validation in the auth service, with its own spec. Decided 2026-07-29. Task 6 is where that handoff gets stated out loud, so it does not evaporate between phases.

---

## ✅ Checkpoint: Complete

- [ ] All acceptance criteria across Tasks 1–6 met
- [ ] Phase 1 handoff criteria satisfied
- [ ] **Phase 1 is done**
- [ ] Next: auth-hardening step (`SPEC.md` §2.12) — then Phase 2, Trading Engine

---

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| **CORS fails in a real browser** — first genuine test of Step 7's hand-written middleware (§1, §7) | High — blocks Task 3 and everything after | Fix in the **gateway**, with its own small spec. Never work around it in the frontend; Vite's `server.proxy` is explicitly forbidden because it would make the browser same-origin and leave the middleware unproven |
| **`client.ts` refresh logic is wrong and has no unit tests** (§6) | High — silent auth failures, or an infinite refresh loop | Task 2's three explicit manual checks. If it proves fiddly during the build, **stop and propose adding Vitest for that module alone** rather than shipping it unverified — §6 pre-authorizes raising this |
| **Empty data reads as broken code** — no ingested history, or markets closed | Medium — wasted debugging on working code | Confirm with `redis-cli GET price:AAPL` and the ingest curl (`SPEC.md` §3) **before** debugging the frontend. `—` everywhere is correct behavior after hours |
| **Stale v3/v4 guides** — Tailwind and Lightweight Charts both changed setup/API | Medium — confusing build errors | §2.8 and §2.11 record the verified current form. A `tailwind.config.js` or an `addCandlestickSeries()` call means a stale guide was followed |
| **Port 5173 occupied** → Vite falls back to 5174 → every request fails CORS | Medium — misleading failure that points at the wrong layer | `strictPort: true` (§2.4) turns it into a loud startup failure |
| **Scope creep into backend work** — a missing endpoint invites "just add it" | Medium — Step 8 balloons past "minimal," changes land without a spec | §8's "Ask first" list. Any gateway/backend change needs its own spec, full stop |

## Open questions

**None.** `SPEC.md` §9 is fully resolved as of 2026-07-29.

The one item that was outstanding — the auth-service validation gap (`SPEC.md` §2.12) — has been **scheduled as a small auth-hardening step after Step 8 closes and before Phase 2 begins**, with its own spec. It is out of scope for every task in this plan. Task 6 carries the reminder so it is handed off rather than forgotten.
