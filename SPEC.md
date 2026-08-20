# SPEC — Portfolio Backtests: Multi-Symbol, Single Strategy (Step 19)

Status: **Approved 2026-08-18, with §2.2 revised during review.** Six of §3's seven decisions stand as drafted; decision 1 (position sizing) was found to break this step's own headline invariant and was corrected before approval — see §2.2 and §5. Implementation is unblocked — not started.
Scope: `services/backtesting/internal/{service,store,handler}`, a new migration (`009_...`), and `frontend/src/{api/types.ts,backtesting/}`. No gateway change (`/backtests/*` is already proxied), no `market-data` change (its `History(symbol)` endpoint is called once per symbol, unchanged), no `auth`/`trading-engine` change.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase3-step18-rsi-macd-strategies/`.

---

## 1. Objective

`agents.md` §3's backtesting scope has one item left: multi-symbol / portfolio-level backtests. Step 16 deferred it as "a materially different simulator, not a small extension" and Step 18 deferred it again for the same reason. Both deferrals were correct — this step confirms it by actually building that different simulator.

**Objective:** `POST /backtests` accepts a *list* of symbols instead of one. One run applies the chosen strategy to every symbol independently, but all symbols draw from **one shared starting-capital pool** rather than each getting its own separate account, and the run's metrics are computed over the **combined portfolio's** equity curve — one Sharpe ratio, one max drawdown, one win rate for the whole run, not one per symbol.

**Framing that shapes every decision below:** a single-symbol backtest is not a separate mode this step adds a second engine next to. It is the `len(symbols) == 1` case of the one portfolio engine this step builds — the same relationship Step 18's `maCrossover` has to the general `Strategy` interface (a zero-behavior-change special case of something more general, proven by a byte-identical test, not a parallel code path). Today's single-symbol behavior is not preserved by a compatibility branch; it falls out of the general design for free, and a test asserts that it does.

**What this step is not about: `ComputeMetrics`.** It already takes a symbol-agnostic `[]float64` equity curve and a trade log — it has no idea today whether that curve came from one symbol or five. **It does not change.** All the new work is in how the equity curve and trade log get built from N symbols instead of one, plus the wire format, the schema, and the UI to carry a symbol list instead of a string.

**Non-goals:**
- **Per-symbol metrics.** No second Sharpe/return/drawdown broken out by symbol. `agents.md` §3 names five metrics for a *run*; this step keeps that meaning for a portfolio run, computed over the combined curve. Per-symbol activity is still visible in the trade log (now symbol-tagged, §2.5), just not in a second metrics block. A per-symbol breakdown is a plausible future step, not this one.
- **Rebalancing of open positions.** A position is never trimmed, topped up, or otherwise touched between its own entry and its own exit, and nothing is ever sold to fund a different symbol's entry. New entries *are* sized off current equity (§2.2) — but that is position *sizing*, exactly what today's single-symbol engine already does, not rebalancing.
- **Per-symbol strategies or parameters.** One strategy, one set of parameters, applied uniformly to every symbol in the run — the same "one strategy per run" framing Step 18 §1 already established for the single-symbol case, extended rather than revisited.
- **Correlation-aware position sizing.** No covariance, no risk parity, no "less capital to symbols that move together." Equal fixed-fraction allocation only (§2.2).
- **Short selling.** Unchanged from Step 16/18 — long-only, one position per symbol at a time.
- **Intraday timeframes, equity-curve charting, parameter sweeps.** Unchanged non-goals carried forward from Step 16 §1 and Step 18 §1; nothing here revisits them.
- **A compatibility shim for the singular-`symbol` wire shape.** See §2.5 — breaking on purpose, no shim, same stance as Step 18 §2.6.

---

## 2. Design decisions

### 2.1 Bar alignment: the intersection of every symbol's available dates

Today, one symbol's bars are fetched, ranged to `[start_date, end_date]`, and simulated bar by bar. With N symbols, the simulator needs one synchronized timeline — a bar index `i` has to mean "the same trading day" for every symbol simultaneously, or mark-to-market equity and same-bar cash contention (§2.2) have no coherent meaning.

**(a)** Union of dates — any date any symbol has a bar for is a simulated day; symbols missing a bar that day carry their last-known price forward.
**(b)** Intersection of dates — only a date every requested symbol has a bar for is simulated; nothing is carried forward.

**Recommendation: (b).** In practice, US equities mostly share a trading calendar, so union and intersection agree almost always — but a newly-ingested symbol or a gap in `market-data`'s ingestion (not something this service controls) can break that. (a) means inventing a "last known price" carry-forward rule this codebase has never needed before, and staleness of an unknown, unbounded age silently entering both the fill price and the equity curve. (b) needs no new concept: every bar in the aligned timeline is a real, same-day price for every symbol, exactly like today.

**If a requested symbol has no ingested history at all, the whole request fails** — `ErrSymbolUnavailable`, naming the specific symbol, not a silent drop to the N-1 symbols that do exist. A user who configured 4 symbols and gets a portfolio run over 3 with no indication one was skipped is the "half-answered request" failure mode this codebase consistently avoids (Step 16/17/18's error handling never degrades quietly). If the *intersection* of otherwise-available symbols' dates is empty within the requested range, `ErrDateRangeUnavailable` — the same sentinel Step 16 already defines, its meaning generalized from "this symbol has no bars in range" to "no date has a bar for every requested symbol," with no new error type needed.

The existing warm-up check (`Strategy.WarmupBars() > len(ranged)`, `backtest.go:47`) runs **once, against the aligned timeline's length**, not per symbol — after alignment every symbol has exactly that many bars by construction, so there is nothing per-symbol left to check.

### 2.2 Shared-pool position sizing: an equal share of *current equity*, capped by available cash, sells refill the pool

This is the step's real new problem. `Simulate` today is all-in, one position: a sell liquidates 100% of the position, and a buy spends 100% of **current** cash — `quantity = cash / bar.Open` (`simulate.go:36`), where `cash` is the running balance, *not* a fixed fraction of `starting_capital`. That distinction is the whole reason today's engine **compounds**: a profitable round trip leaves more cash behind, and the next entry is correspondingly larger. Whatever replaces this rule has to keep that property, because losing it is not a simplification — it is a silent behavior regression. And the rule cannot survive contact with more than one symbol as written: if two symbols both signal buy on the same bar, "spend all the cash" cannot mean the same thing twice.

**(a)** Fixed per-symbol sub-budgets: split `starting_capital` into N equal, permanently separate buckets at the start, one per symbol; each symbol trades only within its own bucket, forever. Equivalent to running N independent single-symbol backtests and summing the results for display.
**(b)** A shared pool with a *static* target: each symbol's target allocation is `starting_capital / N`, capped by one common cash balance — a sell in one symbol returns cash to the same pool a different symbol's later buy can then spend.
**(c)** A shared pool with a target read off *current equity*: each symbol's target allocation is `current_total_equity / N`, capped by that same common cash balance.

**Recommendation: (c).**

(a) is not actually a shared pool — it is N independent backtests wearing one report, which is the "batch of independent runs" scope this step was explicitly chosen over.

**(b) is wrong, and this step's own headline invariant is what proves it.** Take N=1 and `starting_capital = 10,000`: buy at 100 (spends 10,000, 100 shares), sell at 120 (cash = 12,000), buy again at 110. Today's `Simulate` spends all 12,000 → 109.09 shares. Rule (b) spends `min(12,000, 10,000)` = 10,000 → 90.91 shares, and the remaining 2,000 sits idle for the rest of the run, unspendable by anyone. Different trades, different equity curve, different metrics. §4's "N=1 is byte-identical to today's engine" test fails on the first fixture containing two profitable round trips — and it is right to fail. Nor is this an N=1 curiosity: (b) disables compounding at *every* N, so a strategy that returns X as a single-symbol run returns less than X as a one-symbol "portfolio" run, for no reason a user could be told with a straight face.

**(c) preserves compounding and the N=1 collapse, and its one apparent cost does not survive inspection.** The objection to reading the target off running equity is that an entry's size becomes path-dependent on every other symbol, making a fixture's expected numbers hard to hand-verify — the "trust a fixture, not self-consistency" risk Step 18 §4 flagged. But **total equity at a bar's open is invariant across that bar's buys**: a buy converts `x` cash into `x` worth of shares at the *same* open price it is priced at, so it moves nothing. The target is therefore computed **once per bar**, before any buy, and every buy at that bar shares it — one number per bar to hand-check, not one per fill. Today's engine is already fully path-dependent in exactly this sense (cash after a round trip depends on both fill prices) and its tests hand-verify fine.

**Mechanics — one shared-cash loop over the aligned timeline (§2.1). At each bar, in this order:**

1. **Every sell first.** For each symbol holding a position whose previous bar signalled sell: liquidate the entire position at this bar's open, exactly like today's single-symbol rule; proceeds return to the **shared** cash balance, not to a symbol-specific bucket — this is the one line that makes it a pool and not N sub-accounts. **Sells before buys, deliberately:** it is what makes the pool genuinely shared *within* a bar and not merely across bars, and it means that behavior does not depend on where the seller happens to fall in alphabetical order.
2. **This bar's target, computed once:** `target := equityAtOpen / N`, where `equityAtOpen = cash + Σ over held symbols(quantity_s × open_s)`, evaluated after step 1. Once per bar is *exact*, not an approximation — see the equity-invariance argument above.
3. **Buys**, for symbols currently flat whose previous bar signalled buy, in **alphabetical order by symbol**: `spend := min(cash, target)`. If `spend > 0`, buy at this bar's open with `quantity = spend / open`, `avgCost = open`, `cash -= spend`. **If `spend == 0`** (symbols earlier in the order already exhausted the pool), the signal produces no fill — the same "a signal that doesn't result in a trade is a legitimate, already-tested outcome" convention `Simulate` already has for "buy while already holding."
4. **Equity at the bar's close:** `cash + Σ over held symbols(quantity_s × close_s)` — the direct N-symbol generalization of today's `cash + quantity*bar.Close`.

**Why alphabetical, not request order:** request order is whatever a user happened to type into a comma-separated field (§2.7); alphabetical is deterministic independent of how the same three symbols happen to get typed on a given run, so two runs configured with the same symbol *set* in a different *order* produce identical trades — a property worth having for free.

**Why this is not the rebalancing ruled out in §1:** no open position is ever trimmed or topped up, and nothing is ever sold to fund something else. Only the size of a *new* entry is read off current equity — which is precisely, and only, what today's engine already does at N=1.

**The N=1 case is exactly today's engine, by construction rather than by coincidence:** with one symbol a buy only ever happens while flat, so `equityAtOpen == cash`, `target = cash / 1 = cash`, and `spend = min(cash, cash) = cash` — literally today's `quantity = cash / bar.Open`, compounding included. A sell liquidates the whole position, as today. Verified with a test that runs the new engine and today's `Simulate` against the same single-symbol fixture — one containing **at least two profitable round trips**, so compounding is actually exercised and rule (b)'s failure mode could not slip through — asserting byte-identical trades, equity curve, and final equity (§4).

### 2.3 One `SimulatePortfolio`, no separate single-symbol code path

Given §2.2's N=1 collapse, keeping today's `Simulate` as a second, parallel implementation would mean two functions that are supposed to agree forever but are free to drift, verified only by a test that happens to check they still do. **Recommendation: `SimulatePortfolio` replaces `Simulate` outright** — every call site (there is exactly one, `RunBacktest`) always calls the one function, for any `N >= 1`. `TradeRecord` gains a `Symbol string` field, populated for every trade including an N=1 run's — the same "one honest shape, no sparse/conditional fields" stance Step 18 §2.5 already took against nullable per-strategy columns, applied here to "does this trade record need a symbol."

`ComputeMetrics` receives whatever `SimulatePortfolio` returns exactly as it does today — a `SimulationResult{Trades, EquityCurve, FinalEquity}` — and needs no change at all, at any N (§1's framing, made concrete).

### 2.4 A cap on symbol count: 10

Nothing today bounds how many symbols one request could name. Left unbounded, three things degrade together: `target = starting_capital / N` shrinks toward amounts too small to buy even one fractional share meaningfully at some point (this simulator already supports fractional quantities, so it would not error, just become numerically silly), the comma-separated input (§2.7) becomes unwieldy to type and to read back, and N sequential-or-concurrent upstream fetches to `market-data` (§2.6) add latency to a request Step 16 §1's own non-goal insists stays "a sub-second computation."

(The first of those is stated in §2.2's terms as `equityAtOpen / N`; the shrinkage argument is the same at any N and does not depend on which target rule §2.2 settled on.)

**Recommendation: reject a request naming more than 10 symbols with `ErrInvalidRequest`.** No principled number exists — this is a starting guess sized to keep the UI and the output readable, revisit if it turns out to bind. Duplicate symbols (case-insensitive, after the same uppercasing `validateRequest` already applies to the single-symbol case) are also rejected outright rather than silently deduplicated, matching this codebase's consistent "ambiguous input is an error, not a guess" stance.

### 2.5 Persistence: `backtests.symbol` → `symbols TEXT[]`; `backtest_trades` gains `symbol`

`backtests.symbol TEXT NOT NULL` describes exactly one symbol. `backtest_trades` has no symbol column at all today, because there was only ever one symbol a trade could belong to.

**`backtests.symbol` becomes `symbols TEXT[] NOT NULL`.** A native Postgres array, not a join table — the same reasoning Step 18 §2.5 used for JSONB `params` applies here even more simply: this list is only ever read back *whole*, to redisplay one run, never filtered or aggregated on (no "find every backtest that touched AAPL" feature is planned or implied by anything in `agents.md`). A join table would buy queryability nothing here needs, at the cost of a second table and a join on every read.

**`backtest_trades` gains `symbol TEXT NOT NULL`.** Every trade now belongs to one of the run's symbols (§2.3); the column is never nullable — there is no trade this engine can produce that lacks a symbol, at any N.

Migration `009_backtest_portfolios.up.sql`:

```sql
ALTER TABLE backtest_trades ADD COLUMN symbol TEXT;
UPDATE backtest_trades t SET symbol = b.symbol
    FROM backtests b WHERE t.backtest_id = b.id;
