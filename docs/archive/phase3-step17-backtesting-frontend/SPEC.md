# SPEC — Backtesting Frontend: Strategy Form, Results & Run History (Step 17)

Status: **Approved 2026-08-18.** All recommendations in §2 accepted as written; §"Open questions" confirms none are blocking. Implementation is unblocked — not started.
Scope: `frontend/src/` only — a new `frontend/src/backtesting/` directory, additions to `frontend/src/api/{client.ts,types.ts}`, an edit to `frontend/src/market/Dashboard.tsx` (a fifth tab). No backend, migration, or gateway changes — Step 16 shipped the full `/backtests/*` API already.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase2-step16-backtesting-engine/`.

---

## 1. Objective

Step 16 shipped a complete, tested backtesting API — `POST/GET /backtests`, `GET /backtests/{id}` — proxied through the gateway, and deliberately built no UI against it (`docs/archive/phase2-step16-backtesting-engine/SPEC.md` §1 non-goals: "frontend UI"). `docs/NEXT_SESSION.md` already scoped this step: "a strategy-config form, a results view (metrics + trade log), and a run history list... the same shape of work Step 15 already proved out against `/trading/*`." Nothing has changed since that scoping — this spec is that step.

**Objective:** wire all three `/backtests/*` endpoints into the existing dashboard — a form to configure and run a moving-average-crossover backtest against a symbol and date range, a results view showing the five metrics plus the simulated trade log, and a history list of past runs a user can reopen — using the same conventions Steps 8/13/15 already established: a typed wire layer in `api/`, status-union hooks, Tailwind design tokens, WHY-only comments, `vitest` for logic-bearing units.

**Non-goals:**
- **RSI/MACD or any second strategy.** The backend only implements moving-average crossover (`services/backtesting/internal/service/strategy.go`); there is nothing else for a strategy picker to offer. `docs/NEXT_SESSION.md` already ranks this below the frontend for the same reason: "a second and third strategy behind a UI nobody can drive yet doesn't add resume-visible value."
- **Multi-symbol backtests.** `RunBacktestRequest.Symbol` is singular; the backend has no concept of a portfolio-level run.
- **Editing or deleting a saved backtest.** There is no `PATCH`/`DELETE /backtests/{id}` — a run is an immutable record of what happened, like an order.
- **An equity curve chart.** `SPEC.md §2.6` of Step 16 states plainly: "no stored equity curve." `SimulationResult.EquityCurve` never leaves the service layer — `Backtest`/`BacktestDetail` carry only the five summary metrics and the trade log. Charting a curve that was never persisted is out of scope until the backend stores one.
- **Live/paper-trading a backtested strategy.** A backtest result has no relationship to `/trading/*` — running a strategy here does not place real orders. Nothing suggests otherwise in the UI.
- **A native mobile layout.** Responsive within the existing Tailwind breakpoint conventions (`sm:`/`lg:`), not a redesign.

---

## 2. Design decisions

### 2.1 A fifth tab, not a separate page

`Dashboard.tsx` already drives four views (Chart/Positions/Orders/Portfolio) as tab state (Step 15 §2.1). **Recommendation:** add `'backtest'` as a fifth `Tab` value rather than introducing routing. The reasoning that closed Step 15 §2.1 hasn't changed — no stated need for a deep link or browser-back between views — and a fifth string in an existing union costs nothing a router wouldn't also cost in dependency weight.

Unlike the four trading tabs, the backtest tab is **not symbol-scoped to `selected`** — a backtest's symbol is a field in its own form, independent of whatever's currently charted on the left. `OrderTicket` stays pinned in the right column across all five tabs as it does today; it has nothing to do with backtesting and nothing here changes it.

### 2.2 One tab, three sub-states: form → running → result

**(a)** Three separate tabs (Configure / Results / History).
**(b)** One `'backtest'` tab whose content is internal state: a form by default, replaced by a loading state while `POST /backtests` is in flight, replaced by the result once it returns — plus a persistent, always-visible history list a user can click to reload any past result (including the one just run) into the same result view.

**Recommendation: (b).** `POST /backtests` is synchronous end-to-end (Step 16 §2.7: "there is no separate GET needed right after a POST to see what just happened") — there is no async job to track across a tab switch, so splitting "configure" from "results" into separate tabs would just make a user click twice for something that already completes in one request. History needs to be visible at the same time as the form (so a user can compare a new run's parameters against an old one, or reopen a past result without re-running it) rather than behind a third tab — it lives in a persistent sidebar within the backtest tab's content, not as a fourth top-level tab.

### 2.3 Data fetching — history on mount, no polling; run result held as local state

`GET /backtests` fetches the caller's run history once on mount, exposing `refetch()` — the same shape as Step 15's `useOrders` (§2.3 there: "nothing outside this browser session can create an order against this account, so there's no clock-driven reason for it to refresh on its own"). The identical reasoning applies here: nothing creates a backtest except this user submitting the form, so a `useBacktests()` hook with no interval mirrors `useOrders` exactly.

The currently-displayed result (whichever run is selected — the one just submitted, or one reopened from history) is **not** re-fetched from `GET /backtests/{id}` when it's the one just returned by `POST /backtests` — `RunBacktest`'s response body already **is** a `BacktestDetail`, trade log included (Step 16 §2.7: "returns the full result, including its trade log"). Reopening an *older* run from the history list does call `GET /backtests/{id}`, since only the list-shaped summary (`Backtest`, no `Trades` field) is held for those.

### 2.4 Explicit refetch of history after a successful run

After `POST /backtests` returns, the new run is shown as the selected result immediately (from the response body, per §2.3 — no extra round trip needed to display it) **and** `useBacktests`'s `refetch()` is called so the history list picks up the new entry without a page reload. Same "explicit refetch after a mutation, not wait for a poll that doesn't exist" pattern Step 15 §2.4 established for orders.

### 2.5 Client-side validation mirrors the backend's bounds, never replaces them

The form validates what `validateRequest` (`services/backtesting/internal/service/backtest.go`) already enforces, before ever calling the API:

| Field | Rule | Source |
|---|---|---|
| `symbol` | non-empty | `validateRequest`: `symbol == ""` → `invalid_request` |
| `short_window` | integer ≥ 2 | `minShortWindow = 2` |
| `long_window` | integer, `> short_window`, and `≤ 500` | `req.LongWindow <= req.ShortWindow`, `maxLongWindow = 500` |
| `start_date` / `end_date` | both required, `start_date` strictly before `end_date` | `dateLayout = "2006-01-02"`, `!start.Before(end)` |
| `starting_capital` | > 0 | `req.StartingCapital <= 0` |

Same posture as Step 15 §2.7: this catches the common mistake before a round trip, but the backend stays the sole authority. Two rejections are **not** pre-checkable client-side and must be handled as server responses, not form errors: `symbol_unavailable` (the symbol has no ingested history at all) and `date_range_unavailable` (the symbol has history, but none inside the requested dates) — both depend on data the frontend doesn't have (what's actually been ingested), the same reason Step 15 §2.7 didn't pre-check symbol availability for orders either. `upstream_unavailable` (market-data is down) is likewise a runtime response, not a validation state.

### 2.6 Error mapping — four new codes, distinct copy for each

`writeServiceError` (`services/backtesting/internal/handler/backtest.go`) emits five error codes; unlike Step 15's rejection reasons these arrive as the request's own HTTP error, not a `200` body field, so they're handled in the form's submit `catch` exactly like `OrderTicket`'s `invalid_request`/rejection-reason split (Step 15 §2.6) — just with backtesting's own codes instead of trading's:

| Code | Status | Copy shown |
|---|---|---|
| `invalid_request` | 400 | The backend's own message (`err.Error()` from `validateRequest` is already a specific, user-legible string — e.g. "long_window must be at most 500" — unlike trading's generic `invalid_request`, so it's shown directly rather than mapped to one static sentence) |
| `symbol_unavailable` | 400 | "No historical data is available for that symbol." |
| `date_range_unavailable` | 400 | "No historical data is available in the requested date range." |
| `upstream_unavailable` | 502 | "Market data is unavailable right now. The backtest was not run." |
| `not_found` | 404 | (Only reachable from `GET /backtests/{id}` when reopening a history entry — e.g. deleted between page load and click, which cannot happen today but the branch must not silently no-op.) "That backtest could not be found." |

**Why `invalid_request` differs from Step 15's treatment:** trading-engine's `invalid_request` message is generic on purpose (`services/trading-engine`'s own validation errors aren't field-specific in the same way), so Step 15 mapped it to one static sentence. `backtesting`'s `validateRequest` already produces a specific, correct, user-safe message per field (confirmed by reading every `fmt.Errorf` in `validateRequest` — none leak anything beyond the field name and the bound it violated). Showing it directly is more useful than replacing it with a generic string, and this doesn't set a new precedent for other services' `invalid_request` handling — just backtesting's, where the message quality happens to differ.

### 2.7 Metrics rendering: `profit_factor`'s null case, and Sharpe/return sign coloring

`Metrics.ProfitFactor` is `*float64`, null when there are no losing trades (Step 16 §2.5) — the same "null is not zero" rule Step 15 §2.5 already applies to `latest_price`. **Recommendation:** render `profit_factor: null` as "—" with a tooltip-free inline note ("no losing trades"), never as `0` or `∞`; this is a direct reuse of the null-rendering pattern already established, not a new decision.

`total_return_pct` and `sharpe_ratio` get the same `text-up`/`text-down` sign-based coloring `PortfolioSummary.tsx` already applies to `total_unrealized_pl` (positive/negative/zero → up/down/ink). `max_drawdown_pct` is always shown in a neutral or `text-down`-leaning tone regardless of sign, since the backend always returns it as a positive percentage (`maxDrawdownPct` in `metrics.go`: "positive percentage") — there's no positive-drawdown case to color green.

### 2.8 Trade log table — reuses `formatPrice`/`formatQuantity`, adds nothing new

`BacktestDetail.Trades` is `[]TradeRecord` — `side`, `bar_timestamp`, `price`, `quantity`, `realized_pl` (null on buys). This is structurally identical to `Order`/`Trade`'s shape already rendered by `OrdersTable.tsx`: a side badge, a price and quantity formatted with the existing `formatPrice`/`formatQuantity` from `frontend/src/format.ts`, and a P/L column using the same up/down/em-dash-when-null convention `OrdersTable`/`PositionsTable` already use for `realized_pl`/`unrealized_pl`. No new formatting logic — a new `TradeLogTable.tsx` component, but not a new pattern.

### 2.9 History list — compact rows, no pagination

`GET /backtests` returns every run for the caller, newest-first (Step 16: `ORDER BY created_at DESC`), with no limit or pagination parameter on the endpoint. **Recommendation:** render the full list as-is — a few dozen runs is a realistic ceiling for one user in this phase, and adding client-side pagination or a "load more" control for a list the backend itself doesn't paginate would be solving a problem that doesn't exist yet. Each row shows symbol, window pair (`5/20`), date range, and `total_return_pct` (colored), and is a button that loads that run into the result view (§2.3).

### 2.10 Frontend test coverage — continue what Step 15 established

Step 15 §2.10 introduced `vitest` scoped to logic-bearing units (already in `frontend/package.json` — no new dependency this step). **Recommendation:** apply the same scoping here. This step's logic-bearing units are the form validation table in §2.5 (as a pure function, not asserted through rendering) and the error-code-to-copy mapping in §2.6 — the direct backtesting analogues of Step 15's `rejection-reason.test.ts`. The null-`profit_factor` rendering rule in §2.7 is a smaller, more local decision than Step 15's null-price rule (one field, one component) and can be covered by the same pure-function pattern if it ends up factored out, or left to manual verification if it doesn't — judgment call at implementation time, not a decision worth gating the spec on.

---

## 3. Project structure

```
frontend/src/
  backtesting/
    BacktestForm.tsx          # symbol/windows/dates/capital form, client-side validation (§2.5)
    BacktestResult.tsx        # metrics grid + trade log for the selected run
    MetricsGrid.tsx           # five metrics, null-profit_factor and sign-coloring (§2.7)
    TradeLogTable.tsx         # BacktestDetail.Trades, reusing format.ts (§2.8)
    BacktestHistoryList.tsx   # GET /backtests rows, click to reopen (§2.9)
    use-backtests.ts          # fetches GET /backtests, exposes refetch() (§2.3)
    backtest-errors.ts        # error code -> user-facing copy (§2.6)
  market/
    Dashboard.tsx              # +'backtest' tab (§2.1)
  api/
    types.ts                   # +Backtest, BacktestDetail, TradeRecord, Metrics, RunBacktestRequest, BacktestsResponse
    client.ts                  # +runBacktest, backtests, backtest(id)
```

Plus, per §2.10: `frontend/src/backtesting/backtest-validation.test.ts` and `frontend/src/backtesting/backtest-errors.test.ts`.

---

## 4. Configuration

No new environment variables — `/backtests/*` is already proxied through the gateway on the existing `VITE_API_BASE_URL` origin, same as `/auth/*`, `/market-data/*`, `/trading/*`. No new frontend dependencies — `vitest`/`@testing-library/react`/`jsdom` are already in `frontend/package.json` from Step 15.

---

## 5. Testing strategy

- Unit tests (`vitest`, no DOM) for the form-validation table (§2.5) and the error-code-to-copy mapping (§2.6), mirroring Step 15's `rejection-reason.test.ts` shape.
- **Manual verification** against the real stack (`make run-backtesting` + the rest of `docs/NEXT_SESSION.md`'s restart sequence): run a valid AAPL backtest end-to-end and confirm the metrics and trade log render, including a run with zero losing trades (`profit_factor` renders as "—", not `0`); trigger each of `invalid_request` (e.g. `long_window ≤ short_window`), `symbol_unavailable` (an un-ingested symbol), `date_range_unavailable` (a valid symbol, a date range before any ingested data); reopen a past run from history and confirm it matches what was originally shown; confirm history is scoped to the logged-in user (a second account sees an empty list, matching Step 16's cross-user 404 test).
- **`npm run lint`** (`oxlint`) and **`npm run build`** (`tsc -b && vite build`) clean, matching the bar every prior frontend step has held.

---

## 6. Code style

Continue what `frontend/src/` already established: WHY-only comments, wire types in `api/types.ts` mirroring the Go `json` tags field-for-field and snake_case (source of truth: `services/backtesting/internal/service/types.go`), status-union hook state, Tailwind semantic tokens (`text-up`/`text-down` for return/Sharpe sign, `text-ink-subtle` for the null-`profit_factor` dash, `tabular font-mono` for every numeric column).

---

## 7. Commands

```bash
cd frontend && npm run dev      # :5173, same as today
cd frontend && npm run build
cd frontend && npm run lint
cd frontend && npm run test
```

---

## Boundaries

- **Always:** treat `profit_factor: null` as a display state, not a number to format (§2.7); render `total_return_pct`/`sharpe_ratio` sign-colored but `max_drawdown_pct` neutral (§2.7); run `npm run lint` and `npm run build` clean before each task's commit.
- **Ask first:** any new frontend dependency; any change to `api/client.ts`'s request/refresh machinery outside of adding the three backtesting methods; introducing `react-router-dom` (§2.1 recommends against it, consistent with Step 15).
- **Never:** build an equity-curve chart — the backend stores no equity curve to chart (§1 non-goals); imply a backtest result places or relates to a real order (§1 non-goals); add a second strategy option to the form — the backend only implements one (§1 non-goals).

---

## Open questions

None identified — this step's shape (form → synchronous result → history list) and every field/error mapping are fully determined by the API Step 16 already shipped, unlike Step 16 itself which had genuine scope-boundary calls to make (RSI/MACD, async job queue, etc.) against an undefined "Major System" backlog item. Recommendations above are included for the record, per this project's working agreement, but none block implementation the way Step 16's three open questions did.

---

## 8. Implementation

Not started. `tasks/plan.md` and `tasks/todo.md` to be created after this spec is reviewed, per the gated workflow (`agents.md`: spec reviewed → plan → checkpoints).
