# Implementation Plan — Portfolio Analytics (Step 20)

## Context

`agents.md`'s Phase 4 opens with *portfolio analytics* and *insight generation*, and §4 splits
that work explicitly: **Phase 1 — rule-based analytics. Phase 2 — LLM-generated insights.**
This step is the first half and nothing else.

`services/ai-insights` becomes the sixth service and serves one endpoint,
`GET /insights/portfolio`, returning a deterministic analysis of the authenticated user's live
paper portfolio across three sections — risk, benchmarking, behavior.

The framing that shapes everything: **every number in the response is computed, reproducible,
and traceable to a trade or a stored bar.** That is what makes the deferred LLM step safe to
build later — it will be handed this object and permitted to phrase it, never to produce a
figure of its own.

The step's real problem is that none of the interesting metrics are functions of the *current*
portfolio. They are functions of its history, and QuantSim stores no portfolio history at all.
SPEC §2.1 solves that by **reconstructing** the equity curve exactly from the trade log plus
stored bars, and Phase 2 of this plan is where that lands.

`SPEC.md` is **approved** (2026-08-20, as drafted; all four §6 questions resolved in favour of
the recommendation). This plan turns it into **16 tasks across 5 phases** on branch
`step20-portfolio-analytics`.

Baseline to re-check at every checkpoint:
```bash
docker compose exec -T postgres psql -U "$POSTGRES_USER" -d postgres -tAc \
  "SELECT 'users=' || (SELECT count(*) FROM users) || ' accounts=' || (SELECT count(*) FROM accounts) \
       || ' trades='  || (SELECT count(*) FROM trades)"
# users=20 accounts=20 trades=0   (the manual pass in T15 moves trades; restore it afterwards)
```

---

## Decisions carried in from SPEC.md §2 (not reopened here)

- The equity curve is **reconstructed on demand**, exactly, from trades + stored bars; the
  window starts at the **first trade**; the calendar is the **intersection** of `SPY`'s dates
  and every ever-held symbol's dates, with **no carry-forward** — §2.1
- `ai-insights` reads trading data over **HTTP** via a new `GET /trading/trades`; it does not
  touch `trading-engine`'s tables — §2.2
- `sharpeRatio`/`maxDrawdownPct`/`meanOf`/`stdevOf` are **extracted to `pkg/portfoliomath`**;
  `backtesting`'s behavior and tests are unchanged — §2.3
- **One endpoint, one object, three sections**; `computed_at` (cache age) and `as_of_date`
  (data age) are distinct — §2.4
- Risk uses **concentration (HHI) over invested positions**, not sectors; cash reported
  separately — §2.5
- Benchmarks are **buy-and-hold `SPY` and `QQQ` over the identical calendar**; both
  non-optional — §2.6
- Behavior is **three rules with named thresholds and evidence trade IDs attached** — §2.7
- Redis cache, `insights:{user_id}`, **5-minute TTL, not invalidated on trade, fail-open** — §2.8
- `ai-insights` owns **no database** — no store, no migration, no `integration/` package — §2.9
- Insufficient data is a **200 with a per-section `insufficient_data` state**; a missing symbol
  is a **404 `symbol_unavailable`** naming it; 5s upstream timeouts; zero-value `errgroup` with
  an ordered error scan — §2.10
- `/insights/*` in the gateway's authenticated group; `INSIGHTS_SERVICE_URL`, port 8085 — §2.11

## Six decisions this plan adds

**D1 — The `pkg/portfoliomath` extraction lands first, alone, as a provably behavior-free
commit.** It is the only work in this step that touches a service otherwise out of scope
(SPEC §2.3). Landing it first and by itself means its proof — *`backtesting`'s
`metrics_test.go` passes unmodified* — is checked against a tree with nothing else in flight,
and it can be reviewed or reverted without disturbing anything else. Doing it last, when
`ai-insights` already depends on it, would make the same revert a cascade.
The proof is mechanical, not a judgment call: `git diff --exit-code --
services/backtesting/internal/service/metrics_test.go` must be **empty** at the end of T1. If
that file needed an edit, behavior changed and the task stops.

**D2 — `GET /trading/trades` returns **ascending** order, diverging from `ListOrders`'
newest-first.** `ListOrders` (`postgres_trading_store.go:257`) returns newest-first because a
UI order history reads that way. This endpoint's only consumer replays the log chronologically
to rebuild cash and holdings (SPEC §2.1), so descending would mean every caller reverses the
slice to undo the `ORDER BY`. The divergence is deliberate and goes in a comment on the store
method, so it does not read as an oversight next to its neighbour.

