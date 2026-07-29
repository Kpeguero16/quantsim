# QuantSim Market Data Service — Task Checklist

Full detail (acceptance criteria, verification commands, dependency graph) in `tasks/plan.md`. Each task is a checkpoint: implement, verify, stop for architect review before starting the next.

Prior Auth Service (Step 4) checklist archived at `docs/archive/phase1-step4-auth/todo.md` — complete.

- [x] **Module wiring** — add `./services/market-data` to root `go.work`
- [x] **Task 1** — Skeleton + `GET /market-data/symbols`
- [x] **Task 2** — Alpaca client (`internal/alpaca`)
- [x] **Task 3** — `POST /market-data/ingest` end-to-end
- [x] **Task 4** — `GET /market-data/history/:symbol`