ALTER TABLE backtest_trades ALTER COLUMN symbol SET NOT NULL;

ALTER TABLE backtests ADD COLUMN symbols TEXT[];
UPDATE backtests SET symbols = ARRAY[symbol];
ALTER TABLE backtests ALTER COLUMN symbols SET NOT NULL;
ALTER TABLE backtests DROP COLUMN symbol;
```

The trade backfill has to run **before** `backtests.symbol` is dropped, since it joins against it — the order above is load-bearing, not incidental.

**The down migration is intentionally not a round trip, the same pattern Step 18's `008_....down.sql` already established for an analogous problem:** the pre-Step-19 schema cannot represent a run over more than one symbol. `009_backtest_portfolios.down.sql` deletes any backtest whose `symbols` array has length other than 1 (cascading to its `backtest_trades` rows, same `ON DELETE CASCADE` Step 16 already set up), then restores `symbol TEXT` for the survivors from `symbols[1]`, then drops both `symbols` and `backtest_trades.symbol`. Documented in-file exactly like `008`'s was, and run against a throwaway database holding a mixed row set (one single-symbol row, one 3-symbol row) before being trusted, not just written — the same verification Step 18 §2.5 performed for its own down migration.

**No `CHECK (array_length(symbols, 1) BETWEEN 1 AND 10)`.** §2.4's cap is enforced once, in the service layer, at request time — the same "the service layer is the sole validation authority; this table has no `CHECK` constraints today" reasoning Step 18 §2.5 already gave for not constraining `strategy`.

### 2.6 Fetching N symbols' history: concurrent, bounded by §2.4's cap

`HistoryClient.History(ctx, symbol)` is unchanged — one call per symbol, exactly as today. The only new question is whether `RunBacktest` calls it N times serially or concurrently.

**Recommendation: concurrently**, via `golang.org/x/sync/errgroup` (confirmed already present as `golang.org/x/sync v0.17.0 // indirect` in `services/backtesting/go.mod`; this step promotes it to a direct requirement, not a new external dependency to evaluate). At up to 10 symbols, N serial HTTP round-trips risks visibly violating the "sub-second" non-goal in a way N concurrent ones does not. `ErrSymbolUnavailable`/`ErrUpstreamUnavailable` keep their existing meaning unchanged — only *how many calls happen at once* changes, not what any individual call can return.

