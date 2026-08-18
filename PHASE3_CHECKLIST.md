# QuantSim Phase 3 — Backtesting Engine Checklist

Phase 2 is complete (`PHASE2_CHECKLIST.md`). Phase 3 delivers the
backtesting engine (`agents.md`'s roadmap: historical ingestion, a strategy
simulator, metrics dashboards) — the second "Major System" in `agents.md`
§3, and the bigger resume-relevant milestone after two UI-focused steps in
a row.

Historical ingestion already existed going into this phase (Step 6's
`market-data` watchlist, ~2 years of daily bars for seven symbols), so
Phase 3 opens directly with the strategy simulator rather than needing its
own ingestion step.

---

## Step 16: Backtesting Engine MVP

The first Phase 3 system: a new `services/backtesting`, running a
moving-average-crossover strategy against `market-data`'s existing
historical daily bars (~2 years, seven symbols, ingested since Step 6).
`POST /backtests` fetches history, generates crossover signals, simulates
trades with next-bar-open fills (no lookahead bias), computes the five
`agents.md` §3 metrics, and persists the run. `GET /backtests` and
`GET /backtests/{id}` read it back, scoped to the caller. Backend only —
mirrors the Step 14 → Step 15 split; the frontend is a later step.

- [x] Spec drafted and reviewed — recommended MVP scope (one strategy, one
      symbol per run, synchronous execution) accepted, two open questions
      (window bound, `starting_capital` default) resolved as recommended
      (`SPEC.md` §3)
- [x] Plan (`tasks/plan.md`) — 14 tasks across 5 phases
- [x] Migration `007_backtests` — `backtests` (params + 5 metrics,
      `profit_factor` nullable) and `backtest_trades` (FK cascade), mirroring
      `orders`/`trades`' own split from migration 006
- [x] `GenerateSignals` — MA crossover, fires only on the crossing bar itself
- [x] `Simulate` — next-bar-open fills, all-in long-only sizing, one position
- [x] `ComputeMetrics` — total return, Sharpe, max drawdown, win rate,
      profit factor, with the two named null/zero edge cases (§2.5)
- [x] `MarketDataClient.History` — reuses `market-data`'s existing
      `GET /market-data/history/{symbol}?limit=2000` unchanged; maps an
      empty `Bars` response to `ErrSymbolUnavailable` itself, since
      market-data's own History endpoint never 404s
- [x] `PostgresBacktestStore` — `SaveBacktest`/`ListBacktests`/`GetBacktest`,
      ownership enforced in the `WHERE` clause so a non-owner and a
      nonexistent id are the identical `ErrNotFound`
- [x] `RunBacktest` orchestration, handlers, router — `backtesting`
      revalidates the caller's JWT itself, the same posture `trading-engine`
      set in Step 14, not the gateway's `X-User-ID`
- [x] Gateway: `backtestingProxy`, `/backtests/*` **and** bare `/backtests`
      (chi's wildcard alone does not match the collection route with no
      trailing segment — see below)
- [x] `services/backtesting/integration/` — the harness's third copy; see
      "The trigger that fired" below
- [x] Mutation-tested the pure computation layer — see below
- [x] Manual adversarial pass against the real stack and real ingested
      AAPL history — see below

**Completed 2026-08-18.** Spec, plan and todo archived to
`docs/archive/phase2-step16-backtesting-engine/`.

### A real routing bug the build caught before it shipped

`trading-engine` has no bare `/trading` endpoint — every route is a
sub-path (`/trading/orders`, `/trading/positions`), so the gateway's
`r.Handle("/trading/*", tradingProxy)` was always sufficient. `backtesting`
does have one: `POST`/`GET /backtests` **is** the collection route, with no
trailing segment. Wiring it the same way (`/backtests/*` only) meant a
request to exactly `/backtests` never matched chi's wildcard, and both the
gateway's own routing test and a live `curl` caught it as a 401 — the
request fell through to the "no auth on an unmatched path" case — before
the fix (`r.Handle("/backtests", backtestingProxy)` alongside the wildcard)
went in.

### The trigger that fired: a third copy of the integration harness

