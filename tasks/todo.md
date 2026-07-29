# QuantSim Minimal Frontend — Task Checklist (Phase 1, Step 8)

Full detail (acceptance criteria, verification steps, dependency graph, risks) in `tasks/plan.md`.

`SPEC.md` is **approved** — all nine proposed decisions accepted 2026-07-29, no reversals. **Implementation is unblocked.**

Prior steps archived: Auth Service (Step 4) at `docs/archive/phase1-step4-auth/`; Market Data historical ingestion (Step 5) at `docs/archive/phase1-step5-market-data/`; Market Data live polling (Step 6) at `docs/archive/phase1-step6-market-data-live/`; API Gateway (Step 7) at `docs/archive/phase1-step7-gateway/` — all complete.

Each task is a stop-for-review checkpoint per `agents.md`: implement, verify, **stop**. Phase checkpoints (✅) are additional integration gates where the whole app is exercised, not just the new slice.

### Phase 1: Foundation
- [ ] **Task 1** — Vite `react-ts` scaffold + Tailwind v4 + port 5173 `strictPort` + `frontend/.env.example` + `make run-frontend`
- [ ] **Task 2** — `src/api/types.ts` + `src/api/client.ts`: bearer injection, `{code, message}` parsing, 401 → shared-promise refresh → retry once

- [ ] ✅ **Checkpoint: Foundation** — build + lint clean; refresh-retry exercised against the live stack; no tokens logged

### Phase 2: Authentication
- [ ] **Task 3** — `AuthContext` (in-memory tokens) + login/register screen + `App.tsx` conditional render

- [ ] ✅ **Checkpoint: Authentication** — full register/login/logout cycle; **CORS confirmed in a real browser**; storage empty of tokens

### Phase 3: Market data
- [ ] **Task 4** — Dashboard: `/market-data/symbols` + 15s price poll; `404 price_not_cached` → `—`; interval cleanup on logout
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