**D3 — The `HistoryClient` mock is symbol-keyed and mutex-guarded from its first line.** This
is Step 19's D3, applied *before* the wall instead of at it. That step built a symbol-less
mock, then had to retrofit per-symbol lookup and a mutex once `RunBacktest` fetched
concurrently. `ai-insights` fetches N symbols plus two benchmarks concurrently (SPEC §2.10)
and needs per-symbol calendars to test the intersection rule at all (§2.1), so both properties
are required by the first test that matters. There is no single-symbol phase to grow out of.

**D4 — `-race` is named in every checkpoint, because `make test` does not run it.** Step 19
found this and worked around it locally. `ai-insights` is concurrent from T6 onward, so
`cd services/ai-insights && go test -race -count=1 ./...` is written into Checkpoints C, D and
E explicitly rather than assumed to be covered by `make test`.

**D5 — The reconstruction self-check exists in two forms, and only one of them is a test.**
SPEC §4 asks that reconstruction at *today* reproduce `accounts.balance` and the live
`positions` rows. In unit tests there is no live account, so there the check is a **property**:
`reconstruct`'s final cash and holdings equal an independently written running fold over the
same trade log — two implementations that must agree, one of them deliberately naive. The
check against **real** `accounts`/`positions` rows happens once, by hand, in T15's manual pass
against the dev database. **Do not build an integration test that appears to cover it** —
`ai-insights` has no database connection to run one through (SPEC §2.9), and a test that
constructs its own rows would be checking the fold against itself. Same trap Step 19's D2
flagged for migration `009`'s backfill.

**D6 — Phase 3 ships one section end to end before the other two are written.** The response
object grows across Phase 3 → Phase 4: `window` + `risk` first, reachable through the gateway
with a real JWT against a real account, then `benchmarking` and `behavior` added to the same
object. The alternative — build all three pure sections, then wire everything — leaves the
service unreachable until the last task and defers every integration surprise (client shapes,
gateway route, injected user ID, JSON marshalling) to the end of the step. The wire shape
churning between Phase 3 and Phase 4 costs nothing: this is a local branch and nothing
consumes the endpoint until Step 21's frontend.

---

## Dependency graph

```
T1 pkg/portfoliomath ────────────────────────────────┐
                                                     │
T2 GET /trading/trades ──────────────────┐           │
                                         │           │
T3 calendar (pure) ─→ T4 reconstruct ────┼───────────┼─→ T7 risk ─→ T8 handler ─→ T9 gateway
                                         │           │       │
T5 skeleton ─→ T6 clients + mock (D3) ───┘           └───────┤
                                                             ├─→ T10 benchmarking
                                                             └─→ T11 behavior
                                                                      │
                                        T12 degradation ←─────────────┤
                                        T13 cache        ←────────────┘
                                                │
                                        T14 mutation pass ─→ T15 manual pass ─→ T16 docs
```

---

## Phase 1 — Foundations in services that are otherwise out of scope

Both tasks are additive to existing, well-tested services. Neither depends on the other, and
nothing in `ai-insights` exists yet.

### T1 — `pkg/portfoliomath`: extract the statistical primitives (D1)
**Description:** Move `sharpeRatio`, `maxDrawdownPct`, `meanOf`, `stdevOf` and
`tradingDaysPerYear` out of `services/backtesting/internal/service/metrics.go` into a new
package `pkg/portfoliomath`, exported as `Sharpe`, `MaxDrawdownPct`, `Mean`, `StdevPopulation`.
`ComputeMetrics` keeps its signature and its body's shape; its private helpers become calls
into the package. The two documented edge-case decisions move **with their comments**:
population stdev so a one-return curve yields `0` rather than a divide-by-zero, and a drawdown
of `0` rather than a negative number on a monotonically rising curve.

**Acceptance criteria:**
- `git diff --exit-code -- services/backtesting/internal/service/metrics_test.go` is empty.
- `pkg/portfoliomath` takes `[]float64` and returns `float64` — no domain types, no import of
  any `services/` package.
- The moved edge-case tests live in `pkg/portfoliomath/portfoliomath_test.go` **as well as**
  remaining exercised through `metrics_test.go`; the package is now the only implementation.
