# Implementation Plan — RSI & MACD Strategies (Step 18)

`SPEC.md` is **approved** (2026-08-18, all four §3 decisions as recommended). This plan turns it into 19 tasks across 5 phases on branch `step18-rsi-macd-strategies` (already created).

Make the backtesting engine multi-strategy: a `Strategy` interface with three implementations, a `{strategy, params}` wire format replacing the two fixed window fields, `strategy TEXT` + `params JSONB` replacing the two window columns, and a strategy picker in the existing dashboard tab. `Simulate`, `ComputeMetrics`, `backtest_trades`, the fill-timing rule and the five metrics are **untouched** (SPEC.md §2.9) — all the work is upstream of `[]Signal`.

Baseline to re-check at every checkpoint:
```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT 'users=' || (SELECT count(*) FROM users) || ' backtests=' || (SELECT count(*) FROM backtests)"   # users=20 backtests=0
```

---

## Decisions carried in from SPEC.md §2 (not reopened here)

- `Strategy` interface — `Kind`/`Params`/`WarmupBars`/`GenerateSignals`, one `NewStrategy` constructor as the sole validation point — §2.1
- RSI: Wilder's smoothing; buy on the cross *up* out of oversold, sell on the cross *down* out of overbought; flat series reads `50` — §2.2, §3.1, §3.2
- MACD: SMA-seeded EMAs, signal-line crossings, sharing one `crossoverSignals` helper with the MA strategy — §2.3
- `WarmupBars()` = bars until the indicator has its **first value** (not until a signal is possible), preserving Step 16's exact rejection boundary; `maxWarmupBars = 500` replaces `maxLongWindow` — §2.4
- `strategy TEXT` + `params JSONB`, window columns dropped, no `CHECK` on strategy — §2.5, §3.4
- Nested `{strategy, params}` on the wire, params re-marshaled canonically, **no compatibility shim** — §2.6
- Backend and frontend in one step — §2.7, §3.3
- Frontend: one `<select>`, discriminated validation, one `describeStrategy` helper with an unknown-strategy fallback — §2.8

## Three decisions the plan adds

**D1 — Indicators get their own file and their own tasks, before the strategies that consume them.** `wilderRSI` and `ema` land in `internal/service/indicators.go` as pure `[]float64 → []float64` functions, tested against hand-computed reference fixtures (SPEC.md §4) *before* any strategy calls them. The alternative — writing `rsi.go` end to end and testing only its signals — is exactly how a wrong smoothing constant hides: the signals still look plausible. Sequencing the indicator first means a wrong number fails its own test, at the smallest possible unit.

**D2 — The down migration deletes non-`ma_crossover` rows, and says so loudly.** `008_backtest_strategies.down.sql` re-adds `short_window`/`long_window` and backfills them from `params` for `ma_crossover` rows — but an RSI or MACD run has no window values, and Step 16's schema cannot represent one. Fabricating `0/0` to satisfy `NOT NULL` would leave a plausible wrong row; deleting is the honest reversal. This follows `006_...down.sql`'s existing convention of stating in the file that the migration is not a round trip rather than pretending it is.

**D3 — `strategy_test.go`'s existing MA assertions are rewritten to construct through `NewStrategy`, not deleted or duplicated.** Step 16's crossover tests are the regression net proving T1's refactor changed no behavior, so they must keep running against the *new* call path. Any assertion that only compiles against the old free-function signature gets its call site updated and its expectations left exactly as they are — if an expectation needs to change, that is a behavior change and it stops the task.

---

## Phases

### Phase 1 — The strategy abstraction and its indicators (pure, unit-tested)

- **T1** `internal/service/strategy.go`: `StrategyKind` constants, the `Strategy` interface, `NewStrategy(kind, raw)` dispatching on kind, and `maCrossover` implementing it — moving Step 16's existing signal logic behind the interface unchanged. Extract `crossoverSignals(fast, slow []float64, from int) []Signal` (the `wasAbove`/`haveState` loop) for T4 to reuse. `strategy_test.go` updated per D3; **no behavior change** — that is this task's whole acceptance bar.
- **T2** `internal/service/indicators.go`: `wilderRSI(closes []float64, period int) []float64` and `ema(values []float64, period int) []float64`, both pure, both SMA-seeded (§2.2, §2.3). `indicators_test.go` against hand-computed reference fixtures carried through the seed and ≥3 smoothed values, plus §2.2's three degenerate rows (`100` / `0` / **`50`**). Per D1, this lands and passes before T3 or T4.
- **T3** `rsiStrategy`: parameters `period`/`oversold`/`overbought` with bounds (`period >= 2`, `0 < oversold < overbought < 100`), `WarmupBars() = period + 1`, exit-from-zone signals over T2's `wilderRSI`.
- **T4** `macdStrategy`: parameters `fast_period`/`slow_period`/`signal_period` (`fast >= 2`, `slow > fast`, `signal >= 2`), `WarmupBars() = slow + signal - 1`, MACD line and signal line over T2's `ema`, signals via T1's `crossoverSignals`.
- **T5** The shared `maxWarmupBars = 500` bound, checked in `NewStrategy` after construction; delete `maxLongWindow` from `backtest.go`. Test the case §2.4 exists to catch: a MACD whose `slow + signal - 1` exceeds 500 while `slow` alone does not.