**One consequence worth designing for rather than discovering: which error surfaces must not be a race.** `errgroup`'s "first error wins" is decided by goroutine scheduling, so a request naming two unavailable symbols would report a nondeterministic one of them, and §4's "names the specific symbol" test would flake. Instead: collect every symbol's result, and if any failed, return the failure for the **alphabetically first** failing symbol. Same cost, deterministic, and it matches the ordering §2.2 already establishes for everything else in this engine.

### 2.7 Wire format: `symbol string` → `symbols []string`, breaking, no shim

**(a)** `symbols: []string` on both the request and the response, replacing `symbol: string`.
**(b)** Keep `symbol: string` for the single-symbol case, add an optional `additional_symbols?: string[]` alongside it for the rest.

**Recommendation: (a).** (b) is exactly the kind of asymmetric, conditionally-meaningful shape Step 18 §2.6 already rejected once (there, for `short_window`/`rsi_period`/etc. all top-level and mostly meaningless per request) — here it would mean two ways to say "one symbol" (`symbol: "AAPL"` or `symbols: ["AAPL"], additional_symbols: []`) that always have to be reconciled by both client and server. §1's framing — a single symbol is `len(symbols) == 1`, not a separate case — argues for exactly one field, always a list, never a string.

**This is a breaking change to `POST /backtests` and to the `Backtest`/`BacktestDetail`/`TradeRecord` response shapes, taken deliberately with no compatibility shim** — the same stance and the same reasoning as Step 18 §2.6: the only client is this repository's own frontend, updated in the same step (§2.8), there is no API version and no external consumer.

