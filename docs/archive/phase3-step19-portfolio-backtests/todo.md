# Todo — Portfolio Backtests (Step 19)

Tracks `tasks/plan.md`'s 19 tasks. None started.

Branch `step19-portfolio-backtests`. `SPEC.md` approved 2026-08-18 (§2.2 revised
during review — see SPEC §5). One commit per task.

## Phase 1 — The portfolio engine (pure, unit-tested)
- [x] T1 `align.go` — `alignBars`, the intersection of every symbol's dates,
      returned alphabetically. N=1 output is exactly `sliceRange`'s (except
      empty, which is non-nil by design). 8 tests; mutation-tested. The
      sort guard needed rewriting — three symbols in a map land sorted by
      luck ~1 run in 6, so it now uses six symbols across repeated calls.
- [x] T2 `simulate_portfolio.go` — `TradeRecord.Symbol` + `SimulatePortfolio`:
      sells first, `target := equityAtOpen / N` once per bar, buys capped at
      `min(cash, target)` alphabetically. `Simulate` stays (plan D1).
      A/B equivalence written first, on a 2-profitable-round-trip fixture,
      asserting **exact** float equality. 7 tests. All three controls
      mutation-tested and caught (the `startingCapital/N` mutation — SPEC §5's
      finding — fails 4 tests including the A/B). `go test -race` clean.
- [x] **Checkpoint A** — `make vet`, `make test` green across all five
      services; `gofmt` clean; both engines present; A/B equivalence passing.