`docs/TESTING_STRUCTURE.md` §6a named "a third service needing it" as the
point to extract the auth/trading-engine Postgres integration harness to
`pkg/testutil/`, and had guessed `market-data` would be the third.
`backtesting`'s store needed the same real-database treatment first, so it
became the third copy instead. The extraction itself was deliberately
**not** done in this step — it is cross-cutting (touches already-shipped
test files in two other services) and belongs in its own reviewed change.
Recorded as `docs/deferred-tuning.md` §11, with the trigger for when to
actually do it.

### Verification

**Mutation-tested the three pure computation files** — `GenerateSignals`,
`Simulate`, `ComputeMetrics` — the highest-value, highest-risk-of-a-quiet-
wrong-number surface in this step, same posture as Steps 14–15:

- Removed the `haveState` guard in `GenerateSignals` (fire a signal on the
  very first eligible bar even with no established side yet). **Not caught**
  by the existing crossover test, because that series happens to start tied.
  This is a real gap the mutation found, not a false negative to shrug off:
  added `TestGenerateSignals_AlreadyAboveOnTheFirstEligibleBarFiresNoSignal`
  (a monotonically-increasing series, already above on bar one), confirmed
  it passes clean code, then re-ran the same mutation — caught.
- Changed `Simulate` to fill on the signal bar's own open instead of the
  next bar's — reintroducing the exact lookahead-bias bug §2.4 exists to
  prevent. Caught immediately: 3 of 5 `Simulate` tests failed, with the
  price/quantity/P&L numbers in the failure output visibly wrong.
- Removed the `grossLoss == 0` guard in `ComputeMetrics`'s profit-factor
  calculation. Caught immediately, with the failure printing `+Inf` — the
  exact value this guard exists to keep out of the response.

All three reverted afterward with a clean diff (confirmed via `diff` against
a pre-mutation backup of each file).

**Manual adversarial pass**, full stack running (auth, market-data, gateway,
backtesting, all on their normal ports — no frontend yet), one throwaway
account (`step16review`, plus a second `step16stranger` for the ownership
check), against real ingested AAPL history:

- **Golden path**: a real 10/50-day crossover backtest over AAPL's full
  ~18-month range produced 4 closed round trips (3 wins, 1 loss, one open
  position correctly excluded from both), `win_rate_pct: 75`, a computed
  `profit_factor`, and a trade log with alternating buy/sell timestamps —
  end-to-end proof the pipeline runs on data this project actually has, not
  just synthetic test bars
- **`GET /backtests`** listed the run with Postgres's `NUMERIC(20,4)`
  rounding visible (`13830.845209257237` on the POST response vs.
  `13830.8452` on the list read) — expected, not a bug
- **Four rejection paths**: unknown symbol → `400 symbol_unavailable`; a
  date range with no ingested data → `400 date_range_unavailable`;
  `long_window` over the fixed 500 bound → `400 invalid_request`;
  `long_window` exceeding the bars actually in a narrow date range →
  `400 invalid_request` naming both numbers
  in the message
- **No token** → `401`, matching `/trading/*`'s existing gateway coverage
- **Malformed id** in `GET /backtests/{id}` → `400 invalid_request`, not a
  500 from a failed UUID parse
- **Cross-user ownership**: `step16stranger` requesting `step16review`'s
  real backtest id → `404`, not `403` — confirms §2.7's "indistinguishable
  from nonexistent" live, not just in the store's own integration test

