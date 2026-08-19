# SPEC — RSI & MACD Strategies: A Multi-Strategy Backtest Engine (Step 18)

Status: **Approved 2026-08-18.** All four open decisions in §3 resolved as recommended. Implementation is unblocked — not started.
Scope: `services/backtesting/internal/{service,store}`, a new migration (`008_...`), and `frontend/src/{api/types.ts,backtesting/}`. No gateway change (`/backtests/*` is already proxied), no `market-data` change, no new dependencies either side.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase3-step17-backtesting-frontend/`.

---

## 1. Objective

`agents.md` §3 names three example strategies — moving-average crossover, RSI thresholds, MACD signals. Step 16 built the first and deliberately deferred the other two (`docs/archive/phase2-step16-backtesting-engine/SPEC.md` §1: "the pipeline this step builds is the hard part; a second and third strategy are additive"). Step 17 reaffirmed the deferral for a different reason — there was no UI to drive a strategy picker. Both reasons are now spent: the pipeline exists and is tested, and Step 17's dashboard tab exists to pick from.

**Objective:** make the backtesting engine multi-strategy. `POST /backtests` accepts a named strategy and its parameters; RSI and MACD join moving-average crossover as implementations; the run's strategy and parameters persist and render everywhere a `5/20` currently does.

**What this step is not about: the simulator or the metrics.** Step 16's `Simulate` and `ComputeMetrics` consume `[]Signal` and know nothing about how those signals were produced. That is the whole payoff of §2.3–2.5's design, and this step collects it — **neither function changes, and neither does `backtest_trades`, the fill-timing rule, or any of the five metrics.** All the work is upstream of `[]Signal`, plus the plumbing to carry a variable-shaped parameter set through the wire, the schema, and the UI.

**Non-goals:**
- **Custom or script-based strategies.** `agents.md`'s own Stretch Features list, and Step 16 §1's non-goal verbatim: sandboxing user-supplied code is a security project, not a strategy addition. This step ships three *named* strategies with typed parameters, which is the opposite of arbitrary code.
- **Combining strategies.** No AND/OR of two strategies' signals, no confirmation filters. One strategy per run — the same singular framing `agents.md`'s processing flow uses.
- **Parameter optimization / sweeps.** No "run RSI across every period from 5 to 30 and rank them." That is a different endpoint with a different cost profile (N simulations per request) and would break §2.9's synchronous-response decision. A user can run them one at a time and compare in the history sidebar.
- **Short selling.** RSI and MACD are frequently traded both ways; this simulator is long-only, all-in, one position (Step 16 §2.3), matching `trading-engine`'s own long-only constraint. A sell signal while flat stays a no-op. Adding shorts changes the simulator and the equity curve, not the strategies.
- **Multi-symbol / portfolio backtests.** Unchanged from Step 16 §1 and still the larger of the two remaining backtest items.
- **Equity-curve storage or charting.** Still nothing persists `SimulationResult.EquityCurve` (Step 16 §2.6). Unchanged here.
- **Intraday timeframes.** `historical_prices` still holds `1Day` bars only.
- **A compatibility shim for the Step 16/17 request shape.** See §2.6 — this step breaks the wire format on purpose.

---

## 2. Design decisions

### 2.1 A `Strategy` interface, because there are now three implementations and a fourth is expected

Today signal generation is one free function with a fixed parameter list: `GenerateSignals(bars, shortWindow, longWindow) []Signal`. Three strategies with 2, 3 and 3 parameters respectively cannot share that signature.

**(a)** Three free functions (`GenerateMASignals`, `GenerateRSISignals`, `GenerateMACDSignals`) plus a `switch` in `RunBacktest`.
**(b)** A `Strategy` interface, constructed and validated once from the request, then consumed by the pipeline.

**Recommendation: (b).** (a) is the smaller diff, but `RunBacktest` needs four strategy-dependent things — the signals, the warm-up bar count (§2.4), the canonical parameters to persist, and the strategy's name — and (a) means four parallel switches drifting apart. An interface is the boring answer at three implementations; it would be over-engineering at one.

```go
type Strategy interface {
    Kind() StrategyKind             // "ma_crossover" | "rsi" | "macd"
    Params() json.RawMessage        // canonical, validated -- what §2.5 persists and §2.6 echoes
    WarmupBars() int                // §2.4
    GenerateSignals(bars []Bar) []Signal
}