## Phase 2 — Wire format, orchestration, schema, store
- [x] T3 `types.go` — `Symbol string` → `Symbols []string` on
      `RunBacktestRequest`, `Backtest`, `StrategyParams`. Never marshals `null`
      (invariant documented on `Backtest`; T9's `scanBacktest` enforces it).
      The tree does **not** compile after this task, by design — the fallout is
      Phase 2's map: `backtest.go:38,58,92,119` (T4/T6),
      `postgres_backtest_store.go:49,167` (T9), `backtest_test.go` and
      `integration/backtest_store_test.go`. Handler: no hits but
      `ErrSymbolUnavailable` (T10's grep, early).
- [x] T4 `validateRequest` — `normalizeSymbols`: trim/upper/sort, reject empty,
      >10, and case-insensitive duplicates outright (plan D4). 7 rejection
      cases in the existing pre-fetch table + a 10-symbol acceptance/ordering
      test built from a **descending** list, so order-preserving code fails it.
      All five controls mutation-tested and caught (cap `>`→`>=`; dedup, sort,
      uppercasing and the empty check each deleted). To make the package
      compile and the tests runnable, `RunBacktest` fetches `Symbols[0]`
      behind a marked placeholder that T6 deletes.
- [x] T5 `mock.go` — `BarsBySymbol`/`ErrBySymbol` consulted first, falling
      back to the symbol-less `Bars`/`Err` so single-symbol tests read
      unchanged; `Calls` becomes an unexported slice behind a mutex and a
      `Calls()` accessor — an exported slice cannot be READ safely mid-run
      either, not just written (plan D3). `go test -race` clean.
- [x] T6 `RunBacktest` — `fetchHistories` (zero-value `errgroup.Group`, **not**
      `WithContext`: cancelling siblings would let a context error stand in for
      the real one and reintroduce the nondeterminism the ordered scan removes),
      alignment, one warm-up check on the intersected length,
      `SimulatePortfolio`. `ComputeMetrics`' call site byte-identical
      (`backtest.go:78`, verified by diff). 6 new tests; the
      alphabetically-first-failure one runs 200x. Four controls
      mutation-tested and caught. `golang.org/x/sync` now direct.
      `go test -race` clean.
- [x] T7 Deleted `simulate.go` and the transient A/B test; Step 16's six
      `simulate_test.go` call sites now go through a `simulateSingle` helper
      with **no expectation touched** (the diff is 6 call lines + the helper).
      Re-checked after deleting the A/B: `equityAtOpen/N → startingCapital/N`
      is still caught, by 3 portfolio tests.
- [x] T8 Migration `009_backtest_portfolios.up/down.sql` — trade backfill
      **before** `backtests.symbol` is dropped. Verified by hand in
      `step19_migration_scratch` (created and dropped; dev DB untouched,
      `users=20 backtests=0` re-checked after) against a mixed row set — a
      migrated 1-symbol run with 2 trades and a directly-inserted 3-symbol run
      with 3 — both directions, recorded in-file (plan D2). The statement
      order was tested, not assumed: reordered, it fails with
      `column b.symbol does not exist`. No `CHECK` on cardinality — the 1..10
      bound is `validateRequest`'s, and a second copy would drift.
- [x] T9 Store — `symbols TEXT[]` through INSERT/both SELECTs/`scanBacktest`
      (plus a `nil → []string{}` guard there, T3's never-null invariant);
      `symbol` through the `backtest_trades` batch INSERT and trade SELECT.
      D5 settled: plain `[]string` binds and scans with no `pgtype` wrapper,
      proven against real Postgres — `go test -tags=integration` green, which
      migrates a fresh DB through `009`.
- [x] T10 Handler — confirmed zero changes. `grep -rn "[Ss]ymbol"
      internal/handler/` returns three lines, all in one unchanged
      `ErrSymbolUnavailable` → 400 `symbol_unavailable` mapping
      (`backtest.go:134-136`); the handler decodes `RunBacktestRequest` whole
      and never touches the renamed field.
- [x] **R1** (Checkpoint B review finding) — same-bar trades came back in a
      **random order**: the trade SELECT was `ORDER BY bar_timestamp, id` with
      `id` a random UUID, and ties were impossible only while one run meant one
      symbol. Probed before fixing: the same three same-bar trades written six
      times read back in six different orders. `009` amended (not yet merged,
      so no `010`) with `seq INTEGER NOT NULL` + `UNIQUE (backtest_id, seq)`;
      the store writes the slice index and reads `ORDER BY seq`. Re-verified by
      hand both directions — the backfill orders by `bar_timestamp`, not
      insertion order, and a duplicate `seq` is refused. Live 102-trade
      2-symbol run: 3 same-bar dates, identical order across 4 reads.
      `TestGetBacktest_TradeLogIsOrderedByBarTimestamp` retargeted (it asserted
      the store re-sorts a hand-scrambled log; the contract is now "the log is
      stored as the sequence it is") plus a uniqueness test.

- [x] **Checkpoint B** — `make vet`/`test`/`test-integration` green;
      `go test -race -count=1 ./...` **and** `-tags=integration` green on
      services/backtesting (plan D3); `009` applied to the real dev DB
      (`schema_migrations` 9, not dirty), baseline `users=20 backtests=0`
      held before and after. Live runs against the real market-data:
      `{"symbols":["msft","aapl"]}` came back `["AAPL","MSFT"]` with 13
      interleaved trades (8 AAPL / 5 MSFT), each carrying its symbol, and
      re-read identically through `GET /backtests/{id}`; the 1-symbol run
      likewise. Throwaway rows and user deleted, baseline re-verified.
      **Review point before Phase 3** — SPEC §2.8's fallback split.

## Phase 3 — Frontend
- [x] T11 `api/types.ts` — `symbols: string[]` on `BacktestBase` and
      `RunBacktestRequestBase`; `TradeRecord` gains `symbol`. `tsc -b` maps the
      rest of Phase 3: `backtest-validation.ts:98,104,109` (T12),
      `BacktestHistoryList.tsx:70` and `BacktestResult.tsx:22` (T15).
      `BacktestForm.tsx` and `TradeLogTable.tsx` type-check untouched — they
      go through the validation module, which is why T12 lands before T13.
- [x] T12 `backtest-validation.ts` — `validateSymbols` reusing the existing
      `FieldValidation<T>` shape; `BacktestFormValues.symbol` → `symbols`
      (the raw comma-separated line). One deliberate divergence from the
      backend: an empty ENTRY is dropped, not rejected — a trailing comma is a
      typing artifact, and the parsed array never contains one anyway. A
      duplicate is still refused. 4 new tests (30 total); all five controls
      mutation-tested and caught.
- [x] T13 `BacktestForm.tsx` — label `Symbols`, placeholder `"AAPL, MSFT"`,
      id/field `backtest-symbols`. The `uppercase` input class already made
      what the user types match what gets sent, so it stays.
- [x] T14 `TradeLogTable.tsx` — Symbol column second, after Date; row key
      stays index (now genuinely the log's own order — R1 stores `seq`).
      Rendered at every run size, not conditionally on `symbols.length > 1`.
- [x] T15 `BacktestResult.tsx` / `BacktestHistoryList.tsx` —
      `symbols.join(', ')`. `describeStrategy` untouched.
- [x] **Checkpoint C** — `tsc -b` clean, `npm run build` ✓, `npm run test`
      61/61 in 6 files, `npm run lint` with only the four pre-existing
      `exhaustive-deps` warnings (`use-orders`, `use-backtests`,
      `use-portfolio`, `use-prices` — none of them files this step touched).

## Phase 4 — Verification
- [x] T16 Mutation testing — all four broken, each caught, each reverted:
      1. `min(cash, target)` → `target` — caught by
         `TestSimulatePortfolio_SameBarContentionPartialThenZeroFill`, and by
         **that test alone**. The pool-exhaustion cap is the one control here
         with a single guard; noted rather than padded, since that test
         asserts the behavior head-on rather than catching it incidentally.
      2. `equityAtOpen / N` → `startingCapital / N` — caught by 3 tests
         (`SameBarContentionPartialThenZeroFill`, `SellFreesCashForASameBarBuy`,
         `TargetTracksEquityNotStartingCapital`). This re-proves T7's claim
         that the A/B test's deletion left the finding covered; the claim was
         re-run, not carried forward on trust.
      3. sells-before-buys → one alphabetical sell-or-buy pass (target still
         read once at the open, so the diff is purely ordering) — caught by
         `TestSimulatePortfolio_SellFreesCashForASameBarBuy`.
      4. validation, as two separate control removals: the 10-symbol cap
         deleted → caught by the `eleven_symbols` subtest; the duplicate check
         deleted → caught by all three of `exact_duplicate`,
         `mixed-case_duplicate`, `duplicate_after_trimming`.
      Reverted to a byte-identical tree: `git diff a679a18` empty,
      `git status` clean, `gofmt -l` silent. `make vet` + `make test` green;
      `go test -count=1 -race ./...` green on services/backtesting (forced
      uncached — `make test` reported `(cached)`, which is itself evidence the
      source matched the pre-mutation baseline).
- [x] T17 Integration — two of the plan's three items were already done by T9
      and R1: `testBacktest` has taken `symbols []string` since T9, and
      `TestGetBacktest_TradeLogRoundTripsInTheOrderItWasWritten` is already a
      3-symbol fixture. What was genuinely missing was the *precision* half.
      Added `TestSaveBacktest_PortfolioTradeLogRoundTripsPerSymbol`: 4 fills
      across 3 symbols with no two adjacent rows sharing a symbol, quantities
      carrying 6 decimals so `NUMERIC(20,4)` has something to do (one rounds
      down, one at the half, one up). Quantities assert through `numeric()`
      against the stored 4-dp value, **and** that `GetBacktest` returns that
      same value — a round trip is only a round trip if what comes back is
      what the database kept.
      Guard test extended: `backtests.symbols`, `backtest_trades.symbol`,
      `backtest_trades.seq` present, `backtests.symbol` **absent** (009's last
      statement — a part-applied migration leaves both, and every store
      statement still runs against a stale singular column), and
      `symbols`'s `data_type` is `ARRAY` (a TEXT column would take pgx's
      []string bind as a literal and fail as a wrong list, not an error).
      Both new tests mutation-tested rather than trusted green: writing
      `b.Symbols[0]` to every trade fails 3 of 4 rows; asserting the
      *unrounded* floats fails all 4, proving `numeric()` reads Postgres and
      not the Go value; all five guard expectations flipped and all five
      fired. Diff is additions only, confined to the two test files.
      `make vet`/`test` green, `go test -tags=integration -race -count=1
      ./services/backtesting/...` green. **No test added over `009`'s
      backfill** (plan D2) — the harness always migrates an empty DB.
- [x] T18 Manual browser pass — done against the real dev DB and real
      ingested history (7 symbols x 501 bars, 2024-07-29..2026-07-28).
      **The migrated row.** Manufactured as planned: rolled `009` back
      (safe — `backtests=0`, and the down migration only deletes
      multi-symbol runs), inserted an old-shape single-symbol AAPL row with
      2 trades **in reverse chronological order**, re-applied `009`. The
      backfill produced `symbols={AAPL}`, both trades carrying `AAPL`, and
      **`seq 0` = the earlier buy despite being inserted second** — so the
      `ORDER BY bar_timestamp` is genuinely sorting, not inheriting insertion
      order. That claim was previously verified only in a scratch DB; it is
      now verified live. Reopened from history in the browser: renders
      `AAPL — 10/50 crossover`, final equity $11,250.50, trade log Buy 2/3
      then Sell 4/15. **`009` is proven through the UI, not only in SQL.**
      **Form.** Label `Symbols`, placeholder `AAPL, MSFT`. Duplicate rejected
      client-side, case-insensitively (typed `AAPL, aapl` → "AAPL is listed
      more than once"), before any network call; 11 symbols → "At most 10
      symbols per backtest (got 11)". The `uppercase` class visibly renders
      typed lowercase as uppercase (T13's note, confirmed).
      **A real run through the UI.** Typed `tsla, googl,` — lowercase,
      reverse order, trailing comma — and got `GOOGL, TSLA — 5/20 crossover`:
      uppercased, sorted, trailing comma dropped rather than rejected
      (T12's deliberate divergence, confirmed in the browser). Trade log
      interleaves both symbols, and **10/29/2024 carries two fills (GOOGL
      Buy, TSLA Buy) in alphabetical order** — same-bar contention rendered.
      **Sells-before-buys in live data** (API): a 7-symbol run produced 168
      trades over 28 contended bars, and on 2025-02-13 **AMZN sells before
      AAPL buys** though AMZN is alphabetically later — one alphabetical pass
      would invert it. T16's mutation 3, confirmed against real market data.
      **R1 stability:** 6 re-reads of that 168-trade log, byte-identical.
      **Zero-trade path:** "No trades were simulated for this run.", metrics
      0.00%, profit factor `—`/"no losing trades"; API serves `"trades":[]`,
      not `null` (T3's invariant, end to end).
      **Cleanup:** 231 trades + 7 backtests deleted **before** the throwaway
      user (its `user_id` FK is `NO ACTION` — verified, not assumed).
      Baseline re-verified: `users=20 backtests=0 trades=0`, 3507 bars, 20
      accounts, `schema_migrations` 9 not dirty, schema back to
      `symbols ARRAY` / `trades.symbol` / `seq` with `backtests.symbol` gone.
      Two caveats, noted rather than papered over: `migrate` is not on this
      shell's PATH, so the down/up ran the real migration SQL files directly
      with `schema_migrations` updated to match (the migrations are plain SQL
      with no migrate directives, so this is what golang-migrate would do);
      and console tracking started after page load, so there is **no console
      evidence either way** — no errors surfaced in the UI, but that is not
      the same as a verified-clean console.

## Phase 5 — Close-out
- [x] T19 `PHASE3_CHECKLIST.md` Step 19 entry written and the "Still open"
      multi-symbol item ticked (it had carried since Step 16).
      `docs/NEXT_SESSION.md` rewritten — it states plainly that the branch is
      **unmerged and unreviewed**, since that is the one way this session ends
      differently from the last few. Four new trip-ups recorded: R1's
      "an ORDER BY over row values cannot express a sequence those values
      don't determine", `[]string ⇄ TEXT[]` needing no pgtype wrapper, the
      zero-value-errgroup-over-WithContext reasoning, and `NUMERIC(20,4)`
      biting harder at N>1. Also noted that the frontend keeps tokens in
      memory only, so browser verification must go through the login form.
      Roadmap lines updated: `agents.md` and `README.md` Phase 3 → done,
      README's service table (portfolio runs, 1..10 symbols), migration
      version 8 → 9, frontend test count 58 → 61, and the closing summary
      paragraph. Archived `SPEC.md`/`plan.md`/`todo.md` **verbatim** to
      `docs/archive/phase3-step19-portfolio-backtests/` — Step 18's archived
      SPEC likewise still reads "not started", because the archived spec
      records what was agreed, not what happened; the checklist carries the
      outcome.
      One deliberate inconsistency: the roadmap files say Phase 3 is done
      while `NEXT_SESSION.md` says the branch is unmerged. Those files land
      on `main` only via the merge, so they are accurate the moment they
      arrive; `NEXT_SESSION.md` is the file that tracks branch state.
- [ ] Independent adversarial review before merge (SPEC §4)