Full suite green at the end: `make test`, `make vet`, `make test-integration`
(all three services' harnesses, 12 new backtesting-store tests included) all
pass. Dev database returned to `users=20, accounts=20, orders=0, trades=0,
positions=0, backtests=0, backtest_trades=0` after both throwaway accounts
were deleted.

---

## Step 17: Backtesting Frontend

Wired all three `/backtests/*` endpoints Step 16 shipped into the existing
dashboard — mirrors the Step 14 → 15 split completing itself a second time.
A fifth `Dashboard.tsx` tab (`'backtest'`), not a new page: a strategy-config
form (symbol, MA windows, date range, starting capital) with client-side
validation mirroring `validateRequest`'s exact bounds, a synchronous result
view (five metrics + trade log) built from `POST /backtests`'s own response
body — no extra round trip — and a persistent run-history sidebar that
reopens any past run via `GET /backtests/{id}`. No new frontend
dependencies; `vitest` was already in place from Step 15.

- [x] Spec drafted and reviewed — every field/error mapping was fully
      determined by Step 16's already-shipped API, so this spec carried no
      blocking open questions, unlike Step 16's three (`SPEC.md` "Open
      questions")
- [x] Plan (`tasks/plan.md`) — 14 tasks
- [x] Wire types (`api/types.ts`) — `Backtest`, `BacktestDetail`,
      `TradeRecord`, `Metrics`, mirroring
      `services/backtesting/internal/service/types.go` field-for-field
- [x] `api/client.ts` — `runBacktest`, `backtests`, `backtest(id)`
- [x] `use-backtests.ts` — fetch-on-mount + `refetch()`, no polling, same
      shape as Step 15's `use-orders.ts` and the same reasoning: nothing
      outside this session creates a backtest for this account
- [x] `backtest-validation.ts` — client-side mirror of `validateRequest`'s
      bounds (`short_window≥2`, `long_window` in `(short_window,500]`,
      dates, `starting_capital>0`), pure function, unit tested per boundary
- [x] `backtest-errors.ts` — five error codes mapped; `invalid_request`
      passed through verbatim rather than replaced with static copy, since
      backtesting's own validation messages are already specific and safe
      (a deliberate deviation from Step 15's `rejection-reason.ts`
      precedent, not an oversight)
- [x] `MetricsGrid.tsx` — `profit_factor: null` renders as "—" with a
      "no losing trades" note, never `0`/`∞`; `total_return_pct`/
      `sharpe_ratio` sign-colored, `max_drawdown_pct` left neutral since the
      backend only ever returns a non-negative value
- [x] `TradeLogTable.tsx`, `BacktestResult.tsx`, `BacktestForm.tsx`,
      `BacktestHistoryList.tsx`, `BacktestPanel.tsx` — composition layer,
      reusing `format.ts`'s `formatPrice`/`formatQuantity` and the existing
      up/down/em-dash P/L convention rather than inventing new formatting
- [x] `Dashboard.tsx` — `'backtest'` added as a fifth tab; `OrderTicket`
      stays pinned across it unchanged, same as every other tab
- [x] Manual adversarial pass against the real stack and real ingested AAPL
      history, in a browser — found two real bugs, see below
- [x] `npm run lint`, `npm run build`, `npm run test` (39 tests: 17 from
      Step 15 plus 22 new) all clean; backend `make test`, `make vet`,
      `make test-integration` re-run clean after the one backend fix this
      step's own testing required (see below)

**Completed 2026-08-18.** Spec, plan and todo archived to
`docs/archive/phase3-step17-backtesting-frontend/`.

### A real backend bug the frontend surfaced: `Trades` marshaling as `null`

`Simulate` (`services/backtesting/internal/service/simulate.go`) built its
trade log as `var trades []TradeRecord` — a nil slice until the first
`append`. `len()` treats a nil and an empty slice identically, so every
existing `Simulate`/`RunBacktest` test (all of which assert on `len(...)`)
stayed green with this in place. `encoding/json` does not: a nil slice
marshals as `null`, not `[]`. Any run whose date range gave
`GenerateSignals` no room to fire even once — the exact "no losing trades"
zero-trade case this step's own manual pass went looking for on purpose —
sent `"trades": null` over the wire. `TradeLogTable.tsx` calls `trades.length`
unconditionally, so the frontend crashed with a blank screen (a real,
reproduced-twice `TypeError`, caught via `read_console_messages`, not a
theoretical concern). `GetBacktest`'s store path already built from
`[]service.TradeRecord{}` and `ListBacktests`' handler already guarded nil
before marshaling — `Simulate` was the one place in the whole backend that
didn't follow the "list responses are never null" rule this project already
applies everywhere else. Fixed with a one-line change (`trades :=
[]TradeRecord{}`) plus a new regression test,
`TestSimulate_TradesIsNeverNilEvenWithZeroTrades`, which encodes the result
with `encoding/json` and asserts the literal bytes `[]` — confirmed it fails
against the pre-fix code and passes against the fix, then re-verified live:
a fresh zero-trade `POST /backtests` now returns `"trades": []` and renders
"No trades were simulated for this run." with no crash.

### A real rendering bug: calendar dates shifting a day backward

`BacktestResult.tsx`, `BacktestHistoryList.tsx`, and `TradeLogTable.tsx` all
formatted `start_date`/`end_date`/`bar_timestamp` with a bare
`new Date(...).toLocaleDateString()`. Those three fields are calendar dates
with no meaningful time-of-day (the backend's own `dateLayout` comment says
so), encoded as UTC midnight on the wire (`2024-08-01T00:00:00Z`).
`toLocaleDateString()` with no `timeZone` option renders in the *viewer's*
local zone, which for anyone west of UTC (this dev machine: US Eastern,
UTC-4) reads that exact value as the *previous* day — `7/31/2024` for a
form input of `08/01/2024`. Caught by directly comparing what was typed
into the form against what the result view echoed back during this step's
own manual verification, not by any of the unit tests (§2.10 scoped tests
to validation/error-mapping, not rendering). Fixed by adding a shared
`formatDate` to `frontend/src/format.ts` that renders with
`{timeZone: 'UTC'}`, used by all three call sites; two new tests pin the
exact UTC-midnight and non-UTC-offset cases and were confirmed to fail
against the pre-fix `toLocaleDateString()` call before the fix went in.

### Verification

**Manual browser pass**, full stack running (auth, market-data,
trading-engine already up from earlier in the session; a fresh gateway and
`backtesting` started to pick up Step 16's code), two rounds of throwaway
accounts (the first round, `step17review`/`step17stranger`, found the two
bugs above; a second round after both fixes re-verified every scenario
against the corrected code), against real ingested AAPL history:

- **Golden path**: a 5/20 crossover over AAPL's full ~2-year range ran
  end-to-end in the browser — metrics grid, trade log (buy rows showing an
  em-dash P/L, sell rows colored), and the new run appearing in history
  immediately, all without a page reload
- **`profit_factor: null` with trades**: a narrow date range with only
  winning trades (a 2/3 crossover over one month) rendered "—" with a
  "no losing trades" note next to it, confirmed against the raw API response
  (`"profit_factor": null`) rather than assumed from the UI alone
- **Zero trades at all**: a date range too short for `GenerateSignals` to
  fire (one month against a 5/20 crossover) — the exact case that found the
  `null`-marshaling bug above — re-run after the fix and confirmed
  `"trades": []` on the wire and "No trades were simulated for this run."
  rendered with no crash, three separate times including a fresh `POST`
- **`symbol_unavailable`**: an unignested symbol (`ZZZZ`) produced "No
  historical data is available for that symbol." inline, no history row
  created
- **`date_range_unavailable`**: a valid symbol with dates entirely before
  AAPL's ingested range (Jan–Jun 2020, ingestion starts 2024-07-30) produced
  "No historical data is available in the requested date range."
- **Reopening history**: clicking an older run in the sidebar reloaded it
  via `GET /backtests/{id}` and rendered identically to what was originally
  shown, with the row highlighted as selected
- **Cross-user isolation**: `step17stranger`'s history list showed "No
  backtests run yet." — confirms scoping holds in the browser, not just in
  Step 16's store-level integration test

Dev database returned to `users=20, accounts=20, backtests=0,
backtest_trades=0` after all three throwaway accounts across both rounds
(and the backtests they owned — `backtests.user_id` has no
`ON DELETE CASCADE`, so those had to be deleted explicitly before the
users) were removed. The `gateway` and
`backtesting` processes started for this session's verification were killed
afterward; `auth`/`market-data`/`trading-engine` were left running as they
were untouched by this step.

---

## Still open

- [ ] **RSI and MACD strategies**, `agents.md` §3's other two named
      examples. `SPEC.md` (Step 16) §1 scoped these out deliberately, and
      Step 17's own non-goals reaffirmed it — now that a frontend exists to
      drive a strategy picker, this is the natural next extension rather
      than dead weight behind a UI nobody could use yet.
- [ ] **Multi-symbol / portfolio-level backtests.** One symbol per run today
      (`SPEC.md` §1 Non-goals) — a materially different simulator
      (correlation, cross-symbol position sizing), not a small extension.
- [ ] **market-data's store still has no tests** — `historical_price_store.go`.
      `docs/deferred-tuning.md` §11 records that `backtesting`, not
      `market-data`, ended up being the harness's third copy; a fourth use
      (this one) is the point to actually extract rather than copy again.
- [ ] Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`,
      untouched since Step 11 — carried over from `PHASE2_CHECKLIST.md`.
