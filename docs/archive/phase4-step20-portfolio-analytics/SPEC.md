# SPEC — Portfolio Analytics: the AI Insights Service, Rule-Based (Step 20)

Status: **Approved 2026-08-20, as drafted.** All seven §2 recommendations stand, and all four §6 open questions were resolved in favour of the recommendation: `pkg/portfoliomath` is extracted (§6.1), the five behavioural thresholds ship at their drafted values (§6.2), the cache TTL stays at 5 minutes (§6.3), and the reconstruction window starts at the first trade (§6.4). Implementation is unblocked — not started. Plan at `tasks/plan.md`.
Scope: a new `services/ai-insights` module; one new endpoint on `services/trading-engine` (`GET /trading/trades`); an extraction of three statistical primitives out of `services/backtesting/internal/service/metrics.go` into `pkg/`; `services/gateway` route + env; `go.work`, `Makefile`, `.env.example`. **No migration, no new table, and no frontend** (§3).

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase3-step19-portfolio-backtests/`.

---

## 1. Objective

Phase 3 closed with Step 19. `agents.md`'s Phase 4 opens with *portfolio analytics* and *insight generation*, and `agents.md` §4 splits that work explicitly: **Phase 1 — rule-based analytics, SQL/statistical computations. Phase 2 — LLM-generated insights.** This step is that first half, and nothing else.

**Objective:** stand up `services/ai-insights` as the sixth service and serve **one endpoint** — `GET /insights/portfolio` — returning a deterministic, fully-derived analysis of the authenticated user's live paper-trading portfolio across three sections: **risk**, **benchmarking**, and **behavior**.

**The framing that shapes every decision below: every number in the response is computed, reproducible, and traceable to a trade or a stored bar.** No number is estimated, sampled, or generated. This is not a stylistic preference — it is what makes the deferred LLM step (§3, `agents.md` §4 "Phase 2") safe to build later. When narrative generation arrives, it will be handed this object and permitted to *phrase* it, never to produce a figure of its own. A rule-based core that is merely "mostly right" would remove that guarantee before it is ever claimed.

**The problem this step actually has to solve:** none of the interesting metrics — volatility, Sharpe, drawdown, benchmark comparison — are functions of the *current* portfolio. They are functions of the portfolio's **history**, and QuantSim stores no portfolio history. `accounts.balance` and `positions` are a single mutable snapshot of *now*; there is no daily equity series anywhere in the schema. §2.1 is where that gets solved, and it is the load-bearing decision of the step.

**Non-goals:**
- **LLM-generated narrative.** Deferred to a later step, by design, not by omission — see §3 and §2.4's response shape, which exists partly to be that step's input.
- **Sector exposure and a diversification score computed from sectors.** `agents.md` §4 names it; there is no sector data anywhere in this repo, and the tradable watchlist is five names plus two ETFs. See §2.5 — replaced with a concentration measure that needs no data QuantSim does not have, and recorded as a deliberate substitution rather than a gap.
- **Any frontend.** Step 16 → 17 and Step 18 established the pattern: the engine lands, the UI follows in its own step. Insights UI is Step 21.
- **Backtest analytics.** This service analyzes the *live* portfolio (`accounts`/`positions`/`orders`/`trades`). The `backtests` tables are Phase 3's and are not read here. Strategy evaluation against backtest runs is a plausible later step.
- **Any write path.** `ai-insights` is read-only with respect to every datastore in the system. It owns no table (§2.9) and mutates nothing.
- **Persistence of computed insights.** Redis cache only (§2.8), which is a cache and not a record — losing it costs a recomputation and nothing else.
- **Dockerization and cloud deployment.** The other half of Phase 4, and a separate spec.

---

## 2. Design decisions

### 2.1 The equity curve is reconstructed from trades and stored bars, not stored

There is no portfolio time series. Three ways to get one:

**(a) Reconstruct it on demand** from the trade log plus `market-data`'s stored daily bars, every time it is needed.
**(b) Snapshot it** — a `portfolio_snapshots` table plus a scheduled job that writes one row per account per day.
**(c) Skip it** — serve only point-in-time metrics that need no history (current weights, current concentration, trade counts).

**Recommendation: (a).**

(c) is the cheapest and deletes most of the value: no volatility, no Sharpe, no drawdown, no benchmark comparison — which is four of the five things `agents.md` §4 actually asks for.

(b) is what a production system with real accounts would do, and it is wrong *here* for a specific reason rather than a general one: **a snapshot job can only record the future.** It starts producing data the day it is deployed, so every existing account has an empty history and the feature is blank on arrival — worst on exactly the accounts with the most trading behind them. It also introduces a scheduler, a table, a backfill question, and staleness semantics, all to store something that is already exactly derivable from data the system has.

**(a) is exact, not an approximation, and that is the point.** Both components of equity are fully determined by data already stored:

- **Cash is exact.** Every account opens at `StartingBalance = 100000.00` (`services/auth/internal/service/auth.go:23`), set once at registration. There are **no deposits, no withdrawals, no fees, and no interest** in QuantSim — every cash movement that can ever occur is a trade. So for any date `t`:
  `cash(t) = 100000 - Σ(buy.quantity × buy.price for executed_at ≤ t) + Σ(sell.quantity × sell.price for executed_at ≤ t)`
- **Holdings are exact.** `holdings(symbol, t) = Σ(±quantity for that symbol, executed_at ≤ t)`, signed by side.
- **Valuation is stored.** `market-data` holds ~501 daily bars per watchlist symbol; `close(symbol, t)` is a lookup.

`equity(t) = cash(t) + Σ over held symbols(holdings(symbol, t) × close(symbol, t))`.

**A correctness check worth building the implementation around:** reconstruction evaluated at *today* must reproduce `accounts.balance` and the live `positions` rows. It is derived from the same trades that produced them, so any divergence is a bug in one of the two — and it is a free, permanent, self-checking assertion over real data. §4 makes it a test.

**The window:** from the date of the account's **first trade** through the **most recent date the calendar (below) contains**. Before the first trade the curve is a flat 100,000 carrying no information; including it would drag every return, volatility and drawdown figure toward zero in proportion to how long the account sat idle before it was used, which reports the age of the account rather than the behavior of the portfolio.

**The calendar:** the **intersection** of `SPY`'s stored bar dates and the stored bar dates of every symbol the account has **ever held** — not just currently holds, since a symbol bought and fully sold in March is part of March's equity. `SPY` is in the intersection because it is the benchmark (§2.6) and must be measured over exactly the same days as the portfolio; comparing two series sampled on different dates is not a comparison.

Intersection, and specifically **no carry-forward of a missing bar**, is Step 19 §2.1's rule applied unchanged. The reasoning carries over intact: a carry-forward rule invents a "last known price" concept this codebase has never had, and lets staleness of unknown, unbounded age enter the equity curve silently. Every date in the reconstructed curve is a date on which a real close exists for every relevant symbol.

**If a held symbol has no stored history at all, the request fails** — `symbol_unavailable`, naming the symbol — rather than reconstructing a curve that quietly omits one of the account's positions. Same stance as Step 19 §2.1: this codebase does not half-answer.

### 2.2 Trade history: a new `GET /trading/trades` on trading-engine

`ai-insights` needs the full trade log. `trading-engine` owns `trades` and exposes `/orders`, `/positions`, `/portfolio` — none of which return trades, and `trades` is where `realized_pl` and every behavioral signal live.

**(a)** Add `GET /trading/trades`; `ai-insights` consumes it over HTTP.
**(b)** Give `ai-insights` its own read-only Postgres connection onto `trades`/`positions`/`accounts`.
**(c)** Have `trading-engine` compute and expose the aggregates; `ai-insights` only interprets them.

**Recommendation: (a).** It is the rule this repo has held to without exception: **a service owns its tables and calls HTTP for anyone else's.** `backtesting` owns `backtests`/`backtest_trades` and calls `market-data` over HTTP for bars; `trading-engine` does the same. (b) is a shared-database coupling in which a `trading-engine` migration can silently break `ai-insights` with no compile error and no failing test in either module — precisely the failure this convention exists to prevent. (c) scatters analytics across two services and grows `trading-engine`'s API once per insight.

The honest cost of (a) is that aggregation is SQL-shaped work being done in Go over JSON. At this data volume — paper trading, twenty dev accounts — it does not matter, and the boundary is worth more than the microseconds. If the volume ever changes, (c) is the escape hatch, and it is reachable without redesigning anything.

**Shape**, mirroring the existing `/trading/orders` handler and `market-data`'s `History` limit convention (`DefaultHistoryLimit`/`MaxHistoryLimit`, `market_data.go:23`):

```
GET /trading/trades?limit=N
→ 200 {"trades":[{"id","symbol","side","quantity","price","realized_pl","executed_at"}]}
```

Scoped to the caller's account by the injected user ID, exactly like every other `/trading/*` route. `limit` defaults to 1000, caps at 10000, and `ai-insights` requests the cap — the same "ask for more than exists so the client always sees the whole series" reasoning as `backtesting`'s `historyLimit = 2000` (`market_data_client.go:26`). An empty account returns `{"trades":[]}`, never `null` — `trading-engine` already has that convention and a test for it (`TestPortfolio_EmptyPortfolioHasAnEmptyPositionsArray`).

**Ordering, and a limit worth writing down rather than discovering later.** `ORDER BY executed_at ASC, id ASC`. Unlike Step 19's R1 bug, `executed_at` here is a real wall-clock `TIMESTAMPTZ` that genuinely determines sequence for orders placed one at a time, and `id` breaks exact ties so the output is stable across reads. But Step 19's actual lesson — *an `ORDER BY` over row values cannot express a sequence those values don't determine* — still applies to the tie case: two trades sharing a timestamp are returned in UUID order, which is not execution order. **Every consumer in this step aggregates by day and is therefore insensitive to within-timestamp ordering** (§2.1, §2.5–2.7). That is a property this step relies on, so §4 tests it rather than assuming it, and any future consumer that needs true execution order needs a stored sequence — the fix Step 19 already applied to `backtest_trades.seq`.

### 2.3 The shared statistical primitives move to `pkg/`

`backtesting`'s `metrics.go` already contains correct, tested, non-obvious implementations of `sharpeRatio`, `maxDrawdownPct`, `stdevOf` and `meanOf` — including two decisions that took thought and are documented in place: population stdev so a single-return curve yields `0` rather than a divide-by-zero, and a drawdown that is `0` rather than negative on a monotonically rising curve. `ai-insights` needs all four, over a different curve.

**(a)** Extract them to a new `pkg/` package; both services import it.
**(b)** Reimplement them in `ai-insights`.

**Recommendation: (a) — `pkg/portfoliomath`.** (b) produces two Sharpe ratios in one system that are supposed to agree forever and are free to drift, verified by nothing. Step 19 §2.3 rejected exactly this shape of duplication for `Simulate`/`SimulatePortfolio` and deleted the second copy; the same reasoning applies before the second copy exists. It also means the two documented edge-case decisions above are either duplicated (and can diverge) or silently dropped in the new copy.

The package takes `[]float64` and returns `float64` — no domain types, no knowledge of backtests or portfolios. `tradingDaysPerYear = 252` moves with it.

**This modifies a service that is otherwise out of scope, so it is bounded explicitly:** `ComputeMetrics` keeps its signature, its behavior, and its tests. The change is that its private helpers become calls into `pkg/portfoliomath`. §4 requires the existing `metrics_test.go` to pass **unmodified** — if it needs editing, the extraction changed behavior and is wrong.

**A risk-free rate of 0** is inherited, not introduced: `sharpeRatio` already assumes it. Stated here because it now appears in a user-facing insight labeled "risk-adjusted return," where the assumption is worth being explicit about rather than buried.

### 2.4 One endpoint, one object, three sections

**Recommendation: a single `GET /insights/portfolio`** returning all three sections, rather than `/insights/risk`, `/insights/benchmarks`, `/insights/behavior`.

The expensive part of the work — fetching trades, fetching bars for every held symbol plus `SPY` and `QQQ`, and reconstructing the curve (§2.1) — is shared by all three sections. Three endpoints would repeat it three times, or share it through a cache and make three cache entries that can disagree with one another about the same portfolio at the same instant. One endpoint means one reconstruction, one cache entry (§2.8), and one `as_of_date` that is true of every number in the response.

It is also the shape the deferred LLM step wants: that step's input is *the whole analysis*, since a narrative that mentions risk without knowing the benchmark result is worse than no narrative.

```
GET /insights/portfolio
→ 200 {
    "computed_at": "2026-08-20T14:03:11Z",   // when this object was built
    "as_of_date":  "2026-08-19",             // last calendar date in the curve (§2.1)
    "window": {"start_date":"2026-05-02","trading_days":78},
    "risk":         { ... §2.5 ... },
    "benchmarking": { ... §2.6 ... },
    "behavior":     { ... §2.7 ... }
  }
```

`computed_at` and `as_of_date` are distinct on purpose and both are load-bearing: the first is cache age (§2.8), the second is data age. A response can be freshly computed from bars that are a day old, and conflating the two hides that.

### 2.5 Risk: concentration instead of sectors, plus volatility and drawdown

`agents.md` §4's Portfolio Risk block asks for sector exposure, a diversification score, and volatility analysis. **There is no sector data in this repo** — no column, no table, no provider field — and the tradable universe is `AAPL, MSFT, GOOGL, AMZN, TSLA` plus the `SPY`/`QQQ` ETFs, which are not a sector at all.

**(a)** Measure concentration from position weights; no sectors.
**(b)** Hardcode a symbol→sector map.
**(c)** Ingest real sector metadata into `market-data`.

**Recommendation: (a).** (b) is a fixture impersonating data: it would report "83% Technology" as though that were a measurement, and it goes stale silently the moment the watchlist grows. (c) is a real `market-data` step with a provider integration behind it, and it belongs to that service, not this one. (a) measures the thing sector exposure is a *proxy* for — undiversified risk — directly, from data that is exact.

```
"risk": {
  "positions": [{"symbol":"AAPL","market_value":18400.0,"weight_pct":17.6}, ...],
  "cash_weight_pct": 41.2,
  "concentration_hhi": 0.38,        // Σ w² over invested positions, 0..1
  "largest_position_pct": 34.1,
  "annualized_volatility_pct": 22.7,
  "max_drawdown_pct": 8.9
}
```

- **HHI** (Herfindahl-Hirschman) is `Σ wᵢ²` over **invested positions only**, with weights renormalized to the invested total so the number answers "how concentrated is the part that is actually at risk." `1.0` is a single holding; `1/n` is n equal holdings. Cash is excluded from it and reported separately as `cash_weight_pct` — folding cash in would make an all-cash portfolio look perfectly concentrated and *also* perfectly safe, from the same number.
- **Volatility** is the annualized population standard deviation of the reconstructed curve's daily returns (`stdevOf × √252`), from §2.3's shared primitive.
- **Max drawdown** is §2.3's `maxDrawdownPct` over the same curve — the same metric Phase 3 already reports for backtests, now over a real portfolio, deliberately identical so the two are comparable.

### 2.6 Benchmarking: buy-and-hold `SPY` and `QQQ` over the identical window

`SPY` and `QQQ` are both already in `DefaultWatchlist` (`market_data.go:16`) with roughly two years of stored daily bars, so this needs no new ingestion and no new data source.

**The comparison:** hypothetically place the account's full `StartingBalance` into the benchmark at the **first close in the reconstruction window** and hold to the last. That is `agents.md` §4's "vs S&P 500 / vs NASDAQ" and its "outperformance vs buy-and-hold" in one construction — for a live portfolio the two questions are the same question.

```
"benchmarking": {
  "portfolio_return_pct": 6.2,
  "portfolio_sharpe": 0.81,
  "benchmarks": [
    {"symbol":"SPY","label":"S&P 500","return_pct":4.1,"excess_return_pct":2.1,"sharpe":0.66},
    {"symbol":"QQQ","label":"NASDAQ 100","return_pct":5.4,"excess_return_pct":0.8,"sharpe":0.72}
  ]
}
```

Every figure is over the **same trading days** as the portfolio curve, which §2.1's calendar guarantees by construction rather than by a separate check. `excess_return_pct` is simple difference, not a regression alpha — a beta-adjusted alpha over a handful of positions and a short window would be a number with more decimal places and less meaning.

**Both benchmarks are non-optional.** If either lacks bars covering the window, the whole request fails with `symbol_unavailable` rather than returning a benchmarks array that is shorter than usual for a reason the caller cannot see (§2.1's stance again).

### 2.7 Behavior: three rules, explicit thresholds, evidence attached

The one section with no objectively correct definition. Each rule is therefore stated as an exact, testable computation over the trade log, with its threshold in one named constant block and its triggering trades attached to the finding.

**Attaching the evidence is a requirement, not a nicety.** It is what lets the deferred LLM step (§3) cite specifics without inventing them — the model receives the trades that fired the rule and can only describe those.

```
"behavior": {
  "trade_count": 34,
  "findings": [
    {"code":"overtrading","severity":"warn","turnover_ratio":3.4,
     "detail":"34 trades in the last 30 days, 3.4x average equity traded",
     "evidence_trade_ids":["...","..."]},
    {"code":"panic_selling","severity":"info","occurrences":3, "evidence_trade_ids":[...]}
  ],
  "risk_profile": "aggressive"
}
```

- **Overtrading** — over a trailing **30 calendar days**: `turnover = Σ(quantity × price) over all trades / mean(daily equity)` in that window. Flagged above **2.0**. Turnover rather than a raw count because ten trades on a 100k portfolio and ten trades of 100 dollars each are not the same behavior, and a count cannot tell them apart. The raw count is reported alongside anyway, because it is what a user recognizes.
- **Panic selling** — a sell that is **both** (i) executed on a date whose *previous* trading day closed **≥ 5% down** for that symbol, and (ii) booked a **negative `realized_pl`**. Both conditions are required: selling into a drop at a profit is taking a gain, and selling at a loss on a calm day is ordinary rebalancing. Flagged at **≥ 3 occurrences** or **≥ 30% of all sells**.
- **Risk profile** — a band, not a score, derived from §2.5's two figures: `aggressive` if `annualized_volatility_pct > 25` **or** `concentration_hhi > 0.5`; `conservative` if `< 12` **and** `< 0.25`; `moderate` otherwise. A band because a two-input score to two decimal places would imply a precision these inputs do not have.

**None of these five numbers is principled** — 2.0, 5%, 3, 30%, and the volatility bands are starting guesses chosen to fire on obviously-bad behavior and stay quiet otherwise, in the same spirit as Step 19 §2.4's symbol cap of 10. They live in one `const` block, are named, and are expected to move once there is real usage to calibrate against.

### 2.8 Caching: Redis, keyed by user, 5-minute TTL, fail-open

One `GET /insights/portfolio` costs one call to `trading-engine`, one call to `market-data` per held symbol plus two benchmarks, and a full reconstruction — too much to redo on every dashboard poll, and cheap to reuse since the underlying bars only change once a day.

**Recommendation:** cache the rendered response object at `insights:{user_id}` with a **5-minute TTL**. Redis is already in the stack and already namespaced by prefix (`market-data`'s prices, `auth`'s `revoked:` keys — `.env.example` notes the convention).

**Not invalidated on trade, deliberately.** Invalidation would require `trading-engine` to know that `ai-insights` exists and to call it on every fill — a dependency pointing from the core trading path *into* an optional analytics service, which is exactly the wrong direction and puts an analytics outage on the critical path of placing an order. The cost is bounded and disclosed instead: **a trade may not be reflected in insights for up to five minutes**, and `computed_at` (§2.4) tells the caller exactly how stale the object is. A UI that needs a fresher number can say when it was computed.

**Cache failure is not request failure.** A Redis error on read means compute; a Redis error on write is logged and ignored. The cache is an optimization, and an unreachable optimization must never turn a working endpoint into a 5xx.

### 2.9 `ai-insights` owns no database

Worth stating rather than leaving as an accident of the design: this service has **no Postgres connection, no store package, and no migration.** Its inputs are two HTTP services and its only stateful dependency is a cache it can run without (§2.8).

That falls out of §2.2 (it owns none of the tables it reads) and §2.1 (its derived series is computed, not stored). The consequences are worth having on purpose: it can be deployed, restarted, or scaled with no schema coordination, it cannot corrupt anything, and its integration surface is HTTP contracts rather than SQL — which is also why this step adds no `integration/` harness and does not become the fourth copy of the Postgres test harness that `docs/deferred-tuning.md` §11 is waiting on.

### 2.10 Degradation: an empty portfolio is a 200, a missing symbol is an error

Two failure shapes that must not be confused with each other.

**Not enough data is a successful response.** A user with no trades has no equity curve, and the honest answer is not `404`, and definitely not zeros — a `0.0%` volatility and a `0.0` Sharpe are *values*, and a new user would read them as measurements of a portfolio that has never traded. Each section instead returns its own explicit state:

```
"risk": {"state":"insufficient_data","reason":"no trades yet"}
```

`risk` and `benchmarking` require **at least 2 trading days** in the window (one return); `behavior` requires **at least 1 trade**. A portfolio that traded yesterday can therefore legitimately return a populated `behavior` section next to two `insufficient_data` ones, and that is a truthful description of what is known.

**Missing upstream data is an error.** A held symbol or a benchmark with no stored bars fails the request (§2.1, §2.6) — `404 symbol_unavailable`, naming the symbol, matching `trading-engine`'s existing code for the same condition (`trading.go:174`).

**Upstream calls get a 5-second timeout each**, matching `backtesting`'s `requestTimeout` (`market_data_client.go:20`), for the same reason: this is a synchronous user-facing request and a hung dependency must fail it promptly. Per-symbol bar fetches run concurrently with a **zero-value `errgroup.Group`** and an error scan in symbol order — Step 19's finding 3, carried forward deliberately: `WithContext` would cancel siblings on the first failure and report a context error in place of the real one, making *which* symbol gets named depend on goroutine scheduling.

### 2.12 Reconciliation: the reconstruction is checked against the live account before anything is reported

**Added 2026-08-20, from Checkpoint B's review.**

§2.1's calendar is a flat intersection over every symbol the account has **ever** held. A gap in the *middle* of one symbol's bars drops a single date, which is exactly the intended behaviour. A gap at the **end** — a symbol delisted, renamed, or simply missing from a `market-data` ingest — truncates the entire curve at that symbol's last bar, including for symbols that have bars through today.

And because `Reconstruction.Holdings` is the position map as of the **last calendar date**, every trade after that date vanishes from it. Silently, with no error:

```
calendar = 2 dates, last = 01-03      (SPY and AAPL both have bars through 01-06)
curve    = 2 points, ends 01-03       (AAPL still being traded on 01-06)
holdings = {AAPL: 10}, cash = 99,000  ← reported
actual   = {AAPL: 100}, cash = 90,000 ← the account
```

That map is what §2.5 weighs. A wrong holdings map is not a degraded answer, it is a confident wrong one, and nothing in the response would hint at it.

**The guard:** before any section is computed, compare the reconstruction's final cash and holdings against the live `balance` and `positions` `trading-engine` already exposes at `GET /trading/portfolio`. Disagreement beyond a cent (`0.01` cash, `1e-4` quantity — `NUMERIC(20,4)`'s finest real distinction) makes the whole response `insufficient_data` with a reason, not a report.

**It is a refusal, not a repair.** Reconciling *toward* the live account would mean inventing an equity history for the dates the calendar could not cover, which is exactly the fabricated data §1's framing exists to forbid.

#### Amended 2026-08-20, from Checkpoint C — the guard must not fire on recency

The drafted guard compared the reconstruction's final state against the live account directly. Verified end to end against the running stack, that turned out to blank the whole report after **any** trade placed since the last stored close:

```
account with 39 trading days of history           → risk populated
one 1-share buy placed today                      → every section insufficient_data
ai-insights: reconstruction disagrees with the live account:
cash 62860.7000 derived from the trade log, account holds 62543.8300
```

Every component behaved as specified. The cause is structural: the reconstruction ends at the **last stored bar**, the live account is **now**, and any trade in between makes them differ. That is the normal case intraday — with daily ingest, every trade placed today diverges until tomorrow's bars land.

**The root problem: the guard cannot distinguish "the curve is truncated" from "the user traded after the last close." Those are arithmetically the same event.** So it fired on ordinary use, and blanking a two-month report over one $317 trade is a worse outcome than the wrong-holdings-map it was added to prevent.

**The amendment:** before comparing, replay the trades executed *after* the last calendar date onto the derived final state. Then compare that projection against the live account.

- If the projection matches, the divergence was **recency**. The report stands, and `as_of_date` already discloses that composition is measured as of the last close.
- If it does not match, the derivation is **genuinely wrong** and it refuses exactly as before.

The trades are already in hand from §2.2, so this is arithmetic over data the request already has — no new upstream call. The fold is the *same* helper `Reconstruct` uses, so the two cannot disagree about what a sell does.

**What the guard still catches:** a dropped or duplicated trade in `GET /trading/trades`, a mis-signed side, a wrong `StartingBalance`, and any divergence in `Reconstruct`'s fold over the calendar — which is the part with the calendar logic in it, and therefore the part worth guarding.

**What it gives back, stated plainly:** pure tail-truncation is no longer a refusal. It becomes an honest as-of-date report: `Holdings` is the position map as of `as_of_date`, and the response says so. That is half of what this section was added for. The trade is deliberate — the truncation case was always *disclosed* by `as_of_date`; what made it dangerous was being **silent about being stale**, not being stale. A section labelled with the date it describes is not a wrong number.

**Consequence for §2.5:** `risk.positions` are the holdings **as of `as_of_date`**, not as of now. A position opened after the last stored close is not in them. That is now part of the section's contract rather than an accident of it.

**This promotes §4's self-check from a test to a runtime invariant.** D5 had it as a unit-test property plus one manual check in T15 — which verifies the code was right on the day it was written, and nothing thereafter. As a guard it holds on every request, and it is not specific to this cause: *any* future divergence between the derived curve and the live account is caught by it, including ones nobody has thought of.

**Why not fix `Calendar` instead.** The principled fix is to qualify each date by the symbols actually held *on that date*, rather than one flat intersection over every symbol ever held. That is a real change to §2.1's rule — which is Step 19's `alignBars` rule carried forward unchanged — and it belongs in its own step. The guard converts every instance of this class from a wrong number into a refusal, which is the property that actually matters, and costs a few lines at the point of consumption.

**Cost:** `ai-insights` now also calls `GET /trading/portfolio`. No new endpoint — it has existed since Step 14.

### 2.11 Gateway and wiring

`/insights/*` mounts in the authenticated group alongside `/trading/*` and `/backtests` — `RequireAuth` → `InjectUserID` → proxy (`gateway/internal/handler/router.go:115`). `INSIGHTS_SERVICE_URL`, default `http://localhost:8085`, continuing the 8081–8084 sequence.

**Corrected 2026-08-20, during T2** — the drafted version of this paragraph was wrong, and the correction has a consequence worth reading rather than skimming.

`ai-insights` does **not** read a trusted `X-User-ID` header. It mounts `pkgauth.RequireAuth(jwtSecret)` and reads the token subject off the request context, which is what `trading-engine` and `backtesting` both actually do — each revalidates the caller's JWT for itself rather than trusting the gateway's header (`trading-engine/internal/handler/router.go`: *"each service checks for itself rather than trusting a proxy header"*). The gateway forwards `Authorization` unchanged; `X-User-ID` is a convenience, not the credential. It binds to loopback by default like the others.

**The consequence:** `/trading/*` is behind `RequireAuth` **at trading-engine itself**, unlike `/market-data/*`, which is open internally. So §2.2's HTTP call cannot be made the way `backtesting` calls `market-data` — an unauthenticated request to `GET /trading/trades` is a 401, no matter which process sends it. `ai-insights` must present a credential: it **forwards the caller's own `Authorization` header** on the outbound call (§6.5, resolved). Scoping therefore stays enforced by `trading-engine`'s own `AccountForUser` — `ai-insights` never learns an account ID and cannot express a request for another user's history.

---

## 3. What is deferred, and why it is recorded here

| Deferred | Why it is not in this step |
|---|---|
| **LLM narrative generation** (`agents.md` §4 Phase 2) | Its input is §2.4's object. Building both together would let prompt design bend the analytics shape toward what reads well, which is the wrong direction of influence. The boundary it will be built against is fixed now: **the model phrases numbers it is given and produces none of its own.** |
| **Insights frontend** | Step 21, per the Step 16→17 precedent. |
| **Sector data** | A `market-data` step with a provider integration behind it (§2.5). |
| **Backtest-vs-live strategy comparison** | Needs both read models; worth doing once this one exists. |
| **Snapshot table** (§2.1 option b) | Reconsider if reconstruction gets slow or if fees/deposits ever exist — either would break the exactness argument that makes reconstruction correct. |
| **`GET /trading/trades` pagination** | `limit`/cap only. Real pagination when an account can plausibly exceed 10000 trades. |

---

## 4. Testing strategy

Unit-first, matching `docs/TESTING_STRUCTURE.md`. **This step adds no `integration/` package** (§2.9) — `ai-insights` has no SQL to test. The one new query is `trading-engine`'s, and it belongs to that service's existing harness.

**`pkg/portfoliomath` (§2.3)** — the extraction's own test is that **`backtesting`'s `metrics_test.go` passes unmodified.** If it needs a single edit, behavior changed and the extraction is wrong. The moved functions carry their existing edge-case tests with them: single-return curve → `0`, zero variance → `0` not `NaN`, monotonically rising curve → drawdown `0` not negative.

**Reconstruction (§2.1)** — the highest-value tests in the step:
- **The self-check:** reconstruction evaluated at today reproduces the live `accounts.balance` and `positions` rows. Run against fixtures in unit tests, and by hand against the dev database during the manual pass.
- Round trip: buy then sell restores cash to exactly the starting balance ± realized P/L.
- A symbol bought and fully sold mid-window still contributes to the curve on the days it was held — the case a "current positions" implementation silently gets wrong.
- A date missing from one symbol's bars is absent from the calendar entirely, and no price is carried forward (§2.1).
- Two trades sharing an `executed_at`, supplied in both possible orders, produce an identical curve — the §2.2 insensitivity claim, tested rather than assumed.

**Rules (§2.5–2.7)** — table-driven, one case per side of every threshold, including exactly-at-threshold. Panic selling gets all four combinations of (prior day down ≥5%) × (realized loss) to prove both conditions are required. Hand-computed expected values, not values captured from a first run — Step 18 §4's "trust a fixture, not self-consistency" warning.

**Degradation (§2.10)** — zero trades, one trade, and a two-day window each produce the documented `insufficient_data` states; a held symbol with no bars produces `404 symbol_unavailable` naming that symbol; a Redis outage still returns 200 (§2.8 fail-open).

**Adversarial pass before merge**, per this project's standing practice: mutate each threshold constant and each comparison operator and confirm a test fails; delete the cash-reconstruction sign handling and confirm the self-check test catches it. A green suite that survives those mutations is evidence; a green suite alone is not.

**Manual pass:** place trades through the running stack across several days of stored history, hit `GET /insights/portfolio` through the gateway, and confirm the numbers against a hand-computed spreadsheet — then restore the dev database to its `users=20, accounts=20` baseline.

---

## 5. Structure, commands, and conventions

**Layout**, mirroring `backtesting` (`docs/TESTING_STRUCTURE.md`; no `store/`, per §2.9):

```
services/ai-insights/
  cmd/server/main.go
  internal/
    client/    trading_client.go, market_data_client.go   // §2.2, §2.1
    cache/     redis_cache.go                             // §2.8
    handler/   insights.go, errors.go, router.go
    service/   reconstruct.go, risk.go, benchmark.go, behavior.go,
               thresholds.go, interfaces.go, types.go, errors.go, mock/
pkg/portfoliomath/                                        // §2.3
```

**Wiring:** add `./services/ai-insights` to `go.work` and to the Makefile's single `GO_MODULES` line; add `make run-ai-insights`; add `INSIGHTS_SERVICE_URL` to `.env.example`. `pkg/portfoliomath` needs neither — `pkg` is already one module (`pkg/go.mod`) that is already in both lists, so the extraction is a new package inside it and is picked up by `make test`/`make vet` for free.

**Conventions carried, not re-decided:** `chi` router with `/healthz`; the `code` + `message` JSON error shape; `service` returns sentinel errors and `handler` maps them to status codes; every external dependency behind an interface in `service/interfaces.go` with a mock in `service/mock/`; money as `float64` in Go against `NUMERIC(20,4)` in Postgres (`docs/deferred-tuning.md` §10, unchanged); loopback bind by default.

---

## 6. Open questions for review

1. **`pkg/portfoliomath` touches `backtesting`.** §2.3 argues the extraction is worth it and bounds it with an unmodified-tests requirement. If you would rather keep this step strictly additive, the fallback is duplication in `ai-insights` — and the two Sharpe implementations start drifting from day one.
2. **The five behavioral thresholds (§2.7) are guesses.** They are named and centralized so they are cheap to change, but if you want different starting values, now is the cheapest moment.
3. **The 5-minute cache TTL (§2.8)** means a fresh trade is invisible to insights for up to five minutes. Acceptable for analytics; say so if it is not, and the alternative is event-driven invalidation with the coupling §2.8 describes.
4. **Reconstruction starts at the first trade (§2.1).** An alternative is account-creation date, which would report longer, calmer-looking histories. First trade is recommended; it is a judgment call about what the window means.

5. **What credential does `ai-insights` present to `trading-engine`?** Raised during T2, not at drafting — §2.2 assumed an open internal call and §2.11 was wrong about why that would work. `/trading/*` requires a valid JWT at the backend, so an unauthenticated internal call is a 401. Three options, in §2's format:

   **(a)** Forward the caller's own `Authorization` header on the outbound call.
   **(b)** Add an unauthenticated internal route to `trading-engine`, e.g. `GET /internal/accounts/{id}/trades`.
   **(c)** Real service-to-service authentication — `docs/security-backlog.md` item 6.

   **Resolved 2026-08-20: (a).** It adds no new credential, no new trust surface and no new route. Scoping stays enforced by `trading-engine`'s own `AccountForUser`, so `ai-insights` never learns an account ID and cannot express a request for someone else's history even if it wanted to — the property §2.2 relies on, preserved rather than re-implemented. The token is the caller's own, acting on the caller's own data, for the duration of their own request; nothing is stored and nothing is delegated beyond the request that arrived.

   **(b)** is the tempting shortcut and the wrong one: it creates a route that returns *any* account's trades to anyone who can reach the port, which is a genuinely new class of exposure rather than a sixth instance of an existing one — and it does so on the service that moves money. **(c)** is correct and is a step of its own, not a sub-task of this one; (a) does not foreclose it.
