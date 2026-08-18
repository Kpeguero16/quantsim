# SPEC — Backtesting Engine MVP: Moving-Average Crossover (Step 16)

Status: **Approved 2026-08-18.** All open questions in §3 resolved as recommended. Implementation is unblocked — not started.
Scope: new service `services/backtesting` (`cmd/server`, `internal/service`, `internal/store`, `internal/client`, `internal/handler`, `integration`, `go.mod`), `go.work`, a new migration (`007_...`), `services/gateway` (router, `cmd/server/main.go`), `Makefile`, `.env.example`. No frontend changes — see Non-goals.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase2-step15-trading-frontend/`.

---

## 1. Objective

`agents.md` §3, "Strategy Backtesting Engine (Major System)": evaluate trading strategies against historical market data, flow `Historical Data → Strategy Engine → Trade Simulator → Metrics Engine`. The data side has existed since Step 6 — `historical_prices` holds ~2 years of daily bars for 7 symbols (`AAPL`, `AMZN`, `GOOGL`, `MSFT`, `QQQ`, `SPY`, `TSLA`), exposed at `GET /market-data/history/{symbol}`. Nothing downstream of it exists: `services/backtesting` is an empty `go.mod` stub, and `agents.md`'s three example strategies (moving-average crossover, RSI, MACD) are unbuilt.

**Objective:** `POST /backtests` runs one strategy against one symbol's historical daily bars over a date range, simulates the resulting trades against a starting cash balance, and returns the five metrics `agents.md` §3 names: total return %, Sharpe ratio, max drawdown, win rate, profit factor. The run and its trade log persist, retrievable via `GET /backtests` and `GET /backtests/{id}`.

**Why moving-average crossover only.** `agents.md` lists three example strategies and explicitly separates "custom strategy configuration" and "script-based strategies" into **Stretch Features** — this step builds the one strategy needed to prove the whole pipeline (data → signals → simulated fills → equity curve → metrics) end to end, not all three. RSI and MACD are the same shape of work repeated, not new design; adding them is mechanical once this pipeline exists and is better sequenced as a fast follow-up than bundled into the step that has to get the pipeline's design right.

**Non-goals:**
- **RSI and MACD strategies.** Same reasoning as above — the pipeline this step builds is the hard part; a second and third strategy are additive.
- **Custom or script-based strategy configuration.** `agents.md`'s own Stretch Features list. Sandboxing arbitrary user scripts is a security-hardening project in its own right, not an MVP concern.
- **Multi-symbol / portfolio-level backtests.** One symbol per run, matching how `agents.md`'s processing flow and metrics are all singular ("a strategy," "an equity curve"). Portfolio backtesting is a materially different simulator (correlation, position sizing across symbols) and its own step.
- **Intraday timeframes.** `historical_prices` only has `1Day` bars ingested (Step 6's watchlist); this step reads what's there rather than adding new ingestion.
- **Frontend UI.** Mirrors the Step 14 → Step 15 split: ship the backend, verify it directly, build the UI as its own step once there's a real API to build against.
- **Async job queue / progress streaming.** A single-symbol, ~500-bar backtest is a sub-second computation. `POST /backtests` runs synchronously and returns the result in the response, the same way `POST /trading/orders` fills synchronously. Revisit only if a later strategy or multi-symbol backtest makes that untrue.
- **AI-generated insights on the results.** `agents.md` §4 is its own major system (Phase 4), explicitly downstream of this one.

---

## 2. Design decisions

### 2.1 New service scaffolding, following the existing pattern exactly

`services/backtesting` gets `cmd/server/main.go`, `internal/service`, `internal/store`, `internal/client`, `internal/handler` — the same layering `trading-engine` established in Step 14. Added to `go.work`.

### 2.2 Historical data source: `market-data`'s existing `GET /market-data/history/{symbol}` over HTTP, fetched at max limit and date-sliced locally

Two options:

**(a)** Call `market-data`'s existing endpoint with `limit=2000` (its own `MaxHistoryLimit`, comfortably above the ~501 bars any symbol currently has), then filter the returned bars to the requested `[start, end]` date range inside `backtesting`.
**(b)** Extend `market-data`'s `History` handler to accept `start`/`end` query params and do the date filtering server-side.

**Recommendation: (a).** It touches zero lines of `market-data` — this step is additive only, consistent with `services/trading-engine/internal/client`'s existing pattern for reaching `market-data` (a small HTTP client, direct service-to-service call bypassing the gateway, per Step 14 §2.2's own reasoning about JWTs having no meaning between internal services). Date-range filtering on an in-memory slice of at most 2000 bars costs nothing. (b) is the more "correct" long-term shape once datasets are large enough that shipping the whole history over the wire is wasteful, but nothing here is close to that scale — worth revisiting only if a later step ingests intraday data or many more years of history.

### 2.3 Strategy: simple moving-average crossover, long-only, fully-invested single position

- Two parameters: `short_window`, `long_window` (bar counts, e.g. 10/50), `short_window < long_window` enforced at validation.
- Signal on bar `i` (`i >= long_window`): `short_ma > long_ma` and it wasn't on the previous bar → **golden cross**, buy. `short_ma < long_ma` and it wasn't on the previous bar → **death cross**, sell. Both are simple arithmetic means over the trailing window, computed once per bar in a single pass.
- **Position sizing: all-in.** A buy spends the entire current cash balance on the position (long-only, no leverage — mirrors `trading-engine`'s own long-only constraint from Step 14). A sell liquidates the entire position back to cash. No partial sizing, no pyramiding — the simplest rule that still produces a real equity curve and real trades to compute metrics from.
- At most one open position at a time, one symbol. A golden cross while already in a position is a no-op (already fully invested); a death cross while flat is a no-op (nothing to sell).

### 2.4 Fill price: next bar's **open**, not the signal bar's close — avoiding lookahead bias

A signal computed from bar `i`'s close (the MA crossover) and then filled at bar `i`'s own close price is trading on information not actually available until the bar closes — a well-known backtesting correctness bug (lookahead bias) that inflates returns. **Recommendation:** the signal is computed from bars `[0..i]`, and if it fires, the simulated fill executes at bar `i+1`'s open. This costs the last bar in the range (no fill can be simulated past the final bar) and is the standard, defensible choice — consistent with this project's existing money-integrity posture (`trading-engine`'s SPEC.md §2.3, "fail closed on price-fetch... a direct integrity violation").

### 2.5 Metrics: all five named in `agents.md` §3, computed from the run's equity curve and trade log

- **Total return %:** `(final_equity - starting_capital) / starting_capital * 100`.
- **Sharpe ratio:** annualized, from daily returns of the equity curve: `mean(daily_returns) / stdev(daily_returns) * sqrt(252)`. Zero-variance edge case (no trades ever executed, equity curve flat) returns `0`, not `NaN` or a divide-by-zero — mirrors this project's existing null-over-lying-number posture (`position-display.ts`'s em-dash rule, Step 15) applied to a numeric edge case instead of a UI one.
- **Max drawdown %:** largest peak-to-trough decline in the equity curve, `(trough - peak) / peak * 100`.
- **Win rate:** `winning_trades / total_closed_trades * 100`. A "closed trade" is a completed buy→sell round trip; an open position at the end of the range (never sold) is excluded from win rate and profit factor, not counted as a loss.
- **Profit factor:** `sum(gains from winning trades) / sum(abs(losses from losing trades))`. No losing trades → the ratio is undefined; return `null` (not `0`, not `Infinity`) — same "unpriceable must never render as a misleading number" rule Step 15 already established for the frontend, applied here at the API boundary instead.

### 2.6 Persistence: two new tables, `backtests` and `backtest_trades` — same split as `orders`/`trades`

Migration `007_backtests.up.sql`:

- `backtests`: `id`, `user_id` (FK → `users`), `symbol`, `short_window`, `long_window`, `start_date`, `end_date`, `starting_capital`, `final_equity`, `total_return_pct`, `sharpe_ratio`, `max_drawdown_pct`, `win_rate_pct`, `profit_factor` (nullable, §2.5), `created_at`. One row per run — the parameters that produced it and the five summary metrics, so `GET /backtests` is a cheap list query with no joins.
- `backtest_trades`: `id`, `backtest_id` (FK → `backtests`), `side` ('buy'/'sell'), `bar_timestamp`, `price`, `quantity`, `realized_pl` (nullable, sell rows only) — the simulated trade log for one run, mirroring `trades`' own shape from Step 14. Fetched only via `GET /backtests/{id}`, never listed independently — there's no cross-backtest trade view in this step's scope.

No `equity curve` table. The full daily equity series is useful for a future frontend chart but is not one of `agents.md`'s five named metrics and has no consumer yet in this step's non-goals (no UI). Storing metrics without the curve keeps `backtests` a cheap list to page through; recomputing or storing the curve is a fast follow when the frontend step needs it to chart.

### 2.7 API surface, auth, and ownership

- `POST /backtests` — body: `{symbol, short_window, long_window, start_date, end_date, starting_capital}`. Runs synchronously (§ Non-goals), returns the full `backtests` row plus its `backtest_trades`.
- `GET /backtests` — the authenticated user's own runs, newest first. No cross-user visibility — mirrors `trading-engine`'s account-scoped reads.
- `GET /backtests/{id}` — one run's full detail including its trade log. 404s (not 403s) if the id doesn't belong to the caller, matching `auth`/`trading-engine`'s existing "don't confirm existence to a non-owner" posture.
- Gated behind the gateway's `RequireAuth` group, same as `/trading/*` and `/market-data/*`. New `backtestingProxy` wired into `NewRouter` alongside `tradingProxy`, `r.Handle("/backtests/*", backtestingProxy)`.
- `backtesting` revalidates the caller's JWT itself rather than trusting the gateway's injected `X-User-ID` — this is *not* what Step 14 does for reads either; `trading-engine`'s router applies `pkgauth.RequireAuth` to the whole `/trading` group and never trusts the header (its own router.go is explicit: "each service checks for itself rather than trusting a proxy header"). Same pattern here: `pkgauth.RequireAuth(jwtSecret)` on the `/backtests` group, `pkgauth.UserIDFromContext` in the handler.

### 2.8 Validation

- `short_window >= 2`, `long_window > short_window`, both bounded above (e.g. `<= 500`, comfortably under any symbol's available bar count) — mirrors `trading-engine`'s existing pattern of bounding inputs that flow into a loop or a query (Step 14's `maxSymbolLength`).
- `start_date < end_date`, both within the symbol's actually-ingested range — a request outside available data is a `400 invalid_request`, not a request that silently runs on fewer bars than asked for.
- `starting_capital > 0`.
- Symbol must be one `market-data` actually has history for — checking this means calling `GET /market-data/history/{symbol}` and getting a non-empty result back before running anything; an unfetchable or unknown symbol is a `400`, not a `502`, since this is a validation failure discoverable before any simulation starts (different from `trading-engine`'s `upstream_unavailable`, which is a live price call that can genuinely fail transiently).

---

## 3. Decisions (resolved 2026-08-18, all as recommended)

1. **Window bound: `long_window <= 500`,** fixed rather than tied dynamically to the request's date range. Matches the ~501 bars currently available per symbol; simpler to validate up front (before any history fetch) than a bound computed from `[start_date, end_date]`, and revisits the moment a symbol's ingested range grows meaningfully past this.
2. **`starting_capital` is always required, no default.** Silently picking a number (e.g. matching `accounts.balance`) would blur "this backtest's hypothetical capital" with "my actual paper-trading balance" — unrelated by design.
3. No further changes to §2.

---

## 4. Verification plan

Same posture as Steps 14–15: unit tests on the pure computation (MA crossover signal generation, the five metrics, the lookahead-avoidance fill timing) with mutation testing to prove they're a real safety net; integration tests against real Postgres for the store; a manual adversarial pass once the endpoint is live (a strategy that never crosses → zero trades → the zero-variance Sharpe/profit-factor edge cases actually exercised, not just unit-tested in isolation; a date range with no ingested data → clean 400; a `long_window` at the data boundary).
