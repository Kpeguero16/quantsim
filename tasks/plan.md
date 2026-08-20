# Implementation Plan — Portfolio Backtests (Step 19)

## Context

`agents.md`'s roadmap (line 341) and `PHASE3_CHECKLIST.md`'s "Still open" both carry one
remaining backtesting item: multi-symbol / portfolio-level backtests. Steps 16 and 18 each
deferred it as "a materially different simulator, not a small extension." This step builds
that simulator.

`POST /backtests` takes a *list* of symbols. One run applies the chosen strategy to every
symbol independently, but all symbols draw from **one shared starting-capital pool**, and the
five metrics are computed over the **combined** equity curve — one Sharpe, one drawdown, one
win rate for the run.

The framing that shapes everything: a single-symbol backtest is not a second mode. It is the
`len(symbols) == 1` case of the one engine this step builds, and a test proves it is
byte-identical to today's `Simulate`.

`SPEC.md` is **approved** (2026-08-18; six §3 decisions as drafted, decision 1 revised during
review — see SPEC §5). This plan turns it into 19 tasks across 5 phases on branch
`step19-portfolio-backtests` (already created, SPEC committed at `42d1b28`).

Baseline to re-check at every checkpoint:
```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT 'users=' || (SELECT count(*) FROM users) || ' backtests=' || (SELECT count(*) FROM backtests)"
# users=20 backtests=0
```

---

## Decisions carried in from SPEC.md §2 (not reopened here)

- Bar alignment is the **intersection** of every symbol's dates; a wholly-unavailable symbol
  fails the whole run — §2.1
- Shared pool: `target := equityAtOpen / N` computed **once per bar after sells**, buys capped
  at `min(cash, target)` in alphabetical order — §2.2
- `SimulatePortfolio` **replaces** `Simulate`; `TradeRecord` gains `Symbol` — §2.3
- Cap of 10 symbols, duplicates rejected outright — §2.4
- `backtests.symbol` → `symbols TEXT[]`; `backtest_trades` gains `symbol TEXT NOT NULL`;
  `009`'s down direction is a deliberate non-round-trip — §2.5
- Concurrent `History` fetch via `errgroup` — §2.6
- `symbols: []string` on the wire, breaking, **no shim** — §2.7
- Backend and frontend in one step — §2.8
- One comma-separated symbol field, no new input component — §2.9
- `ComputeMetrics` and all five metrics **unchanged** — §1, §2.10

## Five decisions this plan adds

**D1 — `SimulatePortfolio` lands *beside* `Simulate` in Phase 1, replaces it in Phase 2.**
SPEC §4's designated first test compares the two engines, so both must be in the tree at once.
T7 deletes `Simulate` at the moment its last caller disappears, and the A/B test goes with it.
What carries the invariant forward permanently is **Step 16's existing `simulate_test.go`
expectations, retargeted at `SimulatePortfolio` with every number unchanged** (Step 18's D3
pattern), plus T16's mutation on the target expression. If any expectation has to change, that
is a behavior change and the task stops.

