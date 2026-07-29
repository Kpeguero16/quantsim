# QuantSim API Gateway — Task Checklist (Phase 1, Step 7)

Full detail (acceptance criteria, verification commands, dependency graph) in `tasks/plan.md`. Each task is a checkpoint: implement, verify, stop for architect review before starting the next.

Prior steps archived: Auth Service (Step 4) at `docs/archive/phase1-step4-auth/`; Market Data historical ingestion (Step 5) at `docs/archive/phase1-step5-market-data/`; Market Data live polling (Step 6) at `docs/archive/phase1-step6-market-data-live/` — all complete.

- [x] **Task 1** — Module setup (`go.mod`, `go.work`) + `internal/proxy` + `internal/httperr`
- [x] **Task 2** — Middleware: CORS (`Vary: Origin`, exact-match) + identity (`StripUserID` / `InjectUserID`)
- [x] **Task 3** — Router, `cmd/server/main.go` wiring, Makefile, `.env.example`, manual E2E
- [x] **Task 4** — Loopback binding across all three services + 32-byte `JWT_SECRET` guard
- [x] **Task 5** — Check off Step 7 in `PHASE1_CHECKLIST.md`

Step 7 docs (`SPEC.md`, `tasks/plan.md`, `tasks/todo.md`) stay at the repo root until Step 8's spec is drafted, then move to `docs/archive/phase1-step7-gateway/` — matching how Steps 4→5 and 5→6 were archived.
