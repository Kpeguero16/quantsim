# Implementation Plan — Backtesting Engine MVP (Step 16)

`SPEC.md` is **approved** (2026-08-18). This plan turns it into tasks across 5 phases on branch `step16-backtesting-engine`.

Build `services/backtesting` — the fifth Go service, layered exactly like `trading-engine` (`cmd/server`, `internal/{service,store,client,handler}`). `POST /backtests` runs a moving-average-crossover strategy against `market-data`'s existing historical bars, simulates trades with next-bar-open fills (no lookahead), computes five metrics, and persists the run. Two read endpoints expose it. Gateway gets a fourth proxied prefix, `/backtests/*`. **Backend only** (SPEC.md §1 Non-goals).

Baseline to re-check at every checkpoint:
```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc "SELECT 'users=' || count(*) FROM users"   # 20
```

---

## Decisions carried in from SPEC.md §2 (not reopened here)

- New `services/backtesting`, same layering as `trading-engine` — §2.1
- History via HTTP to `market-data`'s existing `GET /market-data/history/{symbol}?limit=2000`, date-sliced locally — §2.2
- MA crossover, long-only, all-in sizing, one open position, one symbol — §2.3
- Fills at **next bar's open**, never the signal bar's own close — §2.4
- Five metrics; zero-variance Sharpe → `0`, no-losing-trades profit factor → `null` — §2.5
- Two tables, `backtests` + `backtest_trades`, no stored equity curve — §2.6
- `POST/GET /backtests`, `GET /backtests/{id}`, synchronous, 404-not-403 on a non-owner — §2.7
- **`backtesting` revalidates the JWT itself** (`pkgauth.RequireAuth` + `UserIDFromContext`), same as `trading-engine` — does *not* trust the gateway's `X-User-ID` — §2.7
- Validation bounds: `short_window>=2`, `long_window` in `(short_window, 500]`, date range must fall inside the symbol's ingested history, `starting_capital>0` — §2.8

## One decision the plan adds: the integration-harness trigger

`docs/TESTING_STRUCTURE.md` §6a names "a third service needing it" as the trigger to extract the auth/trading-engine integration harness to `pkg/testutil/`, and had predicted `market-data` would be the third. `backtesting`'s store (inserting a run + its trade log, listing, fetching by id+owner) is exactly the kind of SQL that wants a real-Postgres test, so it becomes the third instead.

**Recommendation: add `services/backtesting/integration/` as a third copy in this step, but do *not* perform the `pkg/testutil` extraction here.** The extraction is explicitly cross-cutting — it touches already-shipped, working test files in `auth` and `trading-engine` — and deserves its own reviewed change rather than riding in on this step's diff. Recorded as this step's entry in `docs/deferred-tuning.md`, mirroring how Step 14's D4 deferred an index rather than adding it speculatively.

---

## Phases

### Phase 1 — Schema and scaffold
- **T1** Migration `007_backtests.up/down.sql`: `backtests` (params + 5 metrics, `profit_factor` nullable), `backtest_trades` (FK → `backtests` CASCADE), matching migration 006's column/comment style.
- **T2** `services/backtesting/go.mod` (already stubbed — fill in requires: chi, pgx, uuid, jwt via `pkg/auth`), add to `go.work`, `cmd/server/main.go` with `/healthz` only (wired fully in T9).

### Phase 2 — Pure computation (unit-tested, mutation-tested)
- **T3** `internal/service/strategy.go`: MA crossover signal generation over `[]Bar` → buy/sell signal per bar index. Pure function, no I/O.
- **T4** `internal/service/simulate.go`: walks signals with next-bar-open fills (§2.4), all-in sizing (§2.3), produces the trade log + daily equity curve. Pure function taking bars + signals + starting capital.
- **T5** `internal/service/metrics.go`: the five metrics (§2.5) from an equity curve + trade log, including the two null/zero edge cases.

### Phase 3 — I/O layers
- **T6** `internal/client/market_data_client.go`: `History(ctx, symbol) ([]Bar, error)`, mirroring `trading-engine/internal/client`'s error-mapping pattern (404-shaped vs. everything-else) against `GET /market-data/history/{symbol}?limit=2000`.
- **T7** `internal/store/postgres_backtest_store.go`: `SaveBacktest` (run + trade log, one transaction, no lock needed — single-writer, no concurrent mutation of a completed run), `ListBacktests(userID)`, `GetBacktest(userID, id)` (404-shaped: not-found and not-owned both return the same "no row" outcome).

### Phase 4 — Orchestration, HTTP, gateway
- **T8** `internal/service/backtest.go`: `RunBacktest` — validates (§2.8), calls the client, date-slices, calls T3→T4→T5, persists via the store, returns the result. `interfaces.go`, `types.go`, `errors.go` alongside it, following `trading-engine`'s file split.
- **T9** `internal/handler/`: `router.go` (`pkgauth.RequireAuth` on the `/backtests` group), `backtest.go` (POST/GET/GET-by-id), `errors.go` (copied JSON error shape, per the documented "each service owns its own encoder" convention). Wire into `cmd/server/main.go`.
- **T10** Gateway: `backtestingProxy` in `router.go` and `main.go` (port `8084`, `BACKTESTING_SERVICE_URL`), `Makefile` (`run-backtesting`, add to `GO_MODULES`, `test-integration`, `vet`), `.env.example`.

### Phase 5 — Verification and close-out
- **T11** `services/backtesting/integration/`: third copy of the harness (see decision above), store-level tests against real Postgres.
- **T12** Mutation-test T3–T5 (the pure, highest-risk-of-a-quiet-wrong-number code): break the crossover edge detection, break the next-bar-open fill (fill on signal bar instead), break a metric formula — confirm each is caught, then revert.
- **T13** Manual adversarial pass via `curl` (no frontend yet): a real strategy run against `AAPL` with real ingested data; a window at the boundary of available bars; a date range with no data (400); a strategy that never crosses (zero trades, zero-variance Sharpe, null profit factor all actually exercised); fetching another user's backtest by id (404).
- **T14** `PHASE2_CHECKLIST.md` Step 16 entry, `docs/NEXT_SESSION.md` rewrite, archive `SPEC.md`/`plan.md`/`todo.md` to `docs/archive/phase2-step16-backtesting-engine/`, `docs/deferred-tuning.md` entry for the integration-harness extraction trigger.

Commit granularity: one commit per task, matching Steps 14–15's convention. Not committed or merged until reviewed and explicitly approved, per this project's standing git workflow.