func NewStrategy(kind StrategyKind, raw json.RawMessage) (Strategy, error)
```

`Params()` returns pre-marshaled bytes rather than `any` or a `(json.RawMessage, error)` pair: each constructor marshals its own typed parameter struct once, at construction, where an error is already being returned anyway. Callers downstream never have to handle a marshal failure that cannot happen.

`NewStrategy` is the single place that decodes and validates parameters, so an unknown `kind`, a malformed `params` object, and an out-of-bounds period all surface as the same `ErrInvalidRequest` from one function — not scattered across three.

**Every strategy keeps `GenerateSignals`' existing edge-only contract**: a signal fires on the crossing bar and only that bar; a strategy sitting in a condition for forty bars emits one signal, not forty. `strategy.go`'s current doc comment already argues why (the alternative forces `Simulate` to guess which repeats to ignore), and that argument is strategy-independent.

### 2.2 RSI: Wilder's smoothing, threshold *exits* as signals

**Parameters:** `period` (default 14), `oversold` (30), `overbought` (70).

**Smoothing — Wilder's, not a simple average.** RSI has two variants in the wild: Wilder's original smoothed average (`avg = (avg*(period-1) + current) / period`, seeded with a simple mean of the first `period` gains/losses) and the simple-average "Cutler's RSI." **Recommendation: Wilder's.** It is the canonical definition and what every charting platform computes, so `RSI(14)` here means what a user's chart says it means. A backtest that quietly disagrees with TradingView about what its own indicator is would be a correctness embarrassment, and the two variants diverge visibly on real data.

**Signal rule — recommendation: fire on the *exit* from a zone, not the entry.**

- **Buy** on the bar where RSI crosses from `<= oversold` to `> oversold`.
- **Sell** on the bar where RSI crosses from `>= overbought` to `< overbought`.

The alternative (buy the moment RSI drops below 30, sell the moment it rises above 70) buys into an ongoing decline and is the classic way a mean-reversion backtest catches a falling knife. Waiting for the cross back out is the standard "wait for confirmation" reading of Wilder's own framing, and — the reason that matters structurally — it is **the same edge-triggered shape as the MA crossover**, so all three strategies share one signal-detection pattern rather than RSI being the odd one out.

**Degenerate windows must not produce `NaN` or `Inf`:**

| Case | RSI |
|---|---|
| `avgLoss == 0`, `avgGain > 0` (window of pure gains) | `100` |
| `avgGain == 0`, `avgLoss > 0` | `0` (falls out of the formula) |
| `avgGain == 0` **and** `avgLoss == 0` (perfectly flat prices) | **`50`**, defined explicitly |

The last row is a real decision, not a formality. The usual implementation returns `100` because `RS = 0/0` is short-circuited on the zero denominator — but a flat price series has no upward pressure whatsoever, and `100` is the maximum-bullish reading. `50` is the honest neutral. This is the same "an unpriceable value must never render as a misleading number" rule Step 16 §2.5 applied to `profit_factor` and Step 15 applied to the em-dash, applied here inside the indicator.

### 2.3 MACD: EMA-of-EMA crossover, signal-line crossings

**Parameters:** `fast_period` (12), `slow_period` (26), `signal_period` (9).

- MACD line = `EMA(close, fast) - EMA(close, slow)`.
- Signal line = `EMA(macdLine, signal)`.
- **Buy** where the MACD line crosses above the signal line; **sell** where it crosses below. Edge-only.

**EMA seeding:** each EMA is seeded with the simple mean of its first `period` inputs (so `EMA(period)` has its first value at input index `period-1`), then `ema[i] = x[i]*α + ema[i-1]*(1-α)` with `α = 2/(period+1)`. This is the standard construction; the alternative (seeding with the first value alone) converges to the same series but is materially wrong for the first several periods, which is exactly the region a short backtest range lives in.

Note that MACD's buy/sell rule is structurally identical to the MA crossover's — two series, fire on the sign change of their difference. **Recommendation:** factor that comparison into one unexported helper (`crossoverSignals(fast, slow []float64, from int) []Signal`) used by both `maCrossover` and `macd`, rather than writing the `wasAbove`/`haveState` loop twice. RSI's rule is a threshold crossing rather than a series crossing and does not share it.

### 2.4 `WarmupBars()`: "bars before the indicator has its first value," preserving Step 16's exact rejection boundary

`RunBacktest` currently rejects a range with `long_window > len(ranged)`. That generalizes to `strategy.WarmupBars() > len(ranged)`, and the definition has to be pinned down or each strategy will pick its own:

| Strategy | First indicator value at bar index | `WarmupBars()` |
|---|---|---|
| MA crossover | `long - 1` | `long` |
| RSI | `period` (needs `period` deltas) | `period + 1` |
| MACD | `slow + signal - 2` (signal line is an EMA over the MACD line, itself starting at `slow - 1`) | `slow + signal - 1` |

**Recommendation: define `WarmupBars()` as the number of bars the indicator needs to produce its first value — not the number needed for a signal to be *possible*.** These differ by one: a crossing needs a previous bar's state to compare against, so the earliest a signal can fire is one bar after the indicator is well-formed.

The looser definition is the deliberate choice, because the tighter one would **change Step 16's behavior**: a request with exactly `long_window` bars is accepted today (it runs, produces zero signals, and returns a zero-trade result — a case Step 17's browser pass exercised specifically). Tightening the check would start rejecting requests that Step 16 accepts, which is a silent behavior change riding along inside a step that is supposed to be additive. Zero trades is a legitimate outcome, not an error, and §2.9's manual pass covers it for the two new strategies.

**Also replaces the fixed `maxLongWindow = 500`.** Step 16 §3 decision 1 capped `long_window` at 500 because that is roughly the ingested bar count per symbol. The general form of that same bound is `strategy.WarmupBars() <= maxWarmupBars` (500), validated once in `NewStrategy` after each strategy computes its own warm-up — so MACD's cap correctly accounts for `slow + signal` rather than bounding `slow` alone at 500 and letting the real warm-up exceed it.

Per-parameter floors still apply, since a period of 0 or 1 is meaningless regardless of warm-up: `period >= 2` for RSI, `fast >= 2` and `slow > fast` and `signal >= 2` for MACD, plus `0 < oversold < overbought < 100` for RSI.

### 2.5 Persistence: `strategy TEXT` + `params JSONB`, replacing the two window columns

`backtests` has `short_window INT NOT NULL, long_window INT NOT NULL` — a shape that only describes one of the three strategies.

**(a)** `strategy TEXT NOT NULL` + `params JSONB NOT NULL`; migrate the two window columns into `params` and drop them.
**(b)** Keep `short_window`/`long_window`, make them nullable, and add `rsi_period`, `rsi_oversold`, `rsi_overbought`, `macd_fast`, `macd_slow`, `macd_signal` alongside — all nullable.

**Recommendation: (a).** (b) is eight sparse nullable columns where at most three are ever populated, plus a new migration for every future strategy, and it still cannot express a parameter that isn't an integer. Its one real advantage — column-level typing and constraints — is worth less than it sounds here, because `backtests` has no `CHECK` constraints today (nor does `backtest_trades.side`); the service layer is already the sole validation authority in this codebase, and §2.1 concentrates it in one constructor. These parameters are also only ever read back **whole**, to re-render a run — nothing filters or aggregates on them, which is precisely the condition under which JSONB costs nothing.

Migration `008_backtest_strategies.up.sql`:

```sql
ALTER TABLE backtests ADD COLUMN strategy TEXT NOT NULL DEFAULT 'ma_crossover';
ALTER TABLE backtests ADD COLUMN params JSONB;
UPDATE backtests SET params = jsonb_build_object(
    'short_window', short_window, 'long_window', long_window);
