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

## Still open

- [ ] **Frontend for `/backtests/*`.** Step 16 is backend only, mirroring
      the Step 14 → 15 split — an order ticket-equivalent (strategy config
      form), a results view (metrics + trade log), and a run history list
      are all unbuilt.
- [ ] **RSI and MACD strategies**, `agents.md` §3's other two named
      examples. `SPEC.md` (Step 16) §1 scoped these out deliberately —
      mechanical once the crossover pipeline exists, but not built yet.
- [ ] **Multi-symbol / portfolio-level backtests.** One symbol per run today
      (`SPEC.md` §1 Non-goals) — a materially different simulator
      (correlation, cross-symbol position sizing), not a small extension.
- [ ] **market-data's store still has no tests** — `historical_price_store.go`.
      `docs/deferred-tuning.md` §11 records that `backtesting`, not
      `market-data`, ended up being the harness's third copy; a fourth use
      (this one) is the point to actually extract rather than copy again.
- [ ] Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`,
      untouched since Step 11 — carried over from `PHASE2_CHECKLIST.md`.
