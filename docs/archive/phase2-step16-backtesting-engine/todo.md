# Todo — Backtesting Engine MVP (Step 16)

Tracks `tasks/plan.md`'s 14 tasks. All complete.

## Phase 1 — Schema and scaffold
- [x] T1 Migration `007_backtests.up/down.sql`
- [x] T2 `services/backtesting` module scaffold, `go.work`, `cmd/server/main.go`

## Phase 2 — Pure computation
- [x] T3 `internal/service/strategy.go` — `GenerateSignals`
- [x] T4 `internal/service/simulate.go` — `Simulate`
- [x] T5 `internal/service/metrics.go` — `ComputeMetrics`

## Phase 3 — I/O layers
- [x] T6 `internal/client/market_data_client.go` — `History`
- [x] T7 `internal/store/postgres_backtest_store.go` — Save/List/Get

## Phase 4 — Orchestration, HTTP, gateway
- [x] T8 `internal/service/backtest.go` — `RunBacktest`, validation
- [x] T9 `internal/handler/` — router, handlers, wired into `main.go`
- [x] T10 Gateway: proxy, `Makefile`, `.env.example` — plus the bare
      `/backtests` route fix (chi's wildcard alone doesn't match it)

## Phase 5 — Verification and close-out
- [x] T11 `services/backtesting/integration/` — third copy of the harness
- [x] T12 Mutation-tested `GenerateSignals`/`Simulate`/`ComputeMetrics` —
      one real test gap found and closed (see PHASE2_CHECKLIST.md)
- [x] T13 Manual adversarial pass against the real stack and real AAPL
      history
- [x] T14 `PHASE2_CHECKLIST.md`, `docs/NEXT_SESSION.md`, archive, and
      `docs/deferred-tuning.md` §11 (the integration-harness trigger)