**Checkpoint A:** `make test` and `make vet` green for `services/backtesting`. Every strategy constructible, validated, and generating signals — with nothing downstream wired up yet.

### Phase 2 — Wire format, orchestration, schema, store

- **T6** `internal/service/types.go`: `RunBacktestRequest` and `Backtest` lose `ShortWindow`/`LongWindow`, gain `Strategy StrategyKind` and `Params json.RawMessage`. `Metrics`, `TradeRecord`, `BacktestDetail`, `BacktestsResponse` unchanged.
- **T7** `internal/service/backtest.go`: `validateRequest` hands strategy construction to `NewStrategy` and keeps only the symbol/date/capital checks; `RunBacktest` swaps the `long_window > len(ranged)` guard for `strategy.WarmupBars() > len(ranged)` and calls `strategy.GenerateSignals(ranged)`. `Simulate`/`ComputeMetrics` call sites unchanged — if either needs editing, the abstraction is wrong and the task stops.
- **T8** Migration `008_backtest_strategies.up/down.sql`. The `up` is already validated (SPEC.md §2.5 — run against a stand-in table with a pre-existing row, backfill confirmed, transitional default confirmed dropped); the `down` is new, per D2.
- **T9** `internal/store/postgres_backtest_store.go`: `strategy, params` through the `INSERT`, both `SELECT` column lists, and `scanBacktest`. Mock (`service/mock/mock.go`) updated if its fixtures carry window fields.
- **T10** `internal/handler/`: verify no change is needed beyond compiling — error mapping is by sentinel (`ErrInvalidRequest` → 400) and `NewStrategy`'s failures are already `ErrInvalidRequest`, so the handler should not learn about strategies at all. Fix only what the compiler demands.

**Checkpoint B:** `make test`, `make vet`, `make test-integration` green across all three harnesses. Migration applied to the dev database; baseline re-checked (`users=20`); one MA backtest run end to end via `curl` against the real stack.

### Phase 3 — Frontend

- **T11** `frontend/src/api/types.ts`: a discriminated `BacktestParams` union (`MACrossoverParams | RSIParams | MACDParams`), `Backtest`/`RunBacktestRequest` updated to `{strategy, params}`.
- **T12** `backtest-validation.ts`: switch on the selected strategy, return the discriminated body, keep mirroring the backend's bounds without becoming the authority. Per-strategy form state stays string-typed. `backtest-validation.test.ts` extended to all three.
- **T13** `strategy-display.ts` — `describeStrategy(strategy, params)` → `"5/20 crossover"` / `"RSI(14) 30/70"` / `"MACD(12/26/9)"`, **plus the unknown-strategy fallback** (§2.8): render the raw name, never throw, never index into `undefined`. Own test file, with the unknown case asserted explicitly — this is the Step 17 `trades: null` lesson applied preemptively.
- **T14** `BacktestForm.tsx`: strategy `<select>`, conditional parameter field groups, per-strategy defaults (`5/20`, `14/30/70`, `12/26/9`) so switching always leaves a runnable form.
- **T15** `BacktestResult.tsx` and `BacktestHistoryList.tsx`: replace both inline `{short}/{long}` formats with `describeStrategy`. `MetricsGrid.tsx` and `TradeLogTable.tsx` are untouched — they read metrics and trades, neither of which changed.

**Checkpoint C:** `npm run lint`, `npm run build`, `npm run test` green.

### Phase 4 — Verification

- **T16** `services/backtesting/integration/`: all three strategies' `params` round-tripped through JSONB, asserting the reloaded run reconstructs via `NewStrategy`; plus migration `008` applied against a database holding a pre-existing `ma_crossover` row, verifying the backfill rather than trusting an empty table.
- **T17** Mutation testing on the new controls, per project convention — break each, confirm a test fails, revert. The three most worth breaking: the `maxWarmupBars` bound, the `oversold < overbought` check, and edge-only signal firing (make a strategy fire on every in-condition bar and confirm something catches it).
- **T18** Manual browser pass against the real stack and real ingested AAPL history: all three strategies run end to end; a zero-trade RSI run and a zero-trade MACD run (§2.4's accepted-but-signal-less boundary) render rather than crash; `describeStrategy`'s output checked in both the result header and the history sidebar; **a pre-Step-18 MA run reopened from history**, confirming the migration through the UI and not only in SQL. Throwaway accounts deleted afterward — `backtests` rows first, `backtests.user_id` has no `ON DELETE CASCADE`.

### Phase 5 — Close-out

- **T19** `PHASE3_CHECKLIST.md` Step 18 entry, `docs/NEXT_SESSION.md` rewrite, archive `SPEC.md`/`plan.md`/`todo.md` to `docs/archive/phase3-step18-rsi-macd-strategies/`.

---

## Risk note

The one materially new risk is SPEC.md §4's: **a subtly wrong RSI or EMA produces a plausible equity curve and plausible metrics, and every downstream test still passes.** `Simulate` and `ComputeMetrics` had self-evident invariants to assert against; `wilderRSI` and `ema` do not. D1's sequencing and T2's reference fixtures are the whole mitigation — if T2 is weakened to "the numbers look reasonable," this step's headline feature is unverified no matter how green the rest of the suite is.

Commit granularity: one commit per task, matching Steps 14–17's convention. Not committed or merged until reviewed and explicitly approved, per this project's standing git workflow.