### 2.8 One combined step again, despite the larger size

Steps 14→15 and 16→17 split backend and frontend; Step 18 combined them because the wire break meant a backend-only merge would leave the existing frontend reading fields (`short_window`/`long_window`) that no longer exist. **The identical argument applies here** — a backend-only Step 19 would land a response with `symbols` where `BacktestResult.tsx`/`BacktestHistoryList.tsx`/`TradeLogTable.tsx` still read `symbol`, breaking the dashboard exactly the way Step 18 §2.7 described.

**Recommendation: keep it combined, the same call Step 18 made, for the same reason.** This step's frontend surface is larger than Step 18's was (§2.9 needs a new symbol-list input, not just a `<select>`; `TradeLogTable.tsx` needs a new column), which is real and worth flagging explicitly to Khalil rather than asserting away — **if the combined diff feels too large in review, the honest split is the same shape Step 18 §2.7 already suggested for itself and didn't need: backend end-to-end behind a route the frontend doesn't call yet, frontend as a follow-up step that starts calling it.** That keeps every merge to `main` in a working state, unlike a layer split.

### 2.9 Frontend: one comma-separated symbol field, no new input component

**(a)** A single text input accepting a comma-separated symbol list (`"AAPL, MSFT, GOOGL"`), split/trimmed/uppercased/deduped client-side by `backtest-validation.ts`, the same string-parse-then-typed-validate shape every other field in that file already has.
**(b)** A repeatable list of individual symbol inputs with an "add symbol" / "remove" control.
**(c)** A multi-select dropdown sourced from `GET /market-data/symbols`.

