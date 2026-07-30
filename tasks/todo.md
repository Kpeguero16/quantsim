# QuantSim Minimal Frontend — Task Checklist (Phase 1, Step 8)

> **PAUSED HERE 2026-07-29, resuming 2026-07-30 — read this before continuing.**
> Session paused mid-Task-4 to save credits. Auto mode was active (user said
> "auto mode from now on" — proceed through checkpoints without stopping to
> ask, per-task review still expected but not gated on explicit go-ahead).
>
> **Done and committed**, in order: `0cd2b86` Task 1 (scaffold+Tailwind),
> `3197769` Task 2 (API client, verified live incl. concurrent-refresh),
> `62b2501` Task 3 (AuthContext + login screen, verified live incl.
> accessibility tree + storage-empty check), `c5b884f` **WIP, not a
> complete task**: `frontend/src/market/use-prices.ts` — the polling hook
> only, typechecked but **not yet wired into any component and not
> exercised against the live stack**.
>
> **Next concrete step:** build `frontend/src/market/Dashboard.tsx` and
> `PriceList.tsx` per `tasks/plan.md` Task 4, consuming `usePrices()`
> (already written) and `api.symbols()` (already in `api/client.ts`).
> Render `not-cached` state as `—`, `error` state distinctly, `ok` state
> with the tabular-nums utility from `index.css`. Then verify per Task 4's
> acceptance criteria in `tasks/plan.md` — including the interval-cleanup
> check on logout — before moving to Task 5 (chart).
>
> **One loose end**: `use-prices.ts` has an unresolved oxlint
> `react-hooks/exhaustive-deps` warning (non-blocking, `npm run lint` still
> exits 0) — see the comment left in that file before spending time on it
> again; a disable-comment did not suppress it at any line tried.
>
> **Environment left running** (loopback only, harmless, but may be stale
> by the time you resume): Postgres + Redis via `docker compose`, and `go
> run ./cmd/server` for auth/market-data/gateway, and the Vite dev server —
> all started manually during Task 2/3 verification, not via a durable
> process manager. Check `docker compose ps` and `lsof -nP -iTCP:8080-8082,5173`
> before assuming any of them are still up; restart with `make docker-up`
> then `make run-auth`, `make run-market-data`, `make run-gateway`,
> `make run-frontend` if not.
>
> Full detail (acceptance criteria, verification steps, dependency graph, risks) in `tasks/plan.md`.

`SPEC.md` is **approved** — all nine proposed decisions accepted 2026-07-29, no reversals. **Implementation is unblocked.**

Prior steps archived: Auth Service (Step 4) at `docs/archive/phase1-step4-auth/`; Market Data historical ingestion (Step 5) at `docs/archive/phase1-step5-market-data/`; Market Data live polling (Step 6) at `docs/archive/phase1-step6-market-data-live/`; API Gateway (Step 7) at `docs/archive/phase1-step7-gateway/` — all complete.

Each task is a stop-for-review checkpoint per `agents.md`: implement, verify, **stop**. Phase checkpoints (✅) are additional integration gates where the whole app is exercised, not just the new slice.

### Phase 1: Foundation
- [x] **Task 1** — Vite `react-ts` scaffold + Tailwind v4 + port 5173 `strictPort` + `frontend/.env.example` + `make run-frontend` (`0cd2b86`)
- [x] **Task 2** — `src/api/types.ts` + `src/api/client.ts`: bearer injection, `{code, message}` parsing, 401 → shared-promise refresh → retry once (`3197769`)

- [x] ✅ **Checkpoint: Foundation** — build + lint clean; refresh-retry exercised against the live stack; no tokens logged

### Phase 2: Authentication
- [x] **Task 3** — `AuthContext` (in-memory tokens) + login/register screen + `App.tsx` conditional render (`62b2501`)

- [x] ✅ **Checkpoint: Authentication** — full register/login/logout cycle; **CORS confirmed in a real browser**; storage empty of tokens

### Phase 3: Market data
- [ ] **Task 4 — IN PROGRESS** — Dashboard: `/market-data/symbols` + 15s price poll; `404 price_not_cached` → `—`; interval cleanup on logout.
      Polling hook done (`c5b884f`, `use-prices.ts`, typechecked but unwired and unverified). **Still needed:** `Dashboard.tsx`, `PriceList.tsx`, wiring into `App.tsx`'s logged-in branch, and the full live-stack verification pass.
- [ ] **Task 5** — Lightweight Charts v5 candlestick panel from `/market-data/history/{symbol}`

- [ ] ✅ **Checkpoint: Market data** — register → login → prices → chart works in a browser; chart matches raw API values

### Phase 4: Close out
- [ ] **Task 6** — Full §3 E2E incl. the refresh path, Phase 1 handoff criteria, check off Step 8

- [ ] ✅ **Checkpoint: Complete** — Phase 1 done; then the auth-hardening step, then Phase 2

---

Every checkpoint runs `npm run build` and `npm run lint` before being flagged done — there is no test suite standing behind this step (`SPEC.md` §6).

**No open questions** — `SPEC.md` §9 is fully resolved.

**Queued after this step:** a small auth-hardening step (`SPEC.md` §2.12) — password minimum and email format validation in the auth service, with its own spec. Decided 2026-07-29 to land after Step 8 and before Phase 2. Not part of this plan.

Step 8 docs (`SPEC.md`, `tasks/plan.md`, `tasks/todo.md`) stay at the repo root until Phase 2's spec is drafted, then move to `docs/archive/phase1-step8-frontend/` — matching how Steps 4→5, 5→6, and 6→7 were archived.
