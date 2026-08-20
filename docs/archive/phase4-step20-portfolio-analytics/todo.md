# Todo — Portfolio Analytics (Step 20)

Tracks `tasks/plan.md`'s 16 tasks. **T1–T9 done; Checkpoints A–C passed.**

Branch `step20-portfolio-analytics`. `SPEC.md` approved 2026-08-20 as drafted, all four §6
questions resolved in favour of the recommendation. One commit per task.

Baseline, re-checked at every checkpoint:
`users=20 accounts=20 trades=0` (T15's manual pass moves `trades`; it restores it).

## Phase 1 — Foundations in services otherwise out of scope
- [x] T1 `pkg/portfoliomath` — extracted `Sharpe`, `MaxDrawdownPct`, `Mean`,
      `StdevPopulation`, `TradingDaysPerYear`. D1's proof holds: `git diff
      --exit-code -- .../metrics_test.go` is **empty**, and the only file
      touched in `backtesting` is `metrics.go`. 20 tests, all hand-computed.
      **Deviation from the plan:** also extracted `DailyReturns`, which
      `sharpeRatio` had inlined. T7 needs an equity curve's daily returns to
      annualize volatility (SPEC §2.5) and would otherwise re-derive them —
      including the non-obvious zero-opening guard — which is the exact
      duplication D1 exists to prevent. `Sharpe` now calls it; behaviour
      unchanged.
      **Finding — the plan's predicted mutation result was wrong, and the
      reason matters.** Plan T1 said flipping `StdevPopulation`'s denominator
      to `n-1` would fail backtesting's existing single-return test. It does
      **not**: backtesting has no such test. Its two Sharpe assertions cover a
      flat curve and a single-*point* curve, both of which return 0 under
      either denominator. Three other tests (`metrics_test.go:56,77,96`) do use
      two-point curves — the single-usable-return case — but assert only win
      rate, profit factor and total return, so under the mutation they carry a
      **NaN** `SharpeRatio` and still pass. The extraction dropped nothing; the
      edge case its comment documents was simply never covered by a test. It is
      covered now, by `TestSharpe/exactly_one_usable_return` and
      `TestStdevPopulation`, which fail on the mutation (4 subtests).
- [x] T2 `GET /trading/trades` — `ListTrades` on the store + mock + service +
      handler + route. **Ascending** (plan D2), with the reason in a comment.
      `limit` default 1000, cap 10000, normalized in the **service** so the
      rule is one tested function rather than something only reachable through
      HTTP. 4 service tests + 4 handler tests, 17 subtests. Three mutations
      caught (`<=0`→`<0`; cap off-by-one; clamp-to-cap→clamp-to-default).
      **Found while writing it:** oldest-first is not just a convenience for
      the consumer, it is what makes truncation *correct*. A capped response is
      a complete prefix of history, which a forward replay can use over a
      shorter window; newest-first truncation would return sells whose funding
      buys were cut off, and the reconstructed cash would be wrong rather than
      merely partial. Recorded on the interface.
      **Integration suite now VERIFIED** (Docker came up later in the session):
      all 5 tests in `integration/trades_store_test.go` ran and passed with real
      durations — not skipped — alongside the harness guards. Three SQL
      mutations caught: `ORDER BY` removed (2 tests), `ASC`→`DESC` (3 tests),
      account scoping removed (4 tests).
      **A false negative worth recording:** the `ASC`→`DESC` mutant first
      appeared to *survive*. It had not — the replacement hit the method's doc
      comment rather than the query, because the same `ORDER BY` string appears
      in both and only the first occurrence was replaced. The query was never
      mutated. Reporting that as a surviving mutant would have sent someone
      hunting a test gap that does not exist; reporting it as caught would have
      been a false green. Mutants get verified against the line they claim to
      change.
      **Blocked question:** SPEC §6.5 — `/trading/*` requires a JWT at
      `trading-engine` itself, so ai-insights cannot call it the way
      backtesting calls market-data. Does not affect this endpoint; blocks T6.
- [x] **Checkpoint A — PASSED** (reviewed 2026-08-20). All gates green.
      The review went past the diff: T1 was **differential-tested**, not just
      argued — `main`'s private `sharpeRatio`/`maxDrawdownPct` extracted into a
      throwaway module and run against `portfoliomath` over 200,019 curves (19
      hand-built edges plus 200k random, seeded with zeros and negatives).
      `mismatches=0`, bit-identical, NaN-for-NaN. That is stronger evidence
      than "the tests needed no edit", which only proves the tests did not
      notice.
      Two non-blocking notes accepted as-is: `ListOrders` is unbounded (no
      LIMIT at all) so `ListTrades` is the better-behaved of the pair and the
      inconsistency to fix is on the orders side; and `trades` has no index, so
      `ORDER BY executed_at, id` on `WHERE account_id` is a seq-scan-plus-sort
      — pre-existing (`orders` has the same gap), irrelevant at this volume.

## Phase 2 — Module wiring, then reconstruction (pure)
**Plan corrected before starting:** T5 moved here from Phase 3. T3/T4 write
packages into `services/ai-insights/`, whose `go.mod` was a stub in neither
`go.work` nor `GO_MODULES` — so as ordered, nothing in Phase 2 would have
compiled under `make test` or been vetted at all. Numbering left alone.
- [x] T5 skeleton — `go.mod` (chi + `pkg`), `internal/handler/{router,errors}.go`,
      `cmd/server/main.go` on 8085/loopback. **No `DATABASE_URL`** — this
      service owns no tables (SPEC §2.9), and its absence is the design.
      Wired into `go.work`, `GO_MODULES`, `make run-ai-insights` and the help
      text. Verified live: `/healthz` → `ok` 200, unmounted route → 404, and
      both boot guards fire (missing `JWT_SECRET` → exit 1; a 5-byte secret →
      `ValidateSecret` rejects it → exit 1).
- [x] T3 `calendar.go` — `Day` (UTC-midnight normalization) + `Calendar`
      (intersection over every symbol the caller names). No carry-forward.
      13 tests; determinism fixture uses **six** symbols across 20 runs.
      **Five mutations, all caught by named tests:** intersection→union (4
      subtests); sort reversed to descending; sort removed entirely
      (`SortsAscendingFromDescendingInput` + `IsDeterministicAcrossRuns`);
      `Day` no longer normalizing; counting bars instead of distinct symbols
      (`NormalizesTimestampsToTheirDate` + `DuplicateBarsForOneDay...`).
      Two earlier mutation attempts did not compile and were redone — a mutant
      that fails to build proves nothing, and reporting it as "caught" would
      have been a false green.
      **Design note beyond the plan:** `Calendar` counts distinct *symbols* per
      date, not bars. Step 19 recorded that `alignBars` fails **silently** if
      intraday bars ever reach it, since several bars would share one dayKey;
      counting bars here would inherit exactly that — one symbol's two intraday
      bars could satisfy a two-symbol intersection alone. Costs one map.
      `Day` returns a canonical UTC-midnight `time.Time` specifically so it is
      safe as a map key: a bare `time.Time` carrying a location or a monotonic
      reading compares unequal to the same instant, so the map would grow a
      second entry for a date it already had.
- [x] T4 `reconstruct.go` — replay to cash + holdings + equity per calendar
      date from `StartingBalance = 100000.00`, window opening at the **first
      trade**. 9 tests, every figure hand-computed. **Six mutations, all caught,
      all compiling:** sell-branch `cash +=`→`-=`; sell-branch `holdings -=`→`+=`;
      trade window `<= date`→`< date`; window no longer opening at the first
      trade; dust deletion removed; equity dropping the cash term. D5's property
      test caught both sign flips, which is what it exists for.
      **Two decisions the plan did not anticipate:**
      (a) Trades are sorted by **day**, stably — not by timestamp. Within-day
      order cannot affect an additive fold, and sorting by day says so instead
      of relying on it. That is what makes the curve insensitive to the
      arbitrary order same-instant trades arrive in (T2's `ORDER BY` breaks
      those ties on a random UUID).
      (b) Trades are applied with `Day(trade) <= date`, **not** `==`. A trade
      can fall on a date the calendar excludes, because some *other* symbol had
      no bar that day. Equality-matching would silently drop it and the cash
      error would surface much later as a curve disagreeing with the live
      balance. `ATradeOffTheCalendarStillMovesCash` covers it.
      `dustQuantity = 1e-9` drops a fully-sold position rather than leaving it
      at ~1e-17 shares — far below `NUMERIC(20,4)`'s finest real distinction,
      so it can only ever catch float residue.
- [x] **Checkpoint B — PASSED with one finding** (reviewed 2026-08-20).
      `Day(trade) <= date` confirmed correct by independent mutation in both
      directions (`<=`→`==` killed by `ATradeOffTheCalendarStillMovesCash`;
      `<=`→`<` killed by 4). The window opening at the first trade was checked
      for the objection that would sink it — whether day-one P/L is swallowed.
      It is not: `equity[0] = cash + shares×close`, so trade-price-vs-close
      movement is captured, and no return is lost by dropping the idle stretch.
      **One mutation survived and is correct behaviour, not a gap:**
      `SliceStable`→`Slice`. The within-day fold is genuinely additive, so
      stability is decorative — kept as documentation-in-code.
      **THE FINDING → SPEC §2.12.** The calendar is a flat intersection, so a
      gap at the **tail** of one ever-held symbol's bars truncates the entire
      curve there, and `Holdings` — the map as of the last calendar date — then
      describes a portfolio the account does not have. Silent, no error, and it
      is `Holdings` that the risk section weighs. This is the silent
      propagation into T7–T11 that Checkpoint B exists to catch; it was simply
      not on the `<=` line.
      Fix recorded as **SPEC §2.12**: a reconciliation guard at the point of
      consumption comparing final cash and holdings against the live
      `GET /trading/portfolio`, refusing with `insufficient_data` on
      disagreement. A refusal, not a repair — reconciling *toward* the account
      would mean inventing equity history for dates the calendar could not
      cover. It also promotes SPEC §4's self-check from a unit-test property
      (true on the day it was written) to a runtime invariant (true on every
      request), and catches any divergence, not just this cause.
      Rewriting `Calendar` is the principled fix and is its own step: it
      changes SPEC §2.1's intersection rule, which is Step 19's `alignBars`
      rule carried forward.
      Pinned in code as `TestReconstruct_ATailGapTruncatesTheCurve...
      _KnownLimitation`, which asserts the truncation **happens** — so a future
      `Calendar` change that fixes it fails the test and forces §2.12 to be
      revisited rather than silently obsoleted.
      Two minor notes actioned: `portfoliomath`'s package doc no longer
      advertises a volatility export it does not have; `Reconstruct` now states
      the ascending-calendar precondition explicitly (contract-stated, not
      guarded — only `Calendar` feeds it, which sorts).
- [x] **SCOPE LEAK FIXED** (review finding). `go work sync` during T5 bumped
      `x/sync 0.17.0→0.20.0`, `x/text 0.29.0→0.35.0` and added `x/sys` across
      `backtesting`, `trading-engine` and `market-data` — none of which this
      branch needs. Reverted all three to `main`'s `go.mod`/`go.sum`; vet,
      test and test-integration all still green, no re-drift. Kept the two
      genuine fixes: `pkg/go.mod` gaining its real jwt/uuid requires (it was
      under-specified and resolving only via `go.work`) and `gateway`'s
      matching indirect. Especially worth reverting in the one step whose
      thesis is "we changed working services without changing behaviour".
      `auth` gofmt drift confirmed pre-existing (33 lines, dating to 0ac6982,
      the only two files `gofmt -l` flags repo-wide) — left for a standalone
      `gofmt -w` commit on `main`, not this diff.


## Phase 3 — First vertical slice, reachable end to end (plan D6)
- [x] T6 clients + mock — `TradingClient.{Trades,Portfolio}` forwarding the
      caller's `Authorization` (SPEC §6.5); `Portfolio` is §2.12's guard input.
      `MarketDataClient.History`. 5s timeouts. `FetchHistories` uses a
      zero-value `errgroup` (**not** `WithContext`) with an ordered error scan.
      Mock symbol-keyed and mutex-guarded from line one (plan D3), with a
      `Calls()` accessor rather than an exported slice — an exported slice
      cannot be READ safely mid-run either, which is what Step 19's T5 had to
      correct after the fact. 21 tests, `-race` clean.
      **Five mutations, all caught, all compiling:** Authorization forwarding
      removed (2 tests); `sort.Strings` removed; the ordered scan replaced by
      `errgroup.Wait`'s own error; 401/403 no longer distinguished from an
      outage; the empty-bars check removed.
      **Beyond the plan:**
      (a) `FetchHistories` **sorts internally** rather than requiring a sorted
      caller, as backtesting's equivalent does. The determinism guarantee then
      holds unconditionally instead of depending on a comment, and the test
      passes symbols in reverse to prove it.
      (b) A refused credential is `ErrUnauthorized` (→401), **not**
      `ErrUpstreamUnavailable` (→502). An expired token reported as "trading
      engine is down" would send someone debugging the wrong service.
      (c) Client tests run against real `httptest` servers, so status codes,
      headers and JSON decoding are exercised through an actual request cycle.
      The `Authorization` assertion is made **on the wire** — a client that
      dropped the token would satisfy any mock and 401 only in production.
      (d) The path-escaping test asserts on `EscapedPath()`, not `Path`: the
      server decodes `%2F` back to `/`, so the obvious version of that test
      passes whether or not anything is escaped.
      `x/sync` pinned at `v0.17.0` to match every other module — deliberately
      not resolved by `go work sync`, which is what caused the scope leak.
- [x] T7 `reconcile.go` + `risk.go` — SPEC §2.12's guard, then §2.5's figures.
      16 tests / 25 subtests.
      **Guard:** cash within `0.01`, quantities within `1e-4`; symbols scanned
      in sorted order so several divergences always name the same one. The
      §2.12 truncated-tail scenario is reproduced end to end and **refused**,
      with a precondition assert so the fixture cannot stop demonstrating what
      it claims. An empty reconstruction reconciles trivially (no derived state
      to contradict; the caller already routes it to insufficient_data).
      **Risk:** HHI over invested positions renormalized to the invested total
      — 1.0 for one holding, 1/n for n equal, and **0 for all-cash**, tested;
      weights are shares of total equity so positions + cash sum to 100;
      positions sorted largest-first with a symbol tiebreak (map order would
      otherwise vary per run); volatility and drawdown wired to
      `pkg/portfoliomath`. `<2` trading days is `insufficient_data` **with the
      figures omitted**, not zeros.
      **Six mutations, all caught, each verified to have applied.** But three
      first appeared to survive, and the diagnosis differs:
      • *Invalid mutant:* the all-cash HHI mutation used a multi-line `sed`
      that never matched, so the real `invested == 0` guard still fired and the
      injected `return 1.0` was unreachable. Not a survivor.
      • *Real gap:* deleting either explicit holdings branch left every test
      green, because the quantity-mismatch branch catches a missing position
      too — a symbol absent from the live map reads as quantity 0, which is
      outside tolerance. Behaviour was still correct (it refuses either way);
      what was untested was **which** branch fires, so the diagnostic could
      silently degrade. Tests now pin the exact message per branch, and both
      mutants are caught.
      `sqrtTradingDays` is derived from `portfoliomath.TradingDaysPerYear`
      rather than written as a second literal, so the two cannot disagree.
- [x] T8 handler — `GET /insights/portfolio` returning `window` + `risk` only
      (plan D6). Service layer in `internal/service/insights.go`, handler +
      route + `main.go` wiring. 13 mutants, 13 caught.
      **Decision — both benchmarks are in the calendar, not just SPY.** SPEC
      §2.1 names only `SPY`; §2.6 claims every benchmark figure is measured
      over the portfolio's own trading days "by construction". Those are only
      both true if `QQQ` is in the intersection too, so it is. Cost, stated
      rather than hidden: a gap in a benchmark's bars shortens the portfolio's
      own window — already true of `SPY` under either reading, so this is a
      second instance of an accepted trade-off, not a new one.
      **Decision — a never-traded account fetches no bars at all.** Not only an
      optimization: with no trades there is no window and no holdings, so the
      benchmark fetch could only *fail* the request, and a brand-new user would
      get a `404` naming `SPY` for a portfolio they do not have.
      **Found by marshalling `RiskSection` for the first time — `omitempty` was
      deleting real measurements.** Every figure has a reachable zero:
      `concentration_hhi` is 0 for an all-cash portfolio (the exact answer §2.5
      argues for, and T7 has a test named after it), `cash_weight_pct` is 0 when
      fully invested, `max_drawdown_pct` is 0 for a curve that only went up. An
      omitted key reads as "not computed", so the tags were reporting
      measurements as absences. Dropped from all five figures and from
      `positions`, which is now always an array and never `null`. `state` stays
      the discriminator.
      **Two mutants earned their pass:** `ComputeRisk`'s own too-few-days path
      still left `positions` nil, and the handler's missing-subject branch is
      unreachable through the router and so was untested — it now has a test
      that calls the handler directly, because mounting it outside the
      authenticated group is a one-line mistake.
- [x] T9 gateway — `/insights/*` in the authenticated group;
      `INSIGHTS_SERVICE_URL` default `http://localhost:8085`; `.env.example`.
      3 routing tests, 2 mutants caught (misrouted to the backtesting backend;
      mounted outside the authenticated group). The `Authorization` header
      surviving the hop is pinned by exact string — load-bearing twice for this
      route, since `ai-insights` both revalidates it and forwards it onward to
      `trading-engine` (§6.5).
      **Noted, not acted on:** `NewRouter` now takes five interchangeable
      `http.Handler` parameters, which is a misroute waiting to happen. The
      per-prefix routing tests do catch a swap. A `Backends` struct would be the
      fix, following `RateLimitConfig`'s own precedent — left off this branch as
      scope.
- [x] **Checkpoint C** — `go vet`, `make test`, `-race -count=1` all clean
      across 7 modules; `gofmt` clean. End-to-end verified through the gateway
      against the live stack with a real JWT: registered account → 3 filled
      orders → populated `risk` (39 trading days, HHI 0.382, volatility 9.70%,
      drawdown 5.62%, weights + cash = 100.00). Unauthenticated → 401 at the
      gateway, never reaching the service. DB baseline restored to
      `users=20 accounts=20 trades=0 orders=0 positions=0`.
      **Also found: a stale pre-T8 `ai-insights` binary was still listening on
      8085** from an earlier session, serving the old router. Anyone curling
      8085 would have seen a plain-text 404 for `/insights/portfolio` and
      concluded the route was missing. Killed.

      **FINDING — §2.12's guard blanks the whole report after any trade placed
      since the last stored close. Demonstrated, not theorised.** An account
      with two months of history reported fully; one 1-share buy placed *today*
      turned the entire response into `insufficient_data`:

      ```
      ai-insights: reconstruction disagrees with the live account:
      cash 62860.7000 derived from the trade log, account holds 62543.8300
      ```

      Every component behaved exactly as specified. The cause is structural:
      the reconstruction ends at the last calendar date (the last stored bar),
      the live account is *now*, and any trade in between makes them differ.
      That is the normal case intraday — in production with daily ingest, every
      trade made today diverges until tomorrow's bars land.

      **The guard cannot tell "the curve is truncated" from "the user traded
      after the last close" — they are arithmetically the same event.** So it
      fires on ordinary recent activity, and blanking a two-month report over
      one $317 trade is worse than the wrong-holdings-map it was added to
      prevent.

      **Recommended fix (Khalil's call, spec change to §2.12):** replay the
      trades executed *after* the last calendar date onto the derived state
      before comparing. If that fully explains the difference, the divergence
      is recency and the report stands, with `as_of_date` already disclosing
      that composition is as of the last close. If it does not, the derivation
      is genuinely wrong and it refuses as now. This keeps the guard's real
      value — a mis-signed sell, a dropped trade, a wrong starting balance —
      and stops it firing on normal use. The trades are already in hand, so it
      is arithmetic on data the request has, not another upstream call.
      **Trade-off, stated plainly:** pure tail-truncation stops being a refusal
      and becomes an honest as-of-date report. That is the half of §2.12 this
      would give back.

      **Secondary, less severe:** an account whose *first* trade is after the
      last stored bar gets `trading_days: 0` and an empty report until bars
      cover the trade date. On this dev stack that is permanent — bars end
      2026-07-28, 23 days stale. In production with daily ingest it resolves
      the next day. Not a design flaw, but it is what a brand-new user sees on
      day one.

## §2.12 amended — the guard must not fire on recency
- [x] **SPEC §2.12 amended and implemented**, per Checkpoint C's finding.
      `Reconcile` now replays the trades executed *after* the last calendar
      date onto the derived state before comparing, and refuses only on what
      remains unexplained. The fold is extracted to `applyTrade` and shared
      with `Reconstruct` — if the two ever drifted on a sign or the dust rule,
      the guard would refuse every request and blame the account.
      **Given back deliberately:** a truncated tail is no longer a refusal, it
      is a report whose `as_of_date` names the date its holdings describe.
      Truncation was always *disclosed* by `as_of_date`; what made it dangerous
      was being silent about being stale, not being stale. Now part of
      `RiskSection`'s contract: positions are as of the last close, not now.
      **Still caught, one test each:** a dropped trade, a mis-signed side, a
      wrong derived state, and a projection that mutates `Holdings` — which
      would reintroduce the exact confusion the guard exists to prevent.
      7 mutants, 7 caught, including restoring the drafted guard.
      **Re-verified against the live stack the same way the bug was found:** the
      1-share trade that blanked the report now leaves it `ok` at 39 days,
      reporting 50 AAPL as of 2026-07-28 rather than the 51 held now; and a
      deliberately corrupted `accounts.balance` still refuses, logging
      `cash 62548.46 derived from the trade log through 2026-07-28, account
      holds 57548.46`. Baseline restored.

## Phase 4 — The remaining two sections
- [x] T10 `benchmark.go` — SPY/QQQ buy-and-hold, both measured over exactly the
      portfolio's calendar dates by indexing `r.Dates`. Fixture where SPY is
      priced three days past the calendar pins it: reading the benchmark's own
      bar range would report a return over a window the portfolio was never
      measured over, and it would look entirely reasonable in the response.
      12 mutants, 12 caught.
      **`buyAndHold` returns the close series itself, not closes scaled by
      `StartingBalance/first` shares.** A mutant that bought at the *last*
      close survived, which was the tell — both reported figures are
      scale-invariant (a total return is a ratio of two points; Sharpe is built
      from day-over-day ratios), so the share count cancels out of everything.
      The multiply looked load-bearing and was not. The Sharpe test still
      builds its expected curve *with* the multiplier, so the invariance the
      omission relies on is itself covered.
      **Gap worth having:** the loop's missing-close guard was untested, because
      the whole-symbol-absent case fails at the first-close check before the
      loop starts. A benchmark missing a *middle* date now has its own fixture
      — otherwise the gap reads as a close of 0 and shows up as a catastrophic
      drawdown in a healthy index.
- [x] T11 `behavior.go` + `thresholds.go` — all five thresholds in one exported
      `const` block with §2.7's admission that none of them is principled kept
      next to them. All four panic-sell combinations tested (plus break-even and
      a nil `realized_pl`); "previous **trading** day" tested across a real
      weekend (2026-03-06 Fri → 2026-03-09 Mon, asserted as such);
      `evidence_trade_ids` pinned on every finding. 18 mutants, 18 caught.
      **The drop comparison was untestable at its own boundary.** It computed
      `(prev/before - 1) * 100 <= -5`, and `0.95` has no exact binary
      representation — so *every* clean 5% fall came out as
      `-5.000000000000004` and the comparison could never land on `-5`. `<=`
      and `<` were indistinguishable, and the test claiming to pin "exactly 5%"
      pinned nothing. Now compared as **prices** against a threshold price:
      `100 * (1 - 5.0/100)` is exactly `95.0`, the boundary is reachable, and
      the mutant flipping `<=` to `<` is caught. Found only because that mutant
      survived.
      **The overtrading denominator was mean-vs-final and nothing could tell.**
      Every fixture held a *flat* equity curve, where the two are the same
      number. A curve that triples over the window now separates them: the same
      550,000 traded is `2.75x` against the mean and `1.83x` against the final
      equity, so one fires and the other does not. Mean is right — the trades
      happened *throughout* the window, and judging them against equity that
      existed only at the end flatters any account that grew.
      **`omitempty` is correct on `Finding`, unlike on `RiskSection`**, and the
      comment says why: neither `turnover_ratio` 0 nor `occurrences` 0 is
      reachable in a finding that exists, so an absent key really does mean
      "not computed for this kind of finding". `risk_profile` is likewise
      absent when risk could not be computed — defaulting to `conservative`
      would be a claim about a portfolio nothing was measured on.
- [x] **Checkpoint D** — `go vet`, `make test`, `-race -count=1` clean across 7
      modules; `gofmt` clean. **All three sections populated end to end through
      the gateway**, against a 39-trading-day account:
      `risk` HHI 0.364 / vol 11.15% / drawdown 6.80%, weights + cash = 100.000000;
      `benchmarking` SPY −2.46% (excess −1.84) and QQQ −9.48% (excess +5.17),
      both differences verified to the sixth decimal;
      `behavior` `panic_selling` ×3 with `risk_profile: moderate`.
      **The panic rule fired against real historical bars**, not a fixture:
      three sells each placed the trading day after a genuine 5%+ fall in
      `historical_prices` (TSLA −5.81% on 2026-06-23, AAPL −6.17% on 06-25,
      TSLA −7.65% on 07-02), and the three `evidence_trade_ids` are exactly
      those three sells in date order. `moderate` is the band the AND requires:
      volatility 11.15 is under 12, but HHI 0.364 is over 0.25, so
      `conservative` correctly does not apply.
      **One fabrication, isolated and disclosed:** `realized_pl` was set
      negative on those sells by SQL, because every fill on this stack happens
      at today's live price and so books zero P/L. Prices and quantities were
      left as filled, so cash and holdings stayed consistent — which the
      reconciliation guard independently confirmed by passing.
      Baseline restored to `users=20 accounts=20 trades=0 orders=0 positions=0`.

## Phase 5 — Degradation, cache, evidence
- [x] T12 degradation — mostly an **audit**: per-section states and the
      502/401 split already landed with T8. Two of three criteria were unmet.
      **The 404 discarded the symbol the error already carried**, answering
      "one of the portfolio's symbols" and leaving the user to guess which
      holding cannot be priced — and it is not always a holding, since a
      missing *benchmark* takes the same path and the user does not own `SPY`.
      `ErrSymbolUnavailable` now has a typed form carrying the symbol as a
      **value**; parsing it back out of a message would have made that
      message's wording a wire contract. `errors.Is` still matches via
      `Unwrap`.
      **The mock returned a bare sentinel where production wrapped the
      symbol**, so the naming could not have been tested against it at all.
      **The mixed response was untested end to end** — a populated `behavior`
      beside two `insufficient_data` sections, for an account that traded but
      has one trading day. That shape is the entire reason the sections carry
      their own states rather than the report carrying one.
      4 mutants, 4 caught, including the empty-symbol fallback that nothing
      reached until a test constructed it (untested it degrades to "no
      historical data is available for ", a sentence that stops mid-thought).
- [x] T13 cache — `insights:{user_id}`, 5-min TTL, fail-open both ways.
      `REDIS_URL` optional (logged at boot when absent); a nil cache becomes a
      `noopCache` so the request path has no "is there a cache" branch. One
      write site; `insufficient_data` reports cached too; errors never cached.
      **`internal/cache` had no unit-test precedent** — `market-data`'s
      equivalent has none either, and no Redis fake is vendored anywhere — so
      rather than add a dependency, `insightsKey` is pinned in-package against
      the three namespaces other services own (`price:`, `prices:`,
      `revoked:`), and the down-Redis paths run against a real client pointed
      at a dead port.
      **Verified against live Redis, covering the one branch a unit test could
      not reach:** no key present → genuine `redis.Nil` miss → computed,
      logging nothing; second request identical `computed_at`; key
      `insights:{uuid}`, `TTL 300`. Then **Redis stopped outright → HTTP 200
      with a complete 39-day report**, with `cache read failed, computing` and
      `cache write failed, ignoring` both logged.
      9 mutants, 8 caught by tests, the ninth by the live check.
      **Dependency hygiene:** `go-redis v9.21.0`, `xxhash v2.3.0` and
      `go.uber.org/atomic v1.11.0` all match `market-data`'s existing pins
      exactly, and no other module's `go.mod` moved.
- [x] T14 adversarial pass — the plan's named list, **15/15 caught**, each by a
      named test:

      | mutation | killed by |
      |---|---|
      | `Calendar`: intersection → union | `TestCalendar_IntersectsEverySymbol` (+10) |
      | cash fold: sell adds holdings, debits cash | `TestReconstruct_ASymbolSoldMidWindowStillContributesWhileHeld` (+4) |
      | cash fold: buy credits cash | `TestReconstruct_FinalStateAgreesWithAnIndependentFold` (+39) |
      | `StdevPopulation`: n → n−1 | `TestSharpe/exactly_one_usable_return`, `TestStdevPopulation` |
      | `OvertradingTurnoverRatio` 2.0 → 3.0 | `TestBehavior_OvertradingThresholdBoundary` (+3) |
      | `OvertradingWindowDays` 30 → 60 | `TestBehavior_OvertradingIgnoresTradesOlderThanTheWindow` |
      | `PanicSellDropPct` 5.0 → 10.0 | `TestBehavior_PanicSellingDropThresholdBoundary` |
      | `PanicSellMinOccurrences` 3 → 4 | `TestBehavior_PanicSellingCountAndShareAreAlternatives/three_of_fifteen` |
      | `PanicSellMinShareOfSells` 0.30 → 0.50 | `TestBehavior_PanicSellingCountAndShareAreAlternatives/two_of_five` |
      | `AggressiveVolatilityPct` 25 → 20 | `TestBehavior_RiskProfileBands/volatility_exactly_at_the_aggressive_bound` |
      | `AggressiveHHI` 0.5 → 0.3 | `TestBehavior_RiskProfileBands/concentration_exactly_at_the_aggressive_bound` |
      | `ConservativeVolatilityPct` 12 → 15 | `TestBehavior_RiskProfileBands/volatility_exactly_at_the_conservative_bound` |
      | `ConservativeHHI` 0.25 → 0.15 | `TestBehavior_RiskProfileBands/calm_and_diversified` |
      | panic rule: `&&` → `||` | `TestBehavior_PanicSellingRequiresBothConditions` (+14) |
      | `minTradingDays` 2 → 1 | `TestComputeRisk_TooFewDaysIsInsufficientDataNotZeros` (+5) |

      **One survivor, and its cause is worth keeping.** `PanicSellMinOccurrences
      3 → 4` changed no verdict anywhere, because every fixture left the SHARE
      arm able to fire too — three of nine sells is 33%, over the 30% share, so
      the count constant was effectively untested behind an alternative that
      always covered for it. Fixed with a fixture that puts the share out of
      reach (2/15 = 13%, 3/15 = 20%) so the count is the only arm that can
      fire. **Two alternatives in one rule will each hide the other's boundary
      unless a test isolates them** — the same shape as T7's two holdings
      branches and T11's mean-vs-final denominator.
      **Cross-task regression check:** every per-task harness re-run against the
      finished code — **80 mutants across the step, all caught** except the one
      Redis-miss branch verified live in T13 instead. Three T8 mutants had gone
      stale (the code moved under them: `Reconcile` gained a parameter,
      `insufficientData` gained two sections, the handler now passes a user id)
      and were repointed rather than left silently passing as INVALID.
- [x] T15 manual pass — nine trades placed through the live stack against a
      fresh account, eight backdated across stored history (2026-06-01 ..
      2026-07-20) and one left at today, 23 days past the last stored bar.
      Every reported figure recomputed independently in Python straight from
      Postgres — own calendar intersection, own fold, own statistics — and
      **all 19 matched to the last significant digit**: the five position
      weights, `cash_weight_pct`, `concentration_hhi`, `largest_position_pct`,
      `annualized_volatility_pct`, `max_drawdown_pct`, `portfolio_return_pct`,
      `portfolio_sharpe`, and both benchmarks' return / excess / Sharpe.

      **D5's real self-check holds exactly.** Reconstruction projected past the
      calendar derives cash `84102.5950` against a live `accounts.balance` of
      `84102.5950`, and holdings `{AAPL 10, AMZN 12, GOOGL 6, MSFT 10, TSLA 8}`
      against the live `positions` rows — to the cent and the share. This is
      also §2.12's amendment working on real data: the post-calendar sell is
      exactly the trade that blanked the whole report at Checkpoint C.

      Verified live besides: §2.8's deliberate staleness (nine trades placed,
      the endpoint still said "no trades yet" with `computed_at` unmoved — the
      field §2.4 added to disclose precisely that); cache hit stable across
      three calls, TTL 289s; 401 at both the gateway *and* directly at 8085
      (§6.5's revalidation); 404 `symbol_unavailable` naming `TSLA` with its
      bars removed, and **not cached** (§2.8).

      **Found a real defect, and it was mine.** `panicSelling` read the
      calendar without bounding itself by `as_of_date` the way its sibling
      `overtrading` already did. For a sell past the last stored close,
      `sort.Search` runs off the end and `calendar[i-1]/[i-2]` silently name
      the window's last two days — so a sell could be flagged as panic selling,
      with its trade ID attached as evidence, on a price move weeks earlier
      that had nothing to do with it. **The path was unreachable until I
      amended §2.12**: before the amendment a post-calendar trade made
      `Reconcile` refuse and no section was computed. Making truncation
      survivable made it routine.

      The live run did *not* expose it — GOOGL rose 2.3% on the last stored
      session, so the drop test failed on price direction alone; the sell had
      already passed the loss gate at −0.32. Reading the code exposed it.
      Fixed by excluding post-calendar sells from the rule, **denominator
      included** — counting a sell the rule refused to judge would dilute the
      share test with a trade that never had a chance to qualify.

      | mutant | result |
      | --- | --- |
      | guard never fires (the pre-fix behaviour) | caught by both new tests |
      | `After(asOf)` → `Before(asOf)` | caught |
      | post-calendar sell still counted in the denominator | **survived first** |

      The denominator mutant survived until a fixture isolated it: 2 qualifying
      sells is under `PanicSellMinOccurrences`, so only the share arm decides,
      and 2/6 = 33% fires where 2/7 = 29% would not. A fourth mutant (deleting
      the guard outright) **did not build** — unused `asOf` — and was rewritten
      as `asOf.AddDate(100, 0, 0)`; a mutant that does not compile proves
      nothing, the same trap the §2.12 harness hit.

      **One system property recorded, not fixed.** With Redis stopped the
      endpoint returns 502, and every layer is behaving as designed:
      `ai-insights`' cache fails open correctly (it logs "cache read failed,
      computing" and proceeds), `trading-engine` degrades to unpriced
      positions — but that degradation is *slow*, and `GET /trading/portfolio`
      takes **8.7s against 5.8ms healthy**, tripping §2.10's 5s upstream
      timeout. So a Redis outage takes the report down even though every figure
      in it comes from Postgres and none needs a live quote. Pre-existing —
      `market-data`'s price path is Redis-backed and `trading-engine` retries
      slowly — and fixing it means changing `trading-engine`'s retry behaviour,
      which is outside this step.

      Also fixed: the gateway's startup log listed four backends and not
      `ai-insights`, so the one line an operator reads to confirm routing was
      silently wrong about the route this step added.

      Baseline restored and **verified by query**, not assumed: `20|20|0|0|0`,
      `historical_prices` back to 3507 rows across all seven symbols, the
      `t15_tsla_backup` scratch table dropped, and no `insights:*` keys left in
      Redis.
- [x] T16 docs — create `docs/PHASE4_CHECKLIST.md`, rewrite
      `docs/NEXT_SESSION.md`, tick `agents.md`'s Phase 4 lines, archive
      `SPEC.md`/`tasks/` to `docs/archive/phase4-step20-portfolio-analytics/`
      and remove them from the root.
- [x] **Checkpoint E — PASSED with two findings** (reviewed 2026-08-20).
      Every gate re-run rather than taken from the session that set it:
      `make vet` clean across seven modules, `make test` green, `-race` clean
      on `ai-insights`/`trading-engine`/`backtesting`, `make test-integration`
      63 passed / 0 failed, D1's `git diff --exit-code` on `backtesting`'s
      `metrics_test.go` still empty, and the T5 dependency revert confirmed
      still holding — `backtesting`, `trading-engine` and `market-data` carry
      no `go.mod` change on this branch.

      **R2 — `panicSelling` diluted its own share test.** The fifth instance
      in this step of the pattern the earlier survivors taught: two paths to
      one outcome hiding each other's boundary.
      `previousTradingDayDropped` returned a bare `false` for two different
      facts — "there is no pair of prior sessions to judge this against" and
      "the prior session did not drop" — so the caller could not tell them
      apart and counted unjudgeable sells in the denominator of
      `occurrences / sells`. A sell in the window's first two sessions is
      unjudgeable for exactly the reason a post-`as_of` sell is, and the
      post-`as_of` case had already been excluded *with that reasoning
      written down*; the early case was not. Direction is a false NEGATIVE —
      the rule goes quiet, it never invents a finding — which is why no
      existing test and no live run exposed it.

      Found by reading the two boundaries against each other, then confirmed
      by a test before anything was changed: 3 unjudgeable early sells beside
      1 genuine panic sell scored `1/4 = 0.25`, under the 0.30 share, and the
      finding stayed silent. Fixed by splitting the conflated function into
      `priorSessions` (does a judgeable pair exist, and which dates) and
      `droppedAcross` (given that pair, did it drop), so judgeability is
      settled before `sells++`. The `After(asOf)` guard stays and now says
      why it must: past the end of the calendar `sort.Search` returns
      `len(calendar)`, which satisfies `priorSessions`' own bound and would
      name the window's last two days. Pinned by
      `PanicSellingExcludesEarlySellsFromTheShare`, written as the explicit
      sibling of the post-calendar test and asserting on `"1 of 1 sells"` in
      the detail string so a denominator leak fails loudly. Verified red
      against the old implementation and green against the new by stashing
      only `behavior.go`.

      **R3 — `pkg` and `services/gateway` did not build outside the
      workspace.** `GOWORK=off go build ./...` failed both with `missing
      go.sum entry for go.mod file`; five of seven modules built, those two
      did not. Pre-existing on `main` rather than introduced here — main
      failed too, with a different error — but this step touched exactly
      those two files to fix `pkg`'s under-specification and stopped
      half-way. It matters now because **Dockerization is the next roadmap
      item but one**, and a standard Go Dockerfile is precisely the
      `GOWORK=off`, clean-cache case that this breaks. `go mod tidy` in both;
      `go.mod` was already tidy, so the change is three added `go.sum` hash
      lines. 7/7 modules now build off-workspace.

      Two documentation corrections from the same pass. `ListTrades`'
      interface comment claimed truncation was safe because a capped response
      "is a complete PREFIX... which reconstruction can use correctly" — true
      of `Reconstruct` alone, false of the system, since `Reconcile` compares
      the truncated derivation against the *live* account and degrades the
      whole report. Reworded to separate *internally consistent* from
      *usable* and to name the refusal as the intended direction. `README.md`
      still led with "in active development (Phase 3)" while the table under
      it announced Phase 4.

      Not acted on, recorded so "we chose not to" stays distinguishable from
      "nobody looked": the internal clients decode responses with no
      `io.LimitReader`, matching `backtesting`'s and `trading-engine`'s
      existing clients, so tightening it is a repo-wide convention change and
      not this step's; and `ai-insights` uses a bare `http.ListenAndServe`
      with no `ReadHeaderTimeout`, matching all four other engine services,
      where only the internet-facing gateway sets one — worth revisiting at
      deployment, when these services stop being loopback-only.