**Recommendation: (a).** (b) and (c) are real UI components this codebase has never needed before — no chip/tag input, no multi-select, no dynamic add/remove row pattern exists anywhere in `frontend/src` today, and introducing one is a meaningfully bigger frontend lift than the backend work this step is actually about. (a) reuses exactly the pattern `backtest-validation.ts` already has for every numeric field: a string in `BacktestFormValues`, parsed and bounds-checked in one pure function, with its own unit tests per boundary (empty, one symbol, exactly 10, 11, a duplicate, mixed case duplicates). A single symbol typed with no comma is `symbols: ["AAPL"]` — no separate "single mode" UI at all, matching §1.

**`TradeLogTable.tsx` gains a `Symbol` column** — trades are no longer implicitly single-symbol, so the table needs to say which symbol each row belongs to; `BacktestResult.tsx`'s header and `BacktestHistoryList.tsx`'s row need whatever currently formats one symbol to instead join the list (`"AAPL, MSFT, GOOGL"`), reusing the existing strategy-description formatter (`describeStrategy`, Step 18) unchanged — it never depended on symbol count.

### 2.10 Unchanged by design, and worth stating

- **`ComputeMetrics`, all five metrics, the next-bar-open fill rule's *meaning*, `NewStrategy`/the `Strategy` interface, all three strategies.** Untouched. A strategy still produces one `[]Signal` per symbol from that symbol's own bars — specifically, **from that symbol's slice of the aligned timeline (§2.1), not from its full unaligned history**. That is not a detail: signal index `i` has to correspond to aligned bar index `i` for the shared-cash loop to be coherent at all, and it is also what makes the N=1 collapse exact, since at N=1 the aligned timeline *is* today's `sliceRange` output. Nothing about *which* strategy or *how* it decides changes at any N.
- **`tradeStats`' comment** (`metrics.go:101-105`) reasons from "§2.3's all-in rule means there is never a partial close." Still true per symbol — a sell always liquidates that symbol's whole position (§2.2) — but the comment should be reworded to say so explicitly, since "all-in" no longer means "all of the account." A comment edit, not a logic change.
- **`POST /backtests` stays synchronous.** §2.4's cap plus §2.6's concurrent fetch are exactly what keeps that true at N > 1.
- **The gateway, auth, and ownership scoping.** No change — `/backtests` routing, `pkgauth.RequireAuth`, and `(id, user_id)` scoping in the store are all symbol-count-agnostic already.

