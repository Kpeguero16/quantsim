# Plan — QuantSim Minimal Frontend (Phase 1, Step 8)

## Context

`SPEC.md` (draft 2026-07-29) defines the minimal frontend: two screens over the gateway's single origin, in-memory tokens, polled prices, and one candlestick chart. Per the working agreement in `agents.md`, checkpoints are vertical slices sized to "one logical piece," reviewed before the next starts.

Lesson carried over from Steps 5–7: order checkpoints so the lowest-dependency, most-reviewable piece goes first, and give the risky/subtle piece its own isolated checkpoint rather than folding it into the thing that consumes it. In Step 7 that was the middleware. **Here it is `api/client.ts`** — the 401-refresh-retry logic (SPEC.md §2.6) is the only genuinely non-obvious code in this step, and it ships with no unit tests (SPEC.md §6), so it gets its own checkpoint with its own explicit manual verification rather than arriving buried inside an auth-context diff.

## Decided defaults (from SPEC.md §2, restated for quick reference while implementing)

- **Scope:** login/register + dashboard + one chart. Nothing else — SPEC.md §2.1.
- **Prices:** poll `GET /market-data/prices/:symbol` every **15s** — SPEC.md §2.2.
- **Base URL:** `VITE_API_BASE_URL`, default `http://localhost:8080`, in `frontend/.env.example` only — SPEC.md §2.3.
- **Port:** 5173 with `strictPort: true` — the gateway's CORS origin is a hardcoded constant — SPEC.md §2.4.
- **Tokens:** access + refresh in memory only, never persisted — SPEC.md §2.5.
- **401 handling:** refresh → retry **once**; shared in-flight promise; `/auth/refresh` never recurses — SPEC.md §2.6.
- **`404 price_not_cached`:** renders `—`, is not an error — SPEC.md §2.7.
- **Chart:** `lightweight-charts` v5, `chart.addSeries(CandlestickSeries, …)`, business-day `YYYY-MM-DD` times, sorted + de-duplicated — SPEC.md §2.8.
- **No router**, conditional render on auth state — SPEC.md §2.9.
- **Deps:** react, react-dom, lightweight-charts, tailwindcss + @tailwindcss/vite. Nothing else — SPEC.md §2.10.
- **Tailwind v4:** Vite plugin + `@import "tailwindcss"`. No `tailwind.config.js`, no PostCSS — SPEC.md §2.11.
- **Types:** wire-format `snake_case`, mirroring the Go json tags — SPEC.md §5.
- **Verification:** `npm run build` + `npm run lint` + manual E2E. No test framework — SPEC.md §6.

---

## Task 1 — Scaffold, Tailwind, and the dev-server contract

**Files:** `frontend/` (Vite scaffold), `vite.config.ts`, `src/index.css`, `.env.example`, `Makefile`

- `npm create vite@latest frontend -- --template react-ts`. **Note:** `frontend/` already contains `.gitkeep`, so the scaffolder will warn the directory is not empty — choose the option that keeps existing files. Delete `.gitkeep` afterwards; the directory is no longer empty.
- Tailwind v4: `npm install tailwindcss @tailwindcss/vite`, register `tailwindcss()` alongside `react()` in `vite.config.ts`, and replace `src/index.css` with `@import "tailwindcss";`. Strip the Vite demo styles and `App.css`.
- `vite.config.ts`: `server: { port: 5173, strictPort: true }` (§2.4).
- `frontend/.env.example`: `VITE_API_BASE_URL=http://localhost:8080`.
- `Makefile`: `run-frontend: cd frontend && npm run dev`, plus a `help` line.

**Acceptance:** `npm run dev` serves on **5173** (not 5174 — verify the number in the terminal); a Tailwind utility class visibly applies; `npm run build` and `npm run lint` pass. No `tailwind.config.js` and no PostCSS config exist (§2.11 — if either does, a v3 guide was followed).

**Depends on:** nothing.

---

## Task 2 — `api/client.ts` (the subtle slice)

**Files:** `src/api/types.ts`, `src/api/client.ts`

- `types.ts`: `TokenPair`, `MeResponse`, `SymbolsResponse`, `HistoryResponse`, `Bar`, `Price`, `ApiError`. Wire-format `snake_case`, transcribed from `services/auth/internal/service/types.go` and `services/market-data/internal/service/types.go` (§5).
- `client.ts`: base URL from `VITE_API_BASE_URL`; injects `Authorization: Bearer` from a token getter supplied by the auth layer (so the client does not import the context and create a cycle); parses `{code, message}` into a thrown `ApiError`; on `401` → refresh → retry once.
- The three requirements from §2.6 that are easy to get wrong: `/auth/refresh` bypasses the interceptor; retry happens **exactly once**; a module-level in-flight `Promise | null` is shared so concurrent 401s trigger one refresh.

**Acceptance:** `npm run build` + `npm run lint` pass. Exercised against the live stack (temporarily, from `main.tsx` or the browser console): a normal call succeeds; a call with a deliberately corrupted access token triggers exactly one `POST /auth/refresh` in the Network tab and the original request then succeeds; seven concurrent calls with an expired token produce **one** refresh, not seven.

**Depends on:** Task 1.

