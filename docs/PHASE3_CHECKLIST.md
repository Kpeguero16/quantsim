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

## Step 18: RSI & MACD Strategies

`agents.md` §3 names three example strategies; Step 16 built the first
(moving-average crossover) and deferred the other two twice — once because
the pipeline was the hard part, once because there was no UI to drive a
strategy picker. Both reasons were spent once Step 17 shipped. Made the
backtesting engine multi-strategy: a `Strategy` interface with three
implementations behind one `NewStrategy` constructor, a `{strategy, params}`
wire format replacing the two fixed window fields, `strategy TEXT` +
`params JSONB` replacing the two window columns, and a strategy picker in
the existing dashboard tab. `Simulate`, `ComputeMetrics`, `backtest_trades`,
the next-bar-open fill rule, and all five metrics are **untouched** — every
line of new code sits upstream of `[]Signal`, which is the whole payoff of
how Step 16 originally split the pipeline.

- [x] Spec drafted and reviewed — four open decisions (RSI fires on the
      *exit* from a zone rather than the *entry*, a flat price series reads
      RSI as `50` rather than `100`, backend and frontend ship in one
      combined step rather than split, `short_window`/`long_window` are
      dropped outright rather than kept dual-written) all resolved as
      recommended (`SPEC.md` §3)
- [x] Plan (`tasks/plan.md`) — 19 tasks across 5 phases, sequenced so the
      indicators (`ema`, `wilderRSI`) land and pass hand-computed reference
      fixtures *before* any strategy calls them — the plan's own risk note:
      a subtly wrong indicator produces a plausible equity curve and
      plausible metrics with every downstream test still green, unlike
      `Simulate`'s self-evident balance-conservation invariant
- [x] `Strategy` interface (`Kind`/`Params`/`WarmupBars`/`GenerateSignals`),
      one `NewStrategy(kind, raw)` constructor as the sole validation
      point; Step 16's `GenerateSignals` free function moved behind it as
      `maCrossover` with **zero behavior change** — the crossing-detection
      assertions are byte-identical to Step 16's own tests
- [x] `wilderRSI` and `ema` (`indicators.go`) — pure `[]float64 -> []float64`,
      each verified against a hand-computed fixture carried through the
      seed bar and several smoothed bars *before* either strategy exists to
      consume them
- [x] `rsiStrategy` — exit-from-zone signals (buy on the cross back above
      oversold, sell on the cross back below overbought), the same
      edge-triggered shape `maCrossover` already uses; `WarmupBars =
      period + 1`