---

## 3. Decisions — all approved

1. **Shared-pool position sizing** (§2.2): per-symbol target of `equityAtOpen / N`, computed once per bar, capped by available shared cash; sells processed before buys at each bar and returning cash to the same pool; alphabetical order among competing buys. **Revised during review** — the draft specified a static `starting_capital / N` target, which silently disables the compounding today's engine has and would have failed this step's own N=1 byte-identical test. See §5.
2. **Bar alignment is the intersection of symbols' available dates**, not a union with stale-price carry-forward (§2.1). A wholly-unavailable symbol fails the whole request rather than silently dropping it.
3. **Symbol cap of 10 per run, duplicates rejected outright** (§2.4) — a starting guess, not a derived number.
4. **`backtests.symbol` → `symbols TEXT[]`; `backtest_trades` gains `symbol TEXT NOT NULL`** (§2.5), migration `009`'s down direction a deliberate non-round-trip for portfolio rows, matching `008`'s own precedent.
5. **One combined step, backend and frontend together** (§2.8), accepting a larger diff than Step 18's rather than inventing a shim to split it.
6. **Frontend symbol input is one comma-separated text field** (§2.9) — no new multi-select or tag-input component.
7. **No per-symbol metrics breakdown** — aggregate-only `Metrics`, unchanged shape (§ Non-goals); per-symbol visibility comes from the trade log's new `Symbol` column only.

---

## 4. Verification plan

Same posture as Steps 16–18: unit tests on the pure computation, mutation-tested to prove they are a real net; integration tests against real Postgres; a manual adversarial browser pass before close-out; an independent adversarial review before merge (Step 18's own precedent — it found a real bug there and should run again here).

**The standout invariant this step gets that Step 18's indicators didn't: N=1 must be byte-identical to today's engine.** Unlike a wrong indicator (Step 18 §4's stated risk — plausible-looking output, no self-evident invariant), `SimulatePortfolio` has a hard, mechanically checkable anchor: run it with one symbol against a fixture already covered by today's `Simulate` tests, assert the trade log, equity curve, and final equity match exactly. This should be one of the first tests written, before any multi-symbol behavior is — the same "prove the general case reduces to the known-good specific case" discipline Step 18 §4 used for `maCrossover`'s move behind the `Strategy` interface.