- `pkg` needs no `go.work` or `Makefile` change — it is already one module in both lists.

**Verification:** `make vet && make test`; then the `git diff --exit-code` above, which is the
task's actual proof. Mutation: flip `StdevPopulation`'s denominator to `n-1` and confirm
`backtesting`'s existing single-return test fails — if it does not, the extraction dropped an
edge case that the old code handled.

**Dependencies:** None · **Files:** `pkg/portfoliomath/portfoliomath.go` + `_test.go` (new),
`services/backtesting/internal/service/metrics.go` · **Scope:** S

### T2 — `GET /trading/trades` on trading-engine (D2)
**Description:** A vertical slice through an existing service: `ListTrades` on `TradingStore`
+ its mock, a service method, a handler, and the route. `ORDER BY executed_at ASC, id ASC`
(ascending per D2, with the comment explaining the divergence from `ListOrders`). `limit`
defaults to 1000 and caps at 10000, mirroring `market-data`'s `DefaultHistoryLimit`/
`MaxHistoryLimit` convention.

**Acceptance criteria:**
- Scoped to the caller's account via the injected user ID, like every other `/trading/*` route;
  unauthenticated → 401.
- Returns `{"trades":[]}` for an account with no trades — never `null`
  (`TestPortfolio_EmptyPortfolioHasAnEmptyPositionsArray`'s convention).
- `realized_pl` is present and null-able; it is set on sells only.
- `limit=0`, negative, non-numeric and absent all fall back to the default; `limit=99999` is
  clamped to 10000, not rejected.

**Verification:** handler/service unit tests against the mock; **an integration test in
`services/trading-engine/integration/`** — this is real SQL and belongs in the harness that
already exists there. Assert ascending order across a fixture whose insertion order is
deliberately *not* chronological, so a missing `ORDER BY` fails rather than passes by luck.

**Dependencies:** None · **Files:** `services/trading-engine/internal/{store,service,handler}/`,
`internal/service/mock/mock.go`, `integration/` · **Scope:** M

### Checkpoint A
- [ ] `make vet`, `make test`, `make test-integration` green across all five services
- [ ] `git diff --exit-code -- .../metrics_test.go` empty (D1's proof)
- [ ] `curl` `GET /trading/trades` through the gateway with a real JWT returns `{"trades":[]}`
- [ ] Review before proceeding — this is the only work touching services outside the step

---

## Phase 2 — Module wiring, then reconstruction (pure, unit-tested)

The step's core arithmetic and its highest-risk work. No HTTP, no Redis — T3 and T4 are pure
functions over fixtures.

**Corrected 2026-08-20, before starting:** T5 (the module skeleton) moves from Phase 3 to the
head of Phase 2. As originally ordered this plan was unbuildable: T3 and T4 write packages
into `services/ai-insights/`, whose `go.mod` is a stub that is in neither `go.work` nor the
Makefile's `GO_MODULES`, so nothing in Phase 2 would have compiled under `make test` or been
vetted at all. The dependency graph above already showed T5 as independent; only its phase
placement was wrong. Task numbering is left alone — renumbering would break every reference
in `todo.md` and the commit log for no gain.

### T3 — The calendar: intersection of `SPY` and every ever-held symbol
**Description:** A pure function taking each relevant symbol's bars and returning the sorted
list of dates for which **every** one of them has a bar. "Every one" includes `SPY` (the
benchmark must be measured on the same days) and every symbol the account has **ever held**,
not only those currently held. Structurally this is Step 19's `alignBars`, keyed by date
rather than by bar index.

**Acceptance criteria:**
- A date missing from any single symbol is absent from the calendar entirely; **no price is
  ever carried forward** (SPEC §2.1).
- Dates returned ascending; the function is deterministic under any input map iteration order.
- An empty intersection returns an empty calendar — the *caller* turns that into an error, not
  this function.

**Verification:** `go test ./internal/service/ -run Calendar -v`. Fixtures: a one-day gap in a
single symbol, a fully disjoint pair, a symbol held and fully sold mid-window, `SPY` shorter
than the holdings. Build the "deterministic ordering" fixture from **six** symbols across
repeated calls — Step 19's T1 found that three symbols in a map land sorted by luck roughly one
run in six.

**Dependencies:** None · **Files:** `services/ai-insights/internal/service/calendar.go` +
`_test.go` · **Scope:** S

### T4 — `reconstruct`: cash, holdings, equity over the calendar (D5)
**Description:** Replay the trade log chronologically to produce, for every calendar date, the
cash balance, the holdings map, and `equity = cash + Σ(qty × close)`. Cash starts at
`StartingBalance = 100000.00` and moves only on trades. The window starts at the **first
trade**.

**Acceptance criteria:**
- Round trip: buy then sell restores cash to exactly the starting balance ± realized P/L.
- A symbol bought and fully sold mid-window contributes to equity on the days it was held —
  the case a "current positions" implementation silently gets wrong.
- Two trades sharing an `executed_at`, supplied in **both** orders, produce an identical curve
  (SPEC §2.2's insensitivity claim, tested rather than assumed).
- Dates before the first trade are absent from the curve entirely.
- **D5's property test:** final cash and holdings equal an independently written naive fold
  over the same log.

**Verification:** table-driven, hand-computed expected values — **not** values captured from a
first run (Step 18 §4's "trust a fixture, not self-consistency"). Mutation: flip the sign on
the sell branch of the cash fold and confirm the property test catches it.

**Dependencies:** T3 · **Files:** `internal/service/reconstruct.go` + `_test.go`,
`internal/service/types.go` · **Scope:** M

### Checkpoint B
- [ ] `make vet`, `make test` green; `gofmt` clean
- [ ] Both mutations above confirmed caught
- [ ] Review before proceeding — every number in the step is downstream of this curve

---

## Phase 3 — The first vertical slice, reachable end to end (D6)

### T5 — Service skeleton
**Description:** `go.mod`, `chi` router, `/healthz`, the `code` + `message` error shape,
`cmd/server/main.go` on port 8085 bound to loopback. Add `./services/ai-insights` to `go.work`
and to the Makefile's single `GO_MODULES` line; add `make run-ai-insights`.

**Acceptance criteria:** `make run-ai-insights` serves `/healthz`; `make test` and `make vet`
pick the module up with no further edits.

**Dependencies:** None · **Files:** `services/ai-insights/`, `go.work`, `Makefile` · **Scope:** S

### T6 — Clients, and the symbol-keyed race-safe mock (D3)
**Description:** `TradingClient.Trades()` against `GET /trading/trades`, `TradingClient.Portfolio()` against `GET /trading/portfolio` (SPEC §2.12's reconciliation guard needs the live balance and positions; the endpoint has existed since Step 14), and
`MarketDataClient.History(symbol)` against `GET /market-data/history/{symbol}`, both with a 5s
timeout (`backtesting`'s `requestTimeout` precedent). **`TradingClient` forwards the caller's
`Authorization` header** (SPEC §6.5): `/trading/*` is authenticated at `trading-engine` itself,
so an unauthenticated internal call is a 401. `MarketDataClient` sends no credential —
`/market-data/*` has no `RequireAuth` on it. Per-symbol fetches run concurrently under
a **zero-value `errgroup.Group`** — *not* `WithContext` — with errors collected per index and
scanned in symbol order, so two unavailable symbols always name the same one (Step 19's
finding 3). The mock is per-symbol keyed and mutex-guarded from its first line.

**Acceptance criteria:**
- A test can express "AAPL has bars, MSFT does not" and "these two have different calendars".
- With two symbols failing, the error names the alphabetically first one on **every** run —
  assert across 200 iterations, as Step 19's T6 did.
- `go test -race` clean.
- Upstream 404 → `ErrSymbolUnavailable`; everything else → `ErrUpstreamUnavailable`.

**Dependencies:** T5 · **Files:** `internal/client/`, `internal/service/{interfaces,mock}/` ·
**Scope:** M

### T7 — The risk section (SPEC §2.5)
**Description:** Position weights, `cash_weight_pct`, `concentration_hhi` (`Σ w²` over
**invested** positions with weights renormalized to the invested total), `largest_position_pct`,
annualized volatility (`StdevPopulation × √252`) and `max_drawdown_pct`, the last two from
`pkg/portfoliomath`.

**Acceptance criteria:**
- HHI of a single holding is `1.0`; of n equal holdings, `1/n`; cash is excluded from it and
  reported separately.
- An all-cash portfolio does **not** report HHI `1.0` — the failure mode §2.5 names explicitly.
- Volatility and drawdown match `backtesting`'s values for an identical curve, since they are
  now literally the same function.

Also implements **SPEC §2.12's reconciliation guard**, which runs before any section is
computed: the reconstruction's final cash and holdings must match the live
`GET /trading/portfolio` within `0.01` cash and `1e-4` quantity, or the whole response is
`insufficient_data`. This is the one defence against a silently truncated calendar
(§2.12) — and against any other divergence between the derived curve and the real account.

**Additional acceptance criteria for the guard:**
- A reconstruction whose final holdings differ from the live positions produces
  `insufficient_data` with a reason, **not** a populated risk section.
- A cash difference of exactly `0.01` passes; `0.02` refuses. Both sides of the threshold.
- A symbol present live but absent from the reconstruction refuses, and vice versa.
- The truncated-tail scenario from §2.12 is reproduced end to end as a fixture and refused.

**Verification:** hand-computed table tests including the all-cash and single-holding cases.
Mutation: widen the tolerance to 1.0 and confirm the §2.12 fixture stops being refused.

**Dependencies:** T1, T4, T6 · **Files:** `internal/service/risk.go`, `reconcile.go` + `_test.go` ·
**Scope:** M

### T8 — `GET /insights/portfolio`: handler and orchestration
**Description:** Fetch trades, derive the ever-held symbol set, fetch bars for those plus `SPY`
and `QQQ`, build the calendar, reconstruct, compute risk, marshal. **This task returns
`window` + `risk` only** — `benchmarking` and `behavior` are added in Phase 4 (D6). Reads the
injected user ID exactly as `trading-engine` and `backtesting` do.

**Acceptance criteria:** `computed_at` and `as_of_date` are both present and distinct in
meaning (SPEC §2.4); unauthenticated → 401; `positions` is `[]` and never `null`.

**Dependencies:** T6, T7 · **Files:** `internal/handler/`, `internal/service/insights.go` ·
**Scope:** M

### T9 — Gateway route and environment
**Description:** `/insights/*` in the authenticated group alongside `/trading/*` —
`RequireAuth` → `InjectUserID` → proxy. `INSIGHTS_SERVICE_URL` defaulting to
`http://localhost:8085`, added to `.env.example` and to `main.go`'s `mustParseURL` block.

**Acceptance criteria:** through the gateway with a real JWT, a real account with real trades
returns a populated `risk` section; **without** a token it returns 401 from the gateway and the
request never reaches `ai-insights`.

**Verification:** the gateway's existing router tests, plus a live `curl` against the running
stack.

**Dependencies:** T8 · **Files:** `services/gateway/`, `.env.example` · **Scope:** S

### Checkpoint C
- [ ] `make vet`, `make test`, `make test-integration` green
- [ ] `cd services/ai-insights && go test -race -count=1 ./...` clean (D4)
- [ ] End-to-end through the gateway: real JWT → real numbers for a real account
- [ ] Review before proceeding — the service is now reachable; the remaining sections are additive

---

## Phase 4 — The remaining two sections

### T10 — Benchmarking (SPEC §2.6)
**Description:** Buy-and-hold `SPY` and `QQQ` with the full `StartingBalance` from the first
close in the window to the last, over the **identical** calendar. Report each benchmark's
return, Sharpe and `excess_return_pct` (simple difference), alongside the portfolio's own.

**Acceptance criteria:**
- Benchmark series use exactly the calendar dates the portfolio curve uses — assert on a
  fixture where `SPY` has *extra* dates the holdings lack, so a calendar mismatch shows up as a
  wrong number rather than a silent one.
- A missing or short benchmark fails the whole request (§2.6) — no shortened array.
- A portfolio that exactly tracks `SPY` reports `excess_return_pct == 0`.

**Dependencies:** T8 · **Files:** `internal/service/benchmark.go` + `_test.go` · **Scope:** S

### T11 — Behavior (SPEC §2.7)
**Description:** `thresholds.go` holding the five named constants, plus the three rules —
30-day turnover for overtrading, the two-condition panic-selling rule, and the risk-profile
bands. Every finding carries `evidence_trade_ids`.

**Acceptance criteria:**
- Panic selling: **all four** combinations of (prior trading day closed ≥5% down) × (negative
  `realized_pl`) tested; only the both-true case fires.
- "Previous **trading** day" means the previous date on the calendar, not `date - 1` — a
  Monday's comparison is against Friday. Tested across a weekend.
- Overtrading uses turnover, not count; a fixture of ten $100 trades does **not** fire while
  ten $10,000 trades does.
- Threshold boundaries tested from both sides *and* exactly at the value.
- Every finding's `evidence_trade_ids` are exactly the trades that caused it.

**Dependencies:** T8 · **Files:** `internal/service/{behavior,thresholds}.go` + `_test.go` ·
**Scope:** M

### Checkpoint D
- [ ] `make vet`, `make test` green; `go test -race` clean
- [ ] All three sections populated end to end through the gateway
- [ ] Review before proceeding

---

## Phase 5 — Degradation, cache, and the evidence

### T12 — Degradation and error mapping (SPEC §2.10)
**Description:** Per-section `insufficient_data` states — `risk` and `benchmarking` need ≥2
trading days, `behavior` needs ≥1 trade. A held symbol or benchmark with no stored bars →
`404 symbol_unavailable` naming the symbol.

**Acceptance criteria:**
- Zero trades → 200 with all three sections in `insufficient_data`, **not** a 404 and **not**
  zeros. A `0.0` volatility for an account that never traded is the specific failure this
  task exists to prevent.
- An account that traded yesterday returns a populated `behavior` beside two
  `insufficient_data` sections — a legitimate mixed response, asserted directly.
- The 404's message names the offending symbol.

**Dependencies:** T10, T11 · **Files:** `internal/service/`, `internal/handler/errors.go` ·
**Scope:** S

### T13 — Redis cache (SPEC §2.8)
**Description:** `insights:{user_id}`, 5-minute TTL, storing the rendered object.
`computed_at` is set when the object is built, not when it is served.

**Acceptance criteria:**
- A second request inside the TTL returns the **same** `computed_at` and makes no upstream call
  — assert on the mock's recorded calls, not on timing.
- **Fail-open, both directions:** a Redis error on read computes; a Redis error on write is
  logged and ignored. Both return 200. Tested with an injected failing cache.
- Keys do not collide with `market-data`'s prices or `auth`'s `revoked:` namespace.

**Dependencies:** T12 · **Files:** `internal/cache/`, `internal/service/insights.go` ·
**Scope:** S

### T14 — Adversarial pass
**Description:** Mutate and confirm a test fails, per this project's standing practice. At
minimum: each of the five thresholds; each threshold's comparison operator; the sell-branch
sign in the cash fold; `StdevPopulation`'s denominator; the calendar's intersection reduced to
a union; the panic-sell rule's `&&` weakened to `||`. Plus `go test -race -count=1` across the
module.

**Acceptance criteria:** every mutation above is caught by a **named** failing test, recorded
in `todo.md`. Any mutation that survives means a missing test, and the test gets written before
the task closes.

**Dependencies:** T13 · **Scope:** M

### T15 — Manual pass against the live stack (D5)
**Description:** Start the full stack, place trades across several days of stored history
through `/trading/orders`, and hit `GET /insights/portfolio` through the gateway. Check every
number against a hand-computed spreadsheet. **Run D5's real self-check here:** reconstruction
evaluated at today must reproduce the live `accounts.balance` and `positions` rows.

**Acceptance criteria:** every reported figure matches the hand computation; the self-check
holds exactly; the dev database is restored to `users=20 accounts=20 trades=0` afterwards and
the restoration is verified with the baseline query, not assumed.

**Dependencies:** T14 · **Scope:** M

### T16 — Documentation
**Description:** Create `docs/PHASE4_CHECKLIST.md` following the Phase 1–3 format with Step
20's entry — what shipped, what each mutation caught, what each verification proved, and the
judgment calls left unaddressed. Rewrite `docs/NEXT_SESSION.md`. Tick Phase 4's first two
roadmap lines in `agents.md`. Archive `SPEC.md`, `tasks/plan.md` and `tasks/todo.md` to
`docs/archive/phase4-step20-portfolio-analytics/`.

**Acceptance criteria:** root `SPEC.md` and `tasks/` do **not** land on `main` — they are
archived under `docs/` and removed from the root, matching every prior step.

**Dependencies:** T15 · **Scope:** S

### Checkpoint E — pre-merge
- [ ] `make vet`, `make test`, `make test-integration` green across all six services
- [ ] `go test -race -count=1 ./...` clean on `ai-insights`, `trading-engine`, `backtesting`
- [ ] `git diff --exit-code -- .../metrics_test.go` still empty (D1 held across the whole step)
- [ ] Dev database restored to baseline, verified by query
- [ ] Independent five-axis review before the squash + `--no-ff` merge