- [x] `macdStrategy` — MACD-line/signal-line crossings, sharing
      `crossoverSignals` with `maCrossover` since both rules are "cross on
      the sign change of two series' difference"; `WarmupBars = slow +
      signal - 1`
- [x] `maxWarmupBars = 500` replaces `maxLongWindow`, checked once in
      `NewStrategy` rather than per-constructor — catches a MACD whose
      `slow + signal - 1` exceeds 500 while `slow` alone does not, a case a
      bound on `slow_period` alone could never see
- [x] Migration `008_backtest_strategies` — `strategy`/`params` added and
      backfilled, `short_window`/`long_window` dropped; both directions run
      against a throwaway database with a mixed row set before being
      trusted, not just written (see below)
- [x] `{strategy, params}` on the wire (`RunBacktestRequest`, `Backtest`) —
      a **deliberate breaking change with no compatibility shim**: the only
      client is this repo's own frontend, updated in the same step
- [x] `postgres_backtest_store.go` — `strategy`/`params` through the
      `INSERT`, both `SELECT`s, `scanBacktest`
- [x] `internal/handler` — needed **zero changes**, confirmed by `grep`,
      not just by inspection: error mapping is by sentinel, and
      `NewStrategy`'s failures are already `ErrInvalidRequest`
- [x] `api/types.ts` — `Backtest`/`BacktestDetail`/`RunBacktestRequest`
      rebuilt as genuine discriminated unions (three tagged variants, not
      one interface with a loose `params: BacktestParams` field), so
      narrowing on `.strategy` inside a `switch` actually narrows `.params`
- [x] `backtest-validation.ts` — switches on the selected strategy, mirrors
      all three strategies' backend bounds including the MACD
      `slow_period + signal_period - 1` boundary
- [x] `strategy-display.ts` — one `describeStrategy` replacing two inline
      `{short}/{long}` formats, with an unknown-strategy fallback that
      renders the raw name rather than throwing (see below)
- [x] `BacktestForm.tsx` — a strategy `<select>` swaps the visible field
      group; every strategy's fields carry their own default from mount, so
      switching never leaves an empty, unrunnable form
- [x] `BacktestResult.tsx`/`BacktestHistoryList.tsx` — both now call
      `describeStrategy`; `MetricsGrid.tsx`/`TradeLogTable.tsx` untouched
- [x] RSI and MACD run through real Postgres JSONB — the two strategies
      `testBacktest`'s existing fixture never exercised
- [x] Mutation-tested the three highest-value new controls (`maxWarmupBars`,
      RSI's `oversold < overbought`, `crossoverSignals`' edge-only firing)
      — all three caught, all cleanly reverted
- [x] Manual browser pass — all three strategies live in the dashboard, a
      live confirmation of the `profit_factor: null` render rule at 100%
      win rate, both zero-trade boundaries, a pre-Step-18 MA run reopened
      from history
- [x] `npm run lint`/`build`/`test` (58 tests) and `make vet`/`test`/
      `test-integration` (all five services) all green throughout
- [x] Adversarial pre-merge review, independent of the work above — found
      and fixed one real bug (integer overflow in `WarmupBars`), see below

**Completed and merged to `main` 2026-08-18** (squashed to
`feat(step18): RSI and MACD strategies`, merge commit `3b94d27`). Spec,
plan and todo archived to
`docs/archive/phase3-step18-rsi-macd-strategies/`.

### Getting the indicators right without trusting self-consistency

Every prior step's riskiest logic (`Simulate`'s balance conservation,
`ComputeMetrics`' null/zero edge cases) had a self-evident invariant a test
could assert against. `wilderRSI` and `ema` don't — a subtly wrong smoothing
constant or a seeding bug produces a plausible-looking equity curve and
plausible-looking metrics, and every downstream test still passes, because
nothing downstream knows what the "right" number should have been. The plan
built around this from the start (D1: indicators land and pass before any
strategy exists to consume them), and each indicator was checked against a
fixture computed independently of the implementation:

- `wilderRSI`: hand-derived through the seed bar and three further smoothed
  bars via Wilder's own recurrence (`avg = (avg*(period-1) + current) /
  period`) — the distinguishing feature versus the simple-average "Cutler's
  RSI" this is deliberately not — plus the three degenerate rows §2.2 calls
  out explicitly (all gains → `100`, all losses → `0`, perfectly flat →
  `50`, the honest neutral rather than the `100` a naive `RS = 0/0`
  short-circuit produces by accident).
- `ema`: two independent fixtures chosen so every expected value is an
  exact integer, so a wrong seed or wrong `alpha` is visible immediately
  rather than lost in floating-point rounding.
- The MACD strategy's own crossing fixture — a price series that declines
  then reverses, expected to produce exactly one bullish cross — was
  computed by hand and then **independently cross-checked with a small
  Python script** replicating the exact recurrence before it went into the
  Go test. Nested EMA-of-EMA arithmetic by hand is exactly the kind of
  place a manual slip hides; the script confirmed the hand derivation to
  five decimal places rather than trusting it on faith.

Migration `008`'s up direction was validated the same way this project's
specs already validate SQL before writing it as fact: run against a
throwaway database (`step18_migration_scratch`, created and dropped) with a
pre-existing `ma_crossover` row, confirming the exact backfilled JSON. The
down direction was validated against a second, richer scenario in the same
database — a directly-inserted `rsi` row with a `backtest_trades` row
attached — confirming the down migration deletes the non-`ma_crossover` run
and its trade (cascading via the existing FK) and restores the
`ma_crossover` row's `short_window`/`long_window` to exactly what the up
migration had encoded. The real dev database was never touched by either
run; `users=20, backtests=0` held throughout.

### A TypeScript gotcha caught before it shipped: `Pick` does not distribute over a union

`strategy-display.ts`'s first draft defined `BacktestParamsByKind` as
`Pick<Backtest, 'strategy' | 'params'>`. `Pick` applied to a union type does
not distribute over its members — TypeScript resolves `T[P]` for a shared
key by unioning across all members, collapsing the intended three-variant
shape into one flat `{ strategy: StrategyKind; params: BacktestParams }`.
That silently loses the pairing between a given strategy and its own params
type, which is exactly the correlation `describeStrategy`'s `switch` on
`backtest.strategy` needs to narrow `backtest.params` inside each case —
with the collapsed type, every case would still see the full
`MACrossoverParams | RSIParams | MACDParams` union and the code would only
compile by accident (or not at all, depending on what each case actually
read). Caught before any test ran, restated as a direct three-member union
instead — the same shape `Backtest` itself already uses, and now the
narrowing works the way the switch statement's shape implies it should.

### A real bug an adversarial review found before merge: integer overflow bypassing `maxWarmupBars`

Everything above landed green through nineteen task commits and passed
Checkpoints B and C, mutation testing, and a full manual browser pass —
all real verification, none of it fabricated. But per this project's
"review before merge" convention, the branch also went through an
independent adversarial review before touching `main`, and that review
is what actually caught the one genuine bug in this step.

`newRSIStrategy` and `newMACDStrategy` bounded their period parameters
only from below (`period >= 2`, etc.), relying entirely on
`NewStrategy`'s single generic `WarmupBars() > maxWarmupBars` check to
reject anything too large. That check is exactly what a large enough
period defeats: `rsiStrategy.WarmupBars()` is `period + 1` and
`macdStrategy.WarmupBars()` is `slowPeriod + signalPeriod - 1`, and a
period near `math.MaxInt` overflows either sum to a large-magnitude
*negative* number. `negative > 500` is `false`, so the bound check
passes silently, and the request sails through `RunBacktest`'s second
`WarmupBars() > len(ranged)` check for the same reason. Execution then
panics deep inside `GenerateSignals` — an `index out of range` for RSI,
a `slice bounds out of range` for MACD — reachable by any authenticated
user via a single crafted `POST /backtests` body. `maCrossover` was
never affected: its `WarmupBars()` is `LongWindow` directly, no
arithmetic to overflow.

Fixed by rejecting `period`/`slow_period`/`signal_period` individually
above `maxWarmupBars` in each constructor, before any arithmetic touches
them — so the later sums those constructors feed into can never
overflow. Confirmed with a standalone (non-repo) repro before the fix,
and with two new regression test cases per strategy after it (one just
over `maxWarmupBars`, one near `math.MaxInt`).

### Verification

**Every task landed compiling and green**, not just the final state: T1
(the `Strategy` interface, no behavior change) was checked against Step
16's own byte-identical assertions; T2's indicators shipped and passed
before T3/T4 existed to depend on them; T6-T7's wire-format change was
allowed to temporarily break `internal/store`'s compilation (confirmed via
`go build` that *only* `store` broke, exactly as the migration hadn't
landed yet) rather than rushing an incomplete fix, then T8-T10 closed that
gap in order.

**Checkpoint B** (after the backend, before the frontend): `make
vet`/`test`/`test-integration` clean across all five services, migration
008 applied to the real dev database (`users=20` held), and all three
strategies run live via `curl` against the real gateway — `POST
/backtests`, `GET /backtests` (history list), `GET /backtests/{id}`
(reopen), and an unknown-strategy rejection all confirmed against real
responses.

**Checkpoint C** (after the frontend): `npm run lint`/`build`/`test` (58
tests) all green. The build check specifically used the project's real
build command (`tsc -b`) rather than a bare `tsc --noEmit`, which was
found to silently no-op against this project's referenced `tsconfig` setup
and report zero errors regardless of what was actually broken — a check
that would have rubber-stamped the frontend work without verifying
anything.

**Mutation testing**, three controls broken deliberately and confirmed
caught, then reverted (`git diff` empty afterward, full `make vet`/`test`
clean): the `maxWarmupBars` bound (short-circuited with `false &&`, caught
by the MA and MACD boundary tests), RSI's `oversold < overbought` check
(dropped from the condition, caught by both ordering subtests), and
`crossoverSignals`' edge-only firing (changed to fire on every in-condition
bar rather than only the crossing bar — caught five tests across both
strategies that share this function, including the one named for exactly
this case).

**Manual browser pass**, full stack running (auth/market-data/
trading-engine already up from earlier; a fresh gateway, `backtesting`, and
frontend dev server started for this pass), one throwaway account
(`step18browser`), against real ingested AAPL history:

- All three strategies ran end to end through the real dashboard — MA
  5/20, RSI(14) 30/70, MACD(12/26/9) — each producing a correctly labeled
  result header and history row via `describeStrategy`
- The strategy `<select>` correctly swapped visible field groups on every
  switch, and values typed into a since-hidden field were never lost
- RSI's run happened to have a 100% win rate, which is a live confirmation
  of the `profit_factor: null`/"no losing trades" render rule established
  in Step 16/17: rendered "—" with the note, not `0` or a crash
- Both zero-trade boundaries (a narrow RSI window, a narrow MACD window)
  rendered "No trades were simulated for this run." with no crash — the
  exact failure mode Step 17's `trades: null` bug produced once already
- The original MA run reopened from history and re-rendered
  "AAPL — 5/20 crossover" correctly through the new `describeStrategy` path
- Zero console errors across the whole session

Dev database returned to `users=20, accounts=20, backtests=0` after the
throwaway account and its backtests were removed. `gateway`, `backtesting`,
and the frontend dev server (started fresh for this step's verification)
were killed afterward; `auth`/`market-data`/`trading-engine` were left
running as they were, untouched by this step.

---

## Step 19: Portfolio Backtests

The item "Still open" had carried since Step 16, and the one Phase 3 could
not end without: one run, N symbols, **one shared pool of capital**. Not N
independent single-symbol runs stapled together — that would have been a
loop around the existing engine and would have answered a different
question. `symbol TEXT` became `symbols TEXT[]`, `Simulate` was replaced by
`SimulatePortfolio` and then deleted, and every trade learned which symbol
it belongs to. `ComputeMetrics` is **untouched** and still does not know how
many symbols produced the curve it is handed — the same "new code sits
upstream of the shared pipeline" payoff Step 18 got, one stage further down.

- [x] Spec drafted and reviewed — §2.2's position sizing was revised *during*
      review: the target had been `startingCapital / N`, which silently
      breaks compounding and makes a 1-symbol run diverge from the engine it
      replaces. It is `equityAtOpen / N` (`SPEC.md` §5)
- [x] Plan (`tasks/plan.md`) — 19 tasks across 5 phases, sequenced so the
      A/B equivalence test between old and new engines is written **first**,
      before either the wire format or the schema moves
- [x] `alignBars` — the intersection of every symbol's dates, returned
      alphabetically, so bar index `i` is the same trading day for every
      symbol. At N=1 its output is exactly the old `sliceRange`'s. The sort
      guard needed rewriting: three symbols in a map land sorted by luck
      about one run in six, so it now uses six symbols across repeated calls
- [x] `SimulatePortfolio` — sells settle first into the shared pool, one
      `target := equityAtOpen / N` per bar, then buys in symbol order each
      capped at `min(cash, target)`. Sells precede buys so the pool is
      genuinely shared *within* a bar and behavior does not depend on where
      the seller falls in the alphabet
- [x] A/B equivalence written before the engine it validates — a 1-symbol run
      through `SimulatePortfolio` asserted **exactly float-equal** to
      `Simulate` on a 2-profitable-round-trip fixture. This is what made
      deleting `Simulate` (T7) a safe operation rather than a hopeful one
- [x] `normalizeSymbols` — trim/upper/sort, rejecting empty, >10, and
      case-insensitive duplicates outright. A duplicate is refused, not
      silently collapsed: quietly running a 1-symbol backtest for someone
      who asked for 2 is a worse answer than saying no
- [x] `RunBacktest` fans out via a **zero-value** `errgroup.Group`, not
      `WithContext` — cancelling siblings on the first failure would let a
      context error stand in for the real one and reintroduce exactly the
      nondeterminism the ordered error scan exists to remove
- [x] `Simulate` and the transient A/B test deleted once the equivalence had
      been demonstrated; Step 16's six call sites now route through a
      `simulateSingle` helper with **no expectation touched**
- [x] Migration `009_backtest_portfolios` — `symbols TEXT[]`, `symbol` and
      `seq` on `backtest_trades`, with the trade backfill ordered **before**
      `backtests.symbol` is dropped. That statement order is load-bearing and
      was tested rather than assumed: reordered, it fails with
      `column b.symbol does not exist`
- [x] Store — `[]string` binds and scans against `TEXT[]` with no `pgtype`
      wrapper (the repo's first array column, so proven against real
      Postgres rather than trusted to compile)
- [x] Handler — confirmed **zero** changes needed; it decodes
      `RunBacktestRequest` whole and never named the renamed field
- [x] Frontend — `symbols: string[]` through the wire types, a
      `validateSymbols` mirror of the backend rule, the form taking a
      comma-separated list, a Symbol column in the trade log, and
      `symbols.join(', ')` wherever a run's symbol was shown

**R1 — a real bug the Checkpoint B review found.** Same-bar trades came back
in a **random order**. The trade SELECT was `ORDER BY bar_timestamp, id` with
`id` a random UUID, and ties had been impossible only while one run meant one
symbol and so at most one fill per bar. A portfolio run fills several symbols
on the same bar routinely — a sell could be listed beneath the same-bar buy
it funded. It was probed before it was fixed: the same three same-bar trades
written six times read back in six different orders. `009` gained
`seq INTEGER NOT NULL` + `UNIQUE (backtest_id, seq)`, the store writes the
slice index and reads `ORDER BY seq`, and the log is now stored as the
sequence it is rather than re-derived from row values that cannot express it.

**Mutation testing**, four controls broken and confirmed caught, then
reverted to a byte-identical tree (`git diff` against the pre-mutation commit
empty, `gofmt` silent, full `make vet`/`test` green plus a forced
`-count=1 -race` run, since `make test` reported `(cached)`):
`min(cash, target)` → `target` (1 test); **`equityAtOpen / N` →
`startingCapital / N`** (3 tests); sells-before-buys → one alphabetical
sell-or-buy pass (1 test); and the validation controls removed one at a time
— the 10-symbol cap (the `eleven_symbols` subtest) and the duplicate check
(all three duplicate subtests). The second of those is the one worth having
run: T7 deleted the A/B test that originally caught it and asserted three
portfolio tests still would, and that assertion had been carried on trust
until this point.

**Checkpoint B** (after the backend): `make vet`/`test`/`test-integration`
green, plus `-race` and `-tags=integration` on `services/backtesting`.
`009` applied to the real dev database (`schema_migrations` 9, not dirty),
baseline `users=20 backtests=0` held either side. Live against real
market-data: `{"symbols":["msft","aapl"]}` came back `["AAPL","MSFT"]` with
13 interleaved trades each carrying its symbol.

**Checkpoint C** (after the frontend): `tsc -b` clean, `npm run build` ✓,
`npm run test` 61/61, `npm run lint` with only the four pre-existing
`exhaustive-deps` warnings, none in a file this step touched.

**Integration.** The plan's `testBacktest`-becomes-symbols-aware and
3-symbol-round-trip items were already satisfied by earlier tasks, so T17
was scoped to what was genuinely uncovered — precision. Four fills across
three symbols with no two adjacent rows sharing a symbol, quantities
asserted through `numeric()` against the 4-decimal value `NUMERIC(20,4)`
actually keeps *and* against what `GetBacktest` returns. This bites harder
at N>1 than it ever did at N=1: a position is funded by `equity/N`, so a
fixed 0.0001 granularity covers more of a smaller position. The harness
guard also now asserts `backtests.symbol` is **gone** — `009` drops it last,
so a part-applied migration leaves both columns present and every store
statement still runs, against a table quietly holding a stale singular
answer. Both new tests were mutated before being believed.

**Manual browser pass**, full stack, real ingested history (7 symbols ×
501 bars), one throwaway account:

- The dev database sits at `backtests=0`, so there was no legacy run to
  reopen — one was **manufactured**: `009` rolled back, an old-shape
  single-symbol row inserted with its two trades in **reverse chronological
  order**, `009` re-applied. `seq 0` came out as the *earlier* buy, so the
  backfill's `ORDER BY bar_timestamp` is genuinely sorting rather than
  inheriting insertion order — previously shown only in a scratch database.
  It then reopened from history and rendered correctly, which is the only
  thing that proves a *migrated* row survives into the UI
- Typing `tsla, googl,` produced `GOOGL, TSLA` — uppercased, sorted, and the
  trailing comma **dropped rather than rejected**, the one deliberate
  divergence the frontend validator has from the backend's
- A duplicate (`AAPL, aapl`) and an 11th symbol were both refused
  client-side, before any network call
- 10/29/2024 carried two fills in one bar, alphabetically ordered — same-bar
  contention rendered
- Across a 7-symbol run's 168 trades and 28 contended bars, 2025-02-13 has
  **AMZN selling before AAPL buys**. AMZN is alphabetically later, so a
  single alphabetical pass would invert it: the sells-before-buys rule
  confirmed against real market data, not a fixture. Six re-reads of that
  log were byte-identical — R1's fix under the probe that once produced six
  different orders
- The zero-trade path still renders "No trades were simulated for this run."

Throwaway rows were deleted **before** their user (`backtests.user_id` is
`NO ACTION`, confirmed in `information_schema` rather than assumed), and the
baseline re-verified: `users=20 backtests=0 trades=0`, migration 9 clean.
Two caveats recorded rather than smoothed over — `migrate(1)` is not on the
working shell's PATH, so the down/up ran the real migration SQL directly with
`schema_migrations` updated to match; and console tracking began after page
load, so there is **no console evidence in either direction** for this pass.

**Pre-merge review** across correctness, readability, architecture, security
and performance. **No Critical or Important findings** — the first step in a
while without one, having gone looking specifically at the shared-cash
arithmetic, the array codec, the concurrency and the auth path. What it
confirmed by checking rather than reading: the loop-variable capture in
`fetchHistories` is safe (`go 1.25`, per-iteration semantics); `alignBars`'
equal-length invariant is genuinely asserted, which matters because the
warm-up check trusts `len(aligned[0])` alone; and T7's "no expectation
touched" claim is exactly true of the diff.

One theory the review formed and then **disproved by running it**: that a
malformed symbol would surface as a 502 blaming market-data for what is a
client input error. Tested live with `!!!!`, a 300-character symbol, and an
unknown ticker — all three return a clean 400 `symbol_unavailable`, because
a failed fetch returns before `SaveBacktest` and market-data's own empty-body
convention maps to `ErrSymbolUnavailable`. Recorded because a plausible
finding that survives reading and dies on execution is worth remembering.

Acted on: **S1**, `alignBars`' daily-bar assumption — it keys the
intersection by calendar date, so if intraday bars ever reach it several
would share one `dayKey`, `byDay` would keep only the last, and the driver
symbol would still emit one row per bar. Mismatched series, **no error**.
`historical_prices` already has a `timeframe` column, so this is reachable by
a future change rather than hypothetical. Now documented at the assumption.

Left unaddressed as recorded judgment calls, not oversights:

- **S2** — the trade-log `pgx.Batch` is unbounded and Step 19 multiplied it
  by N: worst case ~20,000 queued statements in one transaction against
  ~2,000 before. Fine at today's 501 bars; chunk it if history grows.
- **S3** — `alignBars` allocates a `matches` slice per driver bar. Hoisting
  one reusable buffer is safe (the values are copied by `append`) and would
  drop ~2,000 allocations on a large run. Micro-optimization.
- **S4** — the persisted `symbols` come from `params.Symbols` while the trade
  log's come from `alignBars`' derived list. Identical today, and nothing
  enforces it; a divergence would put trades under symbols absent from their
  own run.
- **S5** — no length or format bound on a symbol, where `trading-engine`
  has `maxSymbolLength = 16`. Its stated rationale does **not** transfer:
  it bounds junk because a *rejected order persists verbatim*, whereas here
  a failed fetch returns before anything is written. Noted so the divergence
  from the sibling service is a decision on the record.

---

## Still open

- [x] **Multi-symbol / portfolio-level backtests** — done (Step 19). One run,
      N symbols, one shared pool of capital; `symbols TEXT[]`, per-trade
      `symbol`, and a stored trade-log `seq`.
- [ ] **market-data's store still has no tests** — `historical_price_store.go`.
      `docs/deferred-tuning.md` §11 records that `backtesting`, not
      `market-data`, ended up being the harness's third copy; a fourth use
      (this one) is the point to actually extract rather than copy again.
- [ ] Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`,
      untouched since Step 11 — carried over from `PHASE2_CHECKLIST.md`.