ALTER TABLE backtests ALTER COLUMN params SET NOT NULL;
ALTER TABLE backtests ALTER COLUMN strategy DROP DEFAULT;
ALTER TABLE backtests DROP COLUMN short_window, DROP COLUMN long_window;
```

The `DEFAULT`-then-`DROP DEFAULT` dance is what makes the new `NOT NULL` column legal against existing rows; the default is transitional only, so no future insert can quietly omit the strategy. The dev database currently has `backtests = 0` (`docs/NEXT_SESSION.md`), which makes the backfill a no-op **today** — it is written correctly anyway, because a migration is committed history that will run against databases that are not this one.

**This SQL has been run, not just written.** Applied in a throwaway database (`step18_spec_scratch`, created and dropped; the real `postgres` database was not touched and still reads `users=20, backtests=0`) against a stand-in `backtests` table holding one pre-existing Step 16-era row. The backfill produced `{"short_window": 5, "long_window": 20}`, and an insert omitting `strategy` afterward failed on the not-null constraint — confirming the transitional default does not survive as a silent fallback.

**No `CHECK (strategy IN (...))`.** It would mean a migration per new strategy, which is the exact cost (a) exists to avoid.

### 2.6 Wire format: a discriminated `{strategy, params}`, and a clean break from Step 16's shape

**(a)** Nested: `{symbol, strategy, params: {...}, start_date, end_date, starting_capital}`.
**(b)** Flat with optional fields: `short_window`, `long_window`, `rsi_period`, `macd_fast`, … all top-level and all optional.

**Recommendation: (a).** It makes the discriminated union explicit instead of implied, it round-trips to §2.5's JSONB column unchanged, and it avoids a request body in which six of nine fields are meaningless for any given call. (b)'s only merit is that it would not break the existing shape — and §2.7 argues that not breaking it is not worth buying.

The response mirrors it: `Backtest` loses `short_window`/`long_window` and gains `strategy` and `params`.

**`params` is re-marshaled from the validated typed struct, never echoed back as received.** `Strategy.Params()` (§2.1) returns the canonical encoding, so what gets persisted and what gets returned are byte-identical and free of any unknown keys a client happened to send. A stored run is then always readable by `NewStrategy` — a property worth having the moment anything needs to re-run or clone a past backtest.

**This is a breaking change to `POST /backtests` and to the `Backtest` response, taken deliberately with no compatibility shim.** The only client is this repository's own frontend, updated in the same step (§2.7); there is no API version, no external consumer, and no deprecation commitment anywhere in this project. A shim accepting bare `short_window`/`long_window` would be dead code the day it merged.

### 2.7 One step, backend and frontend together — a deliberate break from the 14→15 / 16→17 pattern

Steps 14→15 and 16→17 each shipped a backend and then its UI as separate steps. **Recommendation: do not split this one.**

The reason is §2.6's clean break, and the two decisions stand or fall together. A backend-only Step 18 would land a `Backtest` response without `short_window`/`long_window` while `BacktestResult.tsx` and `BacktestHistoryList.tsx` still read those fields — `main` would carry a dashboard that renders `undefined/undefined crossover` for every run, and `POST /backtests` would reject everything the form sends. The only way to split cleanly is to build the compatibility shim §2.6 rejects, i.e. to add throwaway work for the sole purpose of preserving a step boundary.

The split earned its keep in 14→15 and 16→17 because each backend was a large new system that deserved to be verified on its own before a UI existed to confuse the picture. This step's frontend surface is small and mechanical by comparison — a `<select>`, two conditional field groups, and one display helper — and it is downstream of a schema change rather than of a new system.

**If the combined step feels too large in review, the honest split is by strategy** (Step 18: the `Strategy` interface + migration + RSI, end to end; Step 19: MACD on top of it), not by layer. That keeps every merge to `main` in a working state, which the layer split here cannot.

### 2.8 Frontend: one strategy `<select>`, one display helper, validation as a discriminated union

- **`BacktestForm.tsx`** gains a strategy `<select>` above the parameter fields, which swap with the selection. Each strategy carries its own conventional defaults — `5/20`, `14/30/70`, `12/26/9` — so switching strategies always leaves a runnable form rather than empty inputs.
- **`backtest-validation.ts`** switches on the selected strategy and returns the discriminated `{strategy, params}` body. It stays one pure function with one test file, and it keeps mirroring the backend's bounds without becoming the authority — the boundary Step 17 §2.5 already drew. Per-strategy form state is held as strings, as today, so a half-typed field never becomes `NaN` mid-keystroke.
- **A new `strategy-display.ts`** with `describeStrategy(strategy, params): string` — `"5/20 crossover"`, `"RSI(14) 30/70"`, `"MACD(12/26/9)"` — used by both `BacktestResult.tsx`'s header and `BacktestHistoryList.tsx`'s row, which currently each format `{short}/{long}` inline. One tested pure function, the same shape as the existing `backtest-errors.ts` and `format.ts`.

**`describeStrategy` must return a readable fallback for an unrecognized strategy string, not throw and not index into `undefined`.** The API is the source of truth for what a stored run is, and a frontend that crashes the whole dashboard on a value it does not recognize is precisely the failure Step 17's `trades: null` bug already demonstrated. A run from a strategy this build does not know should render its raw name and still show its metrics.

### 2.9 Unchanged by design, and worth stating

- **`Simulate`, `ComputeMetrics`, `TradeRecord`, `backtest_trades`, the next-bar-open fill rule, the five metrics** — untouched. Every one of them consumes `[]Signal` or a `SimulationResult` and cannot tell which strategy produced it.
- **`POST /backtests` stays synchronous.** RSI and MACD are both single-pass computations over the same ~500 bars; Step 16's non-goal ("a single-symbol, ~500-bar backtest is a sub-second computation") is no less true with an EMA in the loop.
- **The gateway.** `/backtests` and `/backtests/*` are already routed (Step 16 §2.7, and the bare-prefix fix its routing bug produced). No change.
- **Auth and ownership.** `pkgauth.RequireAuth` on the `/backtests` group, `(id, user_id)` scoping in the store, 404-not-403 for a non-owner. No change.

---

## 3. Decisions (resolved 2026-08-18, all four as recommended)

1. **RSI fires on the *exit* from a zone, not the entry** (§2.2). The more defensible strategy and structurally consistent with the other two, at the cost of differing from the common textbook form — accepted knowingly rather than inherited as a default.
2. **A flat price series reads RSI = `50`, not `100`** (§2.2). The honest neutral over the accidental maximum-bullish. This engine therefore differs from some references in one degenerate case, which §4's tests assert deliberately.
3. **One combined step**, backend and frontend together (§2.7). MACD rides along rather than following as a Step 19.
4. **`short_window`/`long_window` are dropped outright** (§2.5), not kept dual-written alongside `params` for MA runs. Nothing in the repository queries them.

No further changes to §2.

---

## 4. Verification plan

Same posture as Steps 14–17: unit tests on the pure computation, mutation-tested to prove they are a real net; integration tests against real Postgres; a manual adversarial browser pass before close-out.

**The indicator implementations need reference values, not self-consistency.** This is the one materially new risk in this step: a subtly wrong RSI or EMA produces a plausible-looking equity curve and plausible-looking metrics, and every downstream test still passes. `Simulate` and `ComputeMetrics` had obvious invariants to assert against; `rsi()` and `ema()` do not.

- **RSI** — asserted against a hand-computed fixture carried through the seed bar and at least three smoothed bars, so a wrong smoothing constant or an off-by-one seed fails. Wilder's published example series in *New Concepts in Technical Trading Systems* is the reference to pin against. Plus each of §2.2's three degenerate rows, asserted explicitly.
- **EMA / MACD** — a hand-computed fixture through the SMA seed and several smoothed bars, and an assertion that the MACD line and signal line are first defined at the indices §2.4's table claims. The seeding choice is the likeliest thing to get quietly wrong.
- **Signal generation, all three** — edge-only firing (a strategy held in-condition across many bars emits exactly one signal), and the exact crossing bar index, not just the count.
- **`NewStrategy`** — unknown kind, malformed `params` JSON, each parameter bound at and just past its limit, and `WarmupBars() > maxWarmupBars` for a MACD whose `slow + signal` exceeds 500 while `slow` alone does not (the case §2.4 exists to catch).
- **Store integration** — a round trip of all three strategies' `params` through JSONB, asserting the reloaded run reconstructs via `NewStrategy`; plus migration `008` applied against a database holding a pre-existing `ma_crossover` row, verifying the backfill produces the right JSON rather than trusting an empty table.
- **Mutation testing** — per project convention, each new control broken deliberately and confirmed to fail a test. The warm-up bound, the `oversold < overbought` check, and the edge-only firing are the three most worth breaking.
- **Manual browser pass** — all three strategies run end to end against real ingested AAPL history; a zero-trade RSI run and a zero-trade MACD run (§2.4's accepted-but-signal-less boundary) confirmed to render rather than crash; `describeStrategy`'s output checked in both the result header and the history sidebar; a past MA run from before this step reopened from history and confirmed to still render (§2.5's migration, verified through the UI and not only in SQL).