**D2 — Migration `009`'s backfill cannot be verified by the integration harness; it gets a
manual scratch-database task.** `applyMigrations` (`integration/harness_test.go:288`) globs
`infra/migrations/*.up.sql` and applies 001→009 to a database it just created **empty**, so
`UPDATE backtest_trades t SET symbol = b.symbol FROM backtests b` always runs against zero
rows there. Step 18 hit this exact wall (its T16: "unreachable as a repeatable automated test
through this harness's always-migrate-to-head design"). The backfill is verified the way
`008`'s was — by hand, in a throwaway database, against a mixed row set, both directions, with
the outcome written into the migration file as a comment. **Do not add a test that appears to
cover it.**

**D3 — The `HistoryClient` mock becomes symbol-keyed *and* concurrency-safe, before any
orchestration task.** Two independent blockers in `internal/service/mock/mock.go`: (a) it
returns one shared `Bars`/`Err` for every symbol, so no test can express "AAPL has bars, MSFT
doesn't" or "these two have different calendars" — precisely §2.1's error cases; (b)
`RunBacktest` will call it from N goroutines (§2.6) while it appends to `Calls []string`,
which is a data race. **`make test` does not run `-race` today**, so this would ship silently
— `go test -race ./...` on `services/backtesting` is added to Checkpoint B.

**D4 — Stored `symbols` are normalized: uppercased, deduplicated, sorted alphabetically.**
§2.4 settles uppercasing and duplicate rejection; §2.2 settles alphabetical *processing*;
neither says what the *stored array* holds. Sorting makes §2.2's determinism claim ("the same
symbol set in a different order produces identical trades") true of the persisted row too, not
just the trade log — otherwise two otherwise-identical runs differ in one column. The frontend
joins the array for display (§2.9), where alphabetical reads fine. **Approved 2026-08-18**,
raised as an inference from §2.2 rather than something SPEC §3 settled explicitly.

**D5 — pgx array binding gets its own verification, because the repo has no precedent.**
A repo-wide grep finds no `pgtype`, no `ANY(`, and no `[]string` ever bound as a SQL parameter
— `symbols TEXT[]` is this project's first array column. pgx v5's default codec handles
`[]string ⇄ TEXT[]` without explicit wrapping, but "should work" is not this project's bar:
T17 asserts the round trip through real Postgres, not just a compiling `Scan`.

---

## Dependency graph

```
T1 alignBars ─┐
              ├─→ T2 SimulatePortfolio ─→ T3 types.go ─┬─→ T4 validateRequest ─┐
              │                                        ├─→ T5 mock (D3) ───────┼─→ T6 RunBacktest ─→ T7 delete Simulate
              │                                        └─→ T11 api/types.ts ─┐ │
T8 migration 009 (independent) ─────────────────→ T9 store ─→ T10 handler ────┼─┘
                                                                              └─→ T12 validation ─→ T13 form
                                                                                  T14 trade log, T15 result/history
```

---

## Phase 1 — The portfolio engine (pure, unit-tested)

Highest-risk work first. Nothing downstream is wired up; `Simulate` still runs the app.

### T1 — `alignBars`: the intersected timeline
**Description:** A pure function taking each symbol's fetched bars plus the date range,
returning alphabetically-sorted symbols and one equal-length `[]Bar` per symbol covering only
dates *every* symbol has. Reuses the existing `sliceRange` (`backtest.go:131`) per symbol
before intersecting.

**Acceptance criteria:**
- Symbols returned sorted; `[][]Bar` parallel to them; every slice the same length.
- A date missing from any one symbol is absent from all outputs.
- N=1 output is exactly `sliceRange`'s output — no behavior invented at N=1.
- Empty intersection returns an empty timeline; the *caller* maps that to
  `ErrDateRangeUnavailable`, not this function.

**Verification:** `cd services/backtesting && go test ./internal/service/ -run Align -v`.
Fixtures: a deliberate one-day gap in one symbol's calendar, a fully disjoint pair, the N=1
identity.

**Dependencies:** None · **Files:** `internal/service/align.go`, `align_test.go` (both new) ·
**Scope:** S

### T2 — `TradeRecord.Symbol` + `SimulatePortfolio`
**Description:** Add `Symbol string` (JSON `symbol`) to `TradeRecord`, and implement
`SimulatePortfolio` per SPEC §2.2 — sells first, one `target := equityAtOpen / N` per bar,
buys alphabetically capped at `min(cash, target)`, close-of-bar mark to market. **`Simulate`
stays untouched** (D1).

**Acceptance criteria:**
- Sells processed before buys within a bar; target computed once per bar, after sells.
- `spend == 0` produces no trade and no error.
- Equity curve length equals the aligned timeline's; `trades` is `[]TradeRecord{}`, never nil
  (the Step 17 `null`-marshaling bug).
- `Simulate` unmodified, still compiling, its own tests still green.

**Verification:** **Write the A/B test first** — the same single-symbol fixture through both
engines, containing **at least two profitable round trips** so compounding is exercised,
asserting identical trades, equity curve, and final equity. Then: two-symbol same-bar
contention (second alphabetically gets a partial, then a zero, fill); a sell freeing cash a
**same-bar** buy spends *where the seller sorts after the buyer* (this fails under a naive
single alphabetical pass — it is the test that proves step ordering); target visibly larger
after a win and smaller after a loss; divergent price paths aggregating correctly.

**Dependencies:** T1 · **Files:** `internal/service/types.go`, `simulate_portfolio.go` (new),
`simulate_portfolio_test.go` (new) · **Scope:** M

### Checkpoint A
- [ ] `make test` and `make vet` green
- [ ] Both engines present and green; A/B equivalence passing on a compounding fixture
- [ ] Review before proceeding — this is the step's core arithmetic

---

## Phase 2 — Wire format, orchestration, schema, store

### T3 — `types.go`: the wire break
**Description:** `RunBacktestRequest.Symbol` → `Symbols []string` (json `symbols`);
`Backtest.Symbol` → `Symbols []string`; `StrategyParams.Symbol` → `Symbols []string`.

**Acceptance criteria:** `Backtest.Symbols` never marshals as `null` — the store's scan path
produces `[]string{}` rather than a nil slice if it ever reads an empty array.

**Verification:** `go build ./...` — compile fallout is the map of everything Phase 2 touches.

**Dependencies:** T2 · **Files:** `internal/service/types.go` · **Scope:** XS

### T4 — `validateRequest`: symbol-list validation
**Description:** Trim, uppercase, reject an empty list, reject any empty entry, reject >10,
reject case-insensitive duplicates, sort (D4). Everything else in `validateRequest` unchanged.

**Acceptance criteria:**
- `nil`/`[]` → `ErrInvalidRequest: symbols is required`; 11 → `ErrInvalidRequest`; 10 accepted.
- `["aapl","AAPL"]` → `ErrInvalidRequest`, **not** silently deduplicated.
- Output uppercased and sorted.

**Verification:** table test at every boundary — 0, 1, 10, 11, exact duplicate, mixed-case
duplicate, whitespace-only entry.

**Dependencies:** T3 · **Files:** `internal/service/backtest.go`, `backtest_test.go` ·
**Scope:** S

### T5 — Symbol-keyed, race-safe `HistoryClient` mock (D3)
**Description:** Per-symbol `Bars`/`Err` lookup (`map[string][]service.Bar`); `Calls`
recording guarded by a mutex.

**Acceptance criteria:** A test can express "AAPL has bars, MSFT doesn't" and "these two
symbols have different calendars"; existing single-symbol tests still read cleanly; `go test
-race` clean once T6 lands.

**Dependencies:** T3 · **Files:** `internal/service/mock/mock.go` · **Scope:** XS

### T6 — `RunBacktest`: concurrent fetch, alignment, portfolio simulate
**Description:** Fetch N symbols concurrently via `errgroup`, collect **all** results and
return the alphabetically-first failure (SPEC §2.6 — `errgroup`'s first-error-wins is
scheduling-dependent and would make the "names the specific symbol" assertion flaky). Align
via T1, warm-up check once against the aligned length, then `SimulatePortfolio`. Promotes
`golang.org/x/sync` from indirect to direct in `go.mod`.

**Acceptance criteria:**
- **`ComputeMetrics`'s call site is byte-identical to today's.** If it needs editing, the
  design is wrong and the task stops.
- Two unavailable symbols → the alphabetically first is reported, repeatably across runs.
- Empty intersection → `ErrDateRangeUnavailable`; one wholly-unavailable symbol fails the
  whole run (never a silent N-1).
- `market_data_client.go:89` already wraps `ErrSymbolUnavailable` with the symbol name — reuse
  it, don't re-wrap.

**Dependencies:** T1, T4, T5 · **Files:** `internal/service/backtest.go`, `backtest_test.go`,
`go.mod`, `go.sum` · **Scope:** M

### T7 — Delete `Simulate`, retarget its tests (D1)
**Description:** Remove `simulate.go` and the transient A/B test; retarget Step 16's
`simulate_test.go` assertions at `SimulatePortfolio` with a one-symbol input.

**Acceptance criteria:** Every Step 16 expectation **unchanged** — only call sites move. `grep
-rn "Simulate(" services/backtesting` returns nothing.

**Dependencies:** T6 · **Files:** `internal/service/simulate.go` (deleted),
`simulate_test.go` · **Scope:** S

### T8 — Migration `009_backtest_portfolios.{up,down}.sql`
**Description:** SPEC §2.5's exact statement order — the trade backfill runs **before**
`backtests.symbol` is dropped, since it joins against it. Down direction deletes rows whose
`symbols` length ≠ 1 (cascading via `backtest_trades`' existing `ON DELETE CASCADE` from
`007`), restores `symbol` for survivors from `symbols[1]`, drops both new columns.

**Acceptance criteria:** Verified by hand in a throwaway database against a **mixed row set**
(one pre-existing single-symbol run with trades, one 3-symbol run inserted directly), **both
directions**, with the outcome written into the file as a comment the way `008`'s is (D2). The
real dev database is untouched by the scratch work and its baseline re-checked afterward.

**Verification:** `migrate` lives in `$(go env GOPATH)/bin`, not on the default `PATH`.

**Dependencies:** None · **Files:** `infra/migrations/009_backtest_portfolios.up.sql`,
`.down.sql` · **Scope:** S

### T9 — Store: arrays through INSERT / SELECT / scan (D5)
**Description:** `symbols` into the `backtests` INSERT, both SELECT column lists, and
`scanBacktest`; `symbol` as a new column in the `backtest_trades` `pgx.Batch` INSERT and in
the trade-log SELECT and its inline scan.

**Acceptance criteria:** `[]string` binds and scans against `TEXT[]` with no `pgtype`
wrapping; every trade row carries its symbol; nil-vs-empty handled on scan.

**Dependencies:** T3, T8 · **Files:** `internal/store/postgres_backtest_store.go` ·
**Scope:** S

### T10 — Handler: confirm zero changes needed
**Description:** The handler never reads `symbol` — it decodes into `service.RunBacktestRequest`
and passes it through. Error mapping is `errors.Is` against sentinels, so wrapped per-symbol
messages still match.

**Acceptance criteria:** Confirmed by `grep`, not assumed. Fix only what the compiler demands.

**Dependencies:** T9 · **Files:** `internal/handler/backtest.go` (expected: none) ·
**Scope:** XS

### Checkpoint B
- [ ] `make vet` (including its `-tags=integration` pass), `make test`, `make test-integration` green across all five services
- [ ] **`cd services/backtesting && go test -race ./...` green** (D3 — not covered by `make test`)
- [ ] Migration `009` applied to the real dev database; baseline still `users=20`
- [ ] A 2-symbol run and a 1-symbol run driven end to end via `curl` against the real stack
- [ ] Review before proceeding — this is SPEC §2.8's named fallback split point

---

## Phase 3 — Frontend

### T11 — `api/types.ts`
`BacktestBase.symbol: string` → `symbols: string[]`; `RunBacktestRequestBase.symbol` →
`symbols: string[]`; `TradeRecord` gains `symbol: string`. Both `symbol` fields live in the
shared base, not the per-strategy variants — one change point each.
**Dependencies:** T3 (contract) · **Scope:** XS

### T12 — `backtest-validation.ts`: comma-separated parsing
**Description:** `BacktestFormValues.symbol` stays a single string field (it now holds
`"AAPL, MSFT"`). A new `validateSymbols(raw): FieldValidation<string[]>` reuses the file's
existing `FieldValidation<T>` shape: split on comma, trim, drop empty runs, uppercase, reject
duplicates and >10, sort. Mirrors the backend's bounds without becoming the authority.

**Note:** there is **no existing comma-split precedent** in this file — SPEC §2.9 slightly
overstates this. What is reused is `FieldValidation<T>` and first-failure-wins, not a list
parser.

**Acceptance criteria:** `""` → error; `"AAPL"` → `["AAPL"]`; `"aapl, msft"` →
`["AAPL","MSFT"]`; `"AAPL, aapl"` → error; 10 accepted, 11 rejected; `"AAPL,,MSFT"` →
`["AAPL","MSFT"]`; trailing comma tolerated.

**Dependencies:** T11 · **Files:** `backtest-validation.ts`, `backtest-validation.test.ts` ·
**Scope:** S

### T13 — `BacktestForm.tsx`
Relabel to `Symbols`, placeholder `"AAPL, MSFT"`. Keeps the existing
`<div>` / `<label htmlFor>` / `<input>` + `setField` convention, the `uppercase` class, and the
single form-wide `role="alert"` error display.
**Dependencies:** T12 · **Scope:** XS

### T14 — `TradeLogTable.tsx` gains a Symbol column
New `<th>Symbol</th>` and `<td>{trade.symbol}</td>`, placed after Date so a row reads "when,
which, what side." Row `key` stays the array index — adding `symbol` does not make the tuple
unique, so the existing eslint-disable comment stands.
**Dependencies:** T11 · **Scope:** XS

### T15 — `BacktestResult.tsx` + `BacktestHistoryList.tsx`
Both interpolate a single string today (`BacktestResult.tsx:22`,
`BacktestHistoryList.tsx:70`); both become `backtest.symbols.join(', ')`. `describeStrategy` is
untouched — it never took a symbol.
**Dependencies:** T11 · **Scope:** XS

### Checkpoint C
- [ ] `npm run lint`, `npm run build`, `npm run test` green
- [ ] Typechecked with `tsc -b` (via `npm run build`) — **never a bare `tsc --noEmit`**, which
      silently no-ops against this repo's `tsconfig` and reports zero errors regardless

---

## Phase 4 — Verification

### T16 — Mutation testing
Break each control, confirm a test fails, revert. Four:
1. `min(cash, target)` → `target`
2. `equityAtOpen / N` → `startingCapital / N` — **SPEC §5's review finding, made permanent**
3. sells-before-buys → one alphabetical pass
4. the 10-symbol / duplicate validation

**Acceptance criteria:** all four caught; all four cleanly reverted, confirmed via `git diff`
plus a full `make vet` / `make test`.
**Dependencies:** Checkpoint C

### T17 — Integration tests
`testBacktest(userID, symbol string, ...)` becomes symbols-aware; a 3-symbol run's trade log
round-trips through Postgres with each trade's `symbol` intact (D5's real verification); the
guard test's `information_schema` assertions extended to `backtests.symbols` and
`backtest_trades.symbol`.

**Acceptance criteria:** quantity assertions read NUMERIC as text via the existing `numeric()`
helper rather than comparing `float64`s — 4-decimal storage bites harder now that per-symbol
positions are smaller. **No test is added that appears to cover `009`'s backfill** (D2).
**Dependencies:** T16

### T18 — Manual browser pass
Real stack, real ingested history, 2–3 symbols. Cover: the comma-separated input including a
rejected duplicate and a rejected 11th symbol; a genuine 2-symbol run; a same-bar contention
case if the data allows constructing one; the trade log's Symbol column; **a pre-Step-19
single-symbol run reopened from history**, proving migration `009` through the UI and not only
in SQL; the zero-trade render path still intact.

Throwaway accounts deleted afterward — **`backtests` rows first**: `backtests.user_id` has no
`ON DELETE CASCADE`, unlike `backtest_trades.backtest_id`.
**Dependencies:** T17

---

## Phase 5 — Close-out

### T19
`PHASE3_CHECKLIST.md` Step 19 entry and its "Still open" tick; `docs/NEXT_SESSION.md` rewrite;
roadmap lines at `agents.md:341` and `README.md:23,397`; archive `SPEC.md`, `tasks/plan.md`,
`tasks/todo.md` to `docs/archive/phase3-step19-portfolio-backtests/`.

**Before merge:** an independent adversarial review (SPEC §4). Step 18's found a real
integer-overflow bug; this step's shared-cash arithmetic and first-ever array column are at
least as good a place to look.

---

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Shared-cash arithmetic wrong but plausible-looking | High | T2's A/B equivalence test on a ≥2-profitable-round-trip fixture, written first; T16 mutation 2 |
| pgx `[]string ⇄ TEXT[]` — no precedent anywhere in this repo | Med | D5; T17 asserts through real Postgres, not a compiling `Scan` |
| `009`'s backfill silently wrong; harness structurally cannot catch it | Med | D2 — manual mixed-row-set verification, both directions, recorded in-file |
| Concurrent `History` races the mock's `Calls` slice; `make test` has no `-race` | Med | D3; `go test -race` added to Checkpoint B |
| N=1 drifts once `Simulate` and the A/B test are deleted | Med | D1 — Step 16 expectations retargeted unchanged, plus T16 mutation 2 |
| Combined backend+frontend diff too large to review (SPEC §2.8) | Low | Checkpoints B and C are each independently green; §2.8's fallback split is available at B |

## Open questions

None. **D4 was the only one and is approved** (2026-08-18) — `symbols` persist uppercased,
deduplicated, and sorted.

---

Commit granularity: one commit per task, matching Steps 14–18. Not merged until reviewed and
explicitly approved, per this project's standing git workflow.