- **`SimulatePortfolio`** — the N=1 collapse (above), on a fixture with **at least two profitable round trips** so compounding is exercised; a two-symbol same-bar contention case where the second symbol (alphabetically) gets a partial or zero fill because the first exhausted the pool; a sell in one symbol freeing cash another symbol's **same-bar** buy then spends, with the seller sorting *after* the buyer alphabetically (the test that proves both that this is a shared pool and that §2.2's sells-before-buys ordering is doing its job — under a naive single alphabetical pass it would fail); a target that visibly grows after a winning trade and shrinks after a losing one (the direct test of `equityAtOpen / N` over the rejected static target); equity-curve aggregation with symbols on divergent price paths.
- **Bar alignment** — intersection logic with a deliberate gap in one symbol's fixture calendar; the wholly-unavailable-symbol and empty-intersection error cases.
- **Request validation** — the 10-symbol cap at and just past the boundary, case-insensitive duplicate rejection.
- **Migration `009`** — both directions run against a throwaway database holding a mixed row set (a pre-existing single-symbol row, a portfolio row inserted directly), not just written: up backfills `symbols`/`backtest_trades.symbol` correctly for both; down deletes the portfolio row and its trades, restores `symbol` for the survivor.
- **Store integration** — a 3-symbol run's trade log round-trips through Postgres with each trade's `symbol` intact.
- **Mutation testing** — the four highest-value new controls: the shared-cash cap (`min(cash, target)`), the target expression itself (mutating `equityAtOpen / N` to `startingCapital / N` must fail a test — that is the review finding in §5 turned into a permanent guard), the sells-before-buys ordering, and the 10-symbol/duplicate validation.
- **Upstream fetch determinism** — two unavailable symbols in one request reports the alphabetically first, repeatably across runs (§2.6).
- **Manual browser pass** — a real portfolio run (2–3 symbols actually ingested in the dev database) end to end through the dashboard: the comma-separated input, a same-bar contention case if the fixture data allows constructing one, the trade log's new `Symbol` column, a pre-Step-19 single-symbol run reopened from history still rendering correctly through the new `symbols`-array response shape.
- **Independent adversarial review before merge** — Step 18's own precedent (it caught a real integer-overflow bug there); this step's shared-cash arithmetic and array-based schema are at least as good a place for a fresh set of eyes to find something.

---

## 5. Review record

Reviewed against the implementation before approval, rather than on its own internal consistency. Five findings; one changed a decision.

1. **§2.2's static target was wrong (decision-changing).** The draft read today's `Simulate` as all-in on *starting capital*. It is all-in on *current cash* (`simulate.go:36`, `quantity = cash / bar.Open`), which is what makes the engine compound. A fixed `starting_capital / N` target would have stranded every gain as unspendable idle cash, contradicted §1's framing, and failed §4's own first test. Corrected to `equityAtOpen / N`. The draft had already rejected this option as too hard to hand-verify; that objection collapses once you notice a buy is equity-neutral at the open, so the target is one number per bar, not one per fill.
2. **Same-bar sells must precede same-bar buys (§2.2).** The draft's single alphabetical pass made "a sell frees cash another symbol's buy spends on the same bar" work only when the seller happened to sort first — while §4 listed exactly that as a test. Ordering is now explicit.
3. **Concurrent fetch made error selection a race (§2.6).** `errgroup`'s first-error-wins is scheduling-dependent; with two bad symbols the "names the specific symbol" assertion would flake. Now deterministic on alphabetical order.
4. **Signals must be generated from the aligned timeline, not each symbol's full history (§2.10).** Left ambiguous in the draft, and it is load-bearing for both loop coherence and the N=1 collapse.
5. **Two accuracy notes**, no design impact: the warm-up check applies once to the aligned length (§2.1), and `tradeStats`' "all-in" comment needs rewording now that all-in is per-symbol (§2.10).

Verified as accurate as drafted: `golang.org/x/sync` really is already an indirect dependency (§2.6); `backtest_trades` really does have `ON DELETE CASCADE` from migration `007`, so §2.5's down-migration plan works as described; `008`'s up/down pair really is the precedent §2.5 claims, non-round-trip and verified-in-a-scratch-database included; and `ComputeMetrics` really is symbol-agnostic and needs no change (§1).
