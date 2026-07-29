# QuantSim Market Data Service — Live Polling Task Checklist

Full detail (acceptance criteria, verification commands, dependency graph) in `tasks/plan.md`. Each task is a checkpoint: implement, verify, stop for architect review before starting the next.

Prior Market Data (Step 5, historical ingestion) checklist archived at `docs/archive/phase1-step5-market-data/todo.md` — complete. Auth Service (Step 4) archived at `docs/archive/phase1-step4-auth/todo.md` — complete.

- [x] **Task 1** — Redis price cache + `GET /market-data/prices/:symbol` (read path)
- [x] **Task 2** — Alpaca snapshot client (`internal/alpaca`)
- [x] **Task 3** — Poller: background tick loop, end-to-end