---

## Task 3 — Auth context and the login/register screen

**Files:** `src/auth/AuthContext.tsx`, `src/auth/LoginPage.tsx`, `src/App.tsx`, `src/main.tsx`

- `AuthContext`: holds `accessToken`, `refreshToken`, `user`, all in React state — nothing persisted (§2.5). Exposes `login`, `register`, `logout`. Supplies the token getter and the "refresh failed → clear state" callback that Task 2's client depends on.
- `LoginPage`: one screen, toggling between login and register. Register sends `{email, username, password}`; login sends `{email, password}`. Client-side checks are **UX only** — the backend validates non-empty and nothing more (§2.12), so the form must not present its checks as guarantees. The server's `{code, message}` is what gets displayed on rejection.
- On success: store the pair, call `GET /auth/me` (with the bearer — the auth service enforces its own middleware on that route), render the user.
- `App.tsx`: `user ? <Dashboard/> : <LoginPage/>` (§2.9).

**Acceptance:** `npm run build` + `npm run lint` pass. Against the live stack: register a brand-new user → lands logged in; log out → back to login; log in again → works; a duplicate email or a 6-char password shows the **backend's** message; reload → back to the login screen (expected, §2.5); DevTools shows no CORS errors and no token in any console output.

**Depends on:** Task 2.

---

## Task 4 — Dashboard: symbols and polled prices

**Files:** `src/market/Dashboard.tsx`, `src/market/PriceList.tsx`

- On mount: `GET /market-data/symbols` → seven symbols.
- Then poll `GET /market-data/prices/{symbol}` for each, every 15s (§2.2), in a `useEffect` whose cleanup **clears the interval** (§5).
- `404 price_not_cached` → that row shows `—` and stays healthy. Any other failure → an error state (§2.7).
- Selecting a row sets the symbol that Task 5's chart consumes.

**Acceptance:** `npm run build` + `npm run lint` pass. Against the live stack: all seven symbols listed; prices populate; values refresh on the 15s tick (watch the Network tab); stopping the market-data poller degrades cells to `—` rather than an error banner; logging out stops the polling (no further requests in the Network tab — this is the interval-cleanup check).

**Depends on:** Task 3.

---

## Task 5 — Candlestick chart

**Files:** `src/market/CandlestickChart.tsx`

- `GET /market-data/history/{symbol}` → `bars[]`.
- Map `Bar → CandlestickData`: `time` = the `timestamp`'s `YYYY-MM-DD` prefix (business-day string, not a UTC timestamp — §2.8), plus `open/high/low/close`. **Sort ascending and de-duplicate** before `setData`, or the library throws.
- `chart.addSeries(CandlestickSeries, {...})` — the v5 API. `chart.timeScale().fitContent()`.
- Dispose the chart in the `useEffect` cleanup; re-render on symbol change.

**Acceptance:** `npm run build` + `npm run lint` pass. Against the live stack: candles render for the selected symbol; selecting a different symbol re-renders with that symbol's data; a spot-check of the most recent candle's OHLC against `curl`'s raw JSON matches (§6 — the manual substitute for a unit test on the mapping); no console errors about unsorted or duplicate data; switching symbols repeatedly leaks no charts.

**Depends on:** Task 4.

---

## Task 6 — Close out Phase 1

- Run the full SPEC.md §3 manual E2E, including the refresh path (step 8 — force it by restarting auth with a different `JWT_SECRET` or temporarily lowering `AccessTokenTTL`; record the result rather than assuming it works).
- Walk the `PHASE1_CHECKLIST.md` handoff criteria explicitly: migrations clean, `.env.example` complete, gateway routes all three prefixes, E2E passes.
- Check off Step 8 in `PHASE1_CHECKLIST.md`.
- Archive `SPEC.md`, `tasks/plan.md`, `tasks/todo.md` to `docs/archive/phase1-step8-frontend/` when Phase 2's spec is drafted, per the Steps 4–7 convention.

**Depends on:** Task 5.

---

## Dependency graph

```
Task 1 (scaffold) → Task 2 (api client) → Task 3 (auth) → Task 4 (dashboard) → Task 5 (chart) → Task 6 (close out)
```

Strictly linear, unlike Step 7's. Each slice renders something the next one needs, and there is no independent pair worth parallelizing — the frontend is a single dependency chain from transport up to view.

## Risks

- **CORS surfaces here for the first time** (SPEC.md §1, §7). If the gateway's middleware has a defect, it appears in Task 3 as a browser error. The fix goes in the gateway and needs its own small spec — it is not frontend work, and it must not be worked around with Vite's `server.proxy`.
- **Empty data is likely on a first run.** If `historical_prices` was never populated, Task 5's chart will be blank through no fault of the code. Run the ingest curl in SPEC.md §3 before concluding the chart is broken.
- **Markets closed** means `—` everywhere in Task 4. That is correct behavior (§2.7), not a failure — verify against `redis-cli GET price:AAPL` before debugging.
- **Stale v3/v4 guides.** Tailwind v4 and Lightweight Charts v5 both changed their setup/API from the versions most examples describe. SPEC.md §2.8 and §2.11 record the verified current form; deviations from those are the likeliest source of a confusing build error.
