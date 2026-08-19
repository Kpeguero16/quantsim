# Todo — RSI & MACD Strategies (Step 18)

Tracks `tasks/plan.md`'s 19 tasks. None started.

## Phase 1 — The strategy abstraction and its indicators
- [x] T1 `strategy.go` — `Strategy` interface, `NewStrategy`, `maCrossover`
      behind it, `crossoverSignals` extracted. No behavior change.
- [x] T2 `indicators.go` — `wilderRSI` and `ema`, against hand-computed
      reference fixtures. Lands before T3/T4 (plan D1).
- [x] T3 `rsiStrategy` — exit-from-zone signals, `WarmupBars = period + 1`
- [x] T4 `macdStrategy` — signal-line crossings,
      `WarmupBars = slow + signal - 1` (committed with T5)
- [x] T5 `maxWarmupBars = 500` in `NewStrategy`; delete `maxLongWindow`
- [x] **Checkpoint A** — `make test`, `make vet` green

## Phase 2 — Wire format, orchestration, schema, store
- [x] T6 `types.go` — `{Strategy, Params}` replaces the two window fields
- [x] T7 `backtest.go` — `NewStrategy` in validation, `WarmupBars()` guard,
      `strategy.GenerateSignals`. `Simulate`/`ComputeMetrics` untouched.
- [x] T8 Migration `008_backtest_strategies.up/down.sql` (up pre-validated;
      down deletes non-MA rows, plan D2) — applied to real dev DB
- [x] T9 Store — `strategy, params` through INSERT, both SELECTs,
      `scanBacktest`; mock fixtures (committed with T10)
- [x] T10 Handler — needed zero changes, confirmed by grep
- [x] **Checkpoint B** — `make vet`/`test`/`test-integration` all green
      (all 3 services); migration applied to real DB, `users=20` baseline
      held; all three strategies run live via curl (MA/RSI/MACD), GET
      list + GET by id + unknown-strategy rejection all verified; throwaway
      accounts and their backtests deleted afterward (`users=20,
      accounts=20, backtests=0` restored); gateway/backtesting processes
      killed, auth/market-data/trading-engine left as they were

## Phase 3 — Frontend
- [x] T11 `api/types.ts` — discriminated `BacktestParams` union
- [x] T12 `backtest-validation.ts` — switch on strategy, all three tested
- [x] T13 `strategy-display.ts` — `describeStrategy` + unknown-strategy
      fallback asserted explicitly
- [x] T14 `BacktestForm.tsx` — `<select>`, conditional fields, per-strategy
      defaults
- [x] T15 `BacktestResult.tsx` / `BacktestHistoryList.tsx` — use
      `describeStrategy`
- [x] **Checkpoint C** — `npm run lint`/`build`/`test` all green (58 tests);
      `tsc -b` (the real build command, not a bare `--noEmit`) clean

## Phase 4 — Verification
- [x] T16 Integration — JSONB round trip for all three (migration-backfill
      re-verification descoped, see commit: already exhaustively covered
      by T8, and unreachable as a repeatable automated test through this
      harness's always-migrate-to-head design)
- [x] T17 Mutation-tested `maxWarmupBars`, RSI's `oversold < overbought`,
      and `crossoverSignals`' edge-only firing — all three caught (5 tests
      failed on the firing mutation alone), all three cleanly reverted,
      confirmed via `git diff` + full `make vet`/`make test`
- [x] T18 Manual browser pass — all three strategies run end to end
      through the real dashboard (MA 5/20, RSI(14) 30/70 with a live
      confirmation of the `profit_factor: null`/"no losing trades" render
      rule at 100% win rate, MACD(12/26/9)); the strategy `<select>`
      correctly swaps field groups and preserves prior values across
      switches; both zero-trade boundaries (RSI and MACD) render "No
      trades were simulated for this run." with no crash; the original
      MA run reopened from history correctly re-rendered
      "AAPL — 5/20 crossover"; zero console errors throughout. Throwaway
      account and its backtests deleted afterward (`users=20,
      accounts=20, backtests=0` restored); gateway/backtesting/frontend
      processes (started fresh for this pass) killed, auth/market-data/
      trading-engine left running as they were

## Phase 5 — Close-out
- [ ] T19 `PHASE3_CHECKLIST.md`, `docs/NEXT_SESSION.md`, archive to
      `docs/archive/phase3-step18-rsi-macd-strategies/`
