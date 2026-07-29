# QuantSim Auth Service — Task Checklist

Full detail (acceptance criteria, verification commands, dependency graph) in `tasks/plan.md`. Each task is a checkpoint: implement, verify, stop for architect review before starting the next.

- [x] **Module wiring** — `go.work` at repo root, `Makefile` `run-auth` fix
- [x] **Task 1** — Skeleton + `POST /auth/register` end-to-end
- [x] **Task 2** — `POST /auth/login`
- [x] **Task 3** — `POST /auth/refresh`
- [ ] **Task 4** — `GET /auth/me` (protected) + `pkg/auth` middleware
