# Plan — Step 17: Backtesting Frontend

Source: `SPEC.md`, approved 2026-08-18. Each task is a checkpoint — implement, verify, move on.

## T1 — Wire types (`frontend/src/api/types.ts`)

Add `Backtest`, `BacktestDetail`, `TradeRecord`, `Metrics`, `RunBacktestRequest`, `BacktestsResponse`, mirroring `services/backtesting/internal/service/types.go` field-for-field, snake_case, matching the file's existing doc-comment convention (source-of-truth pointer at the top). `ProfitFactor *float64` → `profit_factor: number | null`, same pattern as `Order.filled_price`.

## T2 — API client methods (`frontend/src/api/client.ts`)

Add `runBacktest(body: RunBacktestRequest)`, `backtests()`, `backtest(id: string)` to the `api` object, matching the existing `placeOrder`/`orders`/`positions` shape exactly (all authenticated, no special-casing needed).

## T3 — `use-backtests.ts` hook

Fetch-on-mount + `refetch()`, no interval — copy `use-orders.ts`'s shape (status union, request-id race guard) with `Order`→`Backtest`, `api.orders()`→`api.backtests()`.

## T4 — `backtest-errors.ts` + test

Map the five codes from SPEC §2.6 to copy. `invalid_request` passes the backend's own message through; the other four are static strings. Unit test covers all five branches plus an unrecognized/default code.

## T5 — Form validation as a pure function + test

Extract the SPEC §2.5 bounds table into a pure `validateBacktestForm(...)` (or similar) callable from the component and from a test file without rendering anything — same pattern Step 15 used for `OrderTicket`'s `validateQuantity`, but made standalone here since there are five fields instead of one. Unit test covers each field's boundary (short_window=1 rejected/=2 accepted, long_window=short_window rejected, long_window=501 rejected/=500 accepted, start_date==end_date rejected, starting_capital=0 rejected).

## T6 — `MetricsGrid.tsx`

Five stats (`total_return_pct`, `sharpe_ratio`, `max_drawdown_pct`, `win_rate_pct`, `profit_factor`), reusing `PortfolioSummary.tsx`'s `Stat` pattern. `profit_factor === null` renders "—" + "no losing trades" note (§2.7). Sign coloring for return/Sharpe; `max_drawdown_pct` neutral/down-leaning only, never up-colored.

## T7 — `TradeLogTable.tsx`

Renders `BacktestDetail.Trades`, reusing `formatPrice`/`formatQuantity` from `frontend/src/format.ts` and the up/down/em-dash convention `OrdersTable.tsx` already uses for nullable P/L.

## T8 — `BacktestResult.tsx`

Composes `MetricsGrid` + `TradeLogTable` + the run's own parameters (symbol, windows, date range, starting capital) for whichever `BacktestDetail` is currently selected.

## T9 — `BacktestForm.tsx`

Symbol/short_window/long_window/start_date/end_date/starting_capital inputs, client-side validation from T5, submit → `api.runBacktest`, on success sets the result to the response body directly (§2.3 — no extra `GET`) and calls `useBacktests`'s `refetch()` (§2.4). Error handling via T4's mapping. Direct submit, no confirmation modal (consistent with Step 15 §2.8's precedent, though not separately re-litigated in this spec).

## T10 — `BacktestHistoryList.tsx`

Renders `useBacktests()`'s list, newest-first (already ordered by the backend), each row a button: symbol, `short_window/long_window`, date range, `total_return_pct` (colored). Click → if it's the same id as the currently-held detail, no-op; otherwise `api.backtest(id)` and set as selected result.

## T11 — Dashboard integration

Add `'backtest'` to `Tab`, add to `TABS`, render `BacktestForm` + `BacktestHistoryList` + (once a result exists) `BacktestResult` in the tab's content area. `OrderTicket` untouched — stays pinned regardless of tab (§2.1).

## T12 — Manual verification

Full stack up (`make run-backtesting` included). Run a valid backtest end-to-end; a zero-losing-trades case (`profit_factor` → "—"); each of `invalid_request`, `symbol_unavailable`, `date_range_unavailable`; reopen a past run from history and confirm it matches; confirm a second account's history list is empty (cross-user scoping, no leakage).

## T13 — Lint/build gate

`npm run lint` and `npm run build` clean. `npm run test` green (T4/T5's new tests plus everything from Step 15 still passing).

## T14 — Docs close-out

Archive `SPEC.md`/`tasks/plan.md`/`tasks/todo.md` to `docs/archive/phase3-step17-backtesting-frontend/`, update `PHASE3_CHECKLIST.md` with Step 17's entry, rewrite `docs/NEXT_SESSION.md` for the close of Step 17.
