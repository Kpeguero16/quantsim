# QuantSim Minimal Frontend — Task Checklist (Phase 1, Step 8)

Full detail (acceptance criteria, verification commands, dependency graph, risks) in `tasks/plan.md`. Each task is a checkpoint: implement, verify, stop for architect review before starting the next.

Prior steps archived: Auth Service (Step 4) at `docs/archive/phase1-step4-auth/`; Market Data historical ingestion (Step 5) at `docs/archive/phase1-step5-market-data/`; Market Data live polling (Step 6) at `docs/archive/phase1-step6-market-data-live/`; API Gateway (Step 7) at `docs/archive/phase1-step7-gateway/` — all complete.

**Blocked until `SPEC.md` §9 is signed off.** Nine proposed decisions are awaiting accept/reverse; the one most worth attention is §6 (no frontend test framework in this step).

- [ ] **Task 1** — Vite `react-ts` scaffold + Tailwind v4 + port 5173 `strictPort` + `.env.example` + `make run-frontend`
- [ ] **Task 2** — `src/api/` types and fetch client: bearer injection, `{code, message}` parsing, 401 → shared-promise refresh → retry once
- [ ] **Task 3** — `AuthContext` (in-memory tokens) + login/register screen + `App.tsx` conditional render
- [ ] **Task 4** — Dashboard: `/market-data/symbols` + 15s price poll, `404 price_not_cached` → `—`
- [ ] **Task 5** — Lightweight Charts v5 candlestick panel from `/market-data/history/{symbol}`
- [ ] **Task 6** — Full E2E incl. the refresh path, Phase 1 handoff criteria, check off Step 8

Every checkpoint runs `npm run build` and `npm run lint` before being flagged done — there is no test suite standing behind this step (`SPEC.md` §6).

Step 8 docs (`SPEC.md`, `tasks/plan.md`, `tasks/todo.md`) stay at the repo root until Phase 2's spec is drafted, then move to `docs/archive/phase1-step8-frontend/` — matching how Steps 4→5, 5→6, and 6→7 were archived.
