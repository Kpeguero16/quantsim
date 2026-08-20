# QuantSim Phase 4 — AI Insights + Infra Checklist

Phase 3 is complete (`PHASE3_CHECKLIST.md`). Phase 4 delivers the AI insights
layer and the deployment work (`agents.md`'s roadmap: portfolio analytics,
insight generation, Dockerization, cloud deployment) — the third "Major
System" in `agents.md` §3, and the first phase that starts from an empty
service rather than extending one.

The phase splits the "AI" half in two on purpose. **Step 20 computes every
number** and ships them as a deterministic, fully-derived report. A later step
adds the LLM, which may only *phrase* numbers it is handed and may never
produce one. That ordering is the whole point: it means no figure a user reads
can be a hallucination, because by the time a model sees the report every
value in it has already been computed, tested, and reconciled against the
live account.

---

## Step 20: Portfolio Analytics (rule-based)

`services/ai-insights` as the sixth service, serving **one endpoint** —
`GET /insights/portfolio` — returning a deterministic analysis of the
authenticated user's live paper-trading portfolio across three sections:
**risk**, **benchmarking**, and **behavior**. No LLM, no frontend (Step 21,
per the Step 16 → 17 precedent), and **no database** — the service owns no
tables and reads everything over HTTP.

The hard part is not the statistics. QuantSim stores **no portfolio history**:
there is a trade log and there are daily bars, and nothing that records what
the account was worth on any past day. Every figure in the report is therefore
derived from an equity curve **reconstructed on demand** by replaying the
trade log against stored closes.

- [x] Spec drafted and reviewed — recommended scope accepted (`SPEC.md` §2),
      with §2.12 **amended mid-build** after it failed against real data —
      see below
- [x] Plan (`tasks/plan.md`) — 16 tasks across 5 phases, six added decisions
      (D1–D6)
- [x] `pkg/portfoliomath` — `Sharpe`, `MaxDrawdownPct`, `DailyReturns`,
      `Mean`, `StdevPopulation` extracted from `backtesting`, landed **first
      and alone** as a provably behavior-free commit (D1). Proof is
      mechanical, not a judgment call: `git diff --exit-code` on
      `backtesting`'s `metrics_test.go` is empty, and was re-checked at the
      end of the step
- [x] `GET /trading/trades` on `trading-engine` — **ascending** order,
      deliberately diverging from `ListOrders`' newest-first, with the reason
      in a comment on the store method so it doesn't read as an oversight next
      to its neighbour (D2)
- [x] `Calendar` — the flat **intersection** of every ever-held symbol's dates
      plus both benchmarks', **no carry-forward**
- [x] `Reconstruct` — replays the log into a cash/holdings fold and prices it
      at each calendar close
- [x] `Reconcile` — a runtime invariant that **refuses rather than repairs**
- [x] `ComputeRisk` — position weights, cash weight, HHI over *invested*
      positions only, largest position, annualized volatility, max drawdown
- [x] `ComputeBenchmarking` — buy-and-hold `SPY` and `QQQ` over the identical
      calendar, both non-optional
- [x] `ComputeBehavior` — three rules with named thresholds and **evidence
      trade IDs attached**, so every finding names the trades that caused it
- [x] Handler, router, and the gateway's `/insights/*` route inside the
      authenticated group; `ai-insights` revalidates the JWT itself rather
      than trusting the gateway (§6.5), the posture `trading-engine` set in
      Step 14
- [x] Redis cache, `insights:{user_id}`, 5-minute TTL, **fail-open both ways**
      and never invalidated on trade
- [x] Mutation-tested throughout — **80 mutants across the step**, plus a
      dedicated adversarial pass; see below
- [x] Manual pass against the real stack, with every reported figure
      recomputed independently from Postgres; see below

**Completed 2026-08-20.** Spec, plan and todo archived to
`docs/archive/phase4-step20-portfolio-analytics/`.

### The spec was wrong, and real data is what proved it

§2.12 originally said the reconciliation guard should refuse to report
whenever the reconstructed portfolio disagreed with the live account. At
Checkpoint C that guard blanked **the entire report** — all three sections
`insufficient_data` — for a portfolio with 39 days of clean history and one
1-share buy placed that morning.

The root cause is a sentence worth keeping, because it is not obvious until
you see it fail: **"the curve is truncated" and "the user traded after the
last stored close" are arithmetically the same event.** Both show up as
derived cash disagreeing with `accounts.balance`, and the guard could not
tell them apart — so the ordinary case of trading today, on a system whose
bars necessarily lag, was indistinguishable from corruption.

The fix (`projectPastCalendar`) replays post-calendar trades forward before
comparing. §2.12 was amended in place with a dated subsection recording the
failure, the cause, what is still caught, and **what was given back** — the
trade-off is explicit: truncation is no longer a refusal, it becomes an
as-of-date report. That is defensible only because truncation was always
*disclosed* by `as_of_date`; what made it dangerous was being silent about
being stale, not being stale.

The consequence propagated into §2.5: `risk.positions` are as of
`as_of_date`, **not** as of now, and can legitimately disagree with the live
`positions` table. The manual pass shows exactly this — a reported GOOGL
holding of 10 against a live 6, because the sell happened after the last
stored bar.

### A defect the amendment created, caught before merge

Making post-calendar trades survivable made them **routine**, and one rule
was not ready for that. `panicSelling` read the calendar without bounding
itself by `as_of_date` the way its sibling `overtrading` already did. For a
sell past the last stored close, `sort.Search` runs off the end of the
calendar and `calendar[i-1]/[i-2]` silently name the *window's last two
days* — so a recent sell could be reported as panic selling, **with its
trade ID attached as evidence**, on a price move weeks earlier that had
nothing to do with it.

Two things about how this was found are worth recording:

1. **The live run did not expose it.** GOOGL rose 2.3% on the last stored
   session, so the drop test failed on price direction alone; the sell had
   already passed the loss gate at −0.32. The green report was luck of the
   price path. Reading the code exposed it.
2. **The inconsistency with `overtrading` is what made it obviously a bug
   rather than a design choice.** One rule bounding its window and its
   sibling not bounding the same window is not two opinions; it is one of
   them being wrong.

Fixed by excluding post-calendar sells from the rule, **denominator
included** — counting a sell the rule refused to judge would dilute the share
test with a trade that never had a chance to qualify.

### What the mutation passes actually caught

Mutation testing ran per-task and then again across the whole step. The
headline number — 80 mutants, all caught but one verified live instead — is
less interesting than the survivors, every one of which exposed a test that
looked like coverage and wasn't.

| survivor | what it revealed |
|---|---|
| "drop threshold excludes exactly −5%" | `(95/100−1)*100 == -5.000000000000004` for **every** clean 5% fall — `0.95` has no exact binary form. `<=` and `<` were indistinguishable, so the test claiming to pin the boundary pinned nothing. Rewritten as a **price** comparison, where `100*(1−5.0/100) == 95.0` exactly |
| "overtrading denominator is last equity, not mean" | every fixture had a **flat** equity curve, so mean and final were the same number |
| `PanicSellMinOccurrences` 3 → 4 | 3 of 9 sells is 33%, over the 30% **share** arm — the count constant was masked by an alternative that always covered for it |
| "post-calendar sell still counted in the denominator" | 2/6 fires and 2/7 does not, but only when occurrences sit below the count arm |

The recurring shape, four times in one step: **two paths to one outcome hide
each other's boundary unless a test isolates them.** Also true of T7's two
holdings branches. Every instance had green tests and correct behavior; what
was missing was the ability to tell *which* mechanism produced the result.

Two process notes, both learned the hard way:

- **A mutant that doesn't build proves nothing.** Several `if true`/`if false`
  mutants failed to compile on an unused variable and were rewritten (e.g.
  `asOf.AddDate(100, 0, 0)`) so they actually ran. A compile error is not a
  caught mutant.
- **A mutant that no longer applies proves nothing either.** Three T8 mutants
  had gone stale as the code moved under them (`Reconcile` gained a parameter,
  `insufficientData` gained two sections, the handler now passes a user id).
  They reported INVALID rather than SURVIVED, so nothing *looked* broken —
  and they had silently stopped guarding T8's logic. Repointed.

### `omitempty` deleted a real measurement

The first time `RiskSection` was marshalled end to end, `concentration_hhi`
vanished for an all-cash portfolio — which is the exact answer §2.5 argues
for, with a test named after it. `omitempty` cannot tell "no value" from "the
value is zero", so it is wrong on every figure whose zero is a reachable
measurement and right only where zero is unreachable. Removed from all five
risk figures and from `positions`; kept on `reason`, and kept on `Finding`'s
`turnover_ratio`/`occurrences`, where a zero genuinely cannot occur.

### The manual pass (D5)

D5 drew a line the plan was explicit about: the reconstruction self-check
exists in **two forms, and only one of them is a test**. The unit property
checks the fold against an independent fold; the check against *real*
`accounts`/`positions` rows happens once, by hand, because `ai-insights` has
no database connection to run an integration test through and a test that
built its own rows would be checking the fold against itself.

Nine trades were placed through the live stack, eight backdated across stored
history and one left 23 days past the last stored bar. Every reported figure
was then recomputed independently in Python straight from Postgres — its own
calendar intersection, its own fold, its own statistics, never calling the Go
code. **All 19 matched to the last significant digit.**

**The self-check holds exactly**: derived cash `84102.5950` against a live
`accounts.balance` of `84102.5950`, and derived holdings
`{AAPL 10, AMZN 12, GOOGL 6, MSFT 10, TSLA 8}` against the live `positions`
rows — to the cent and the share.

Also verified live: §2.8's deliberate staleness (nine trades placed and the
endpoint still said "no trades yet", with `computed_at` unmoved — the field
§2.4 added to disclose exactly that); cache hits stable across three calls at
a 289s TTL; 401 at the gateway **and** directly at `:8085`; and a 404
`symbol_unavailable` naming `TSLA` with its bars removed, which was **not**
cached.

### One system property recorded, not fixed

With Redis stopped the endpoint returns **502**, and every layer is behaving
as designed: `ai-insights`' cache fails open correctly (it logs "cache read
failed, computing" and proceeds), and `trading-engine` degrades to unpriced
positions. But that degradation is *slow* — `GET /trading/portfolio` takes
**8.7s against 5.8ms healthy**, tripping §2.10's 5s upstream timeout.

So a Redis outage takes the whole report down even though every figure in it
comes from Postgres and **none of them needs a live quote**. This is
pre-existing (`market-data`'s price path is Redis-backed and `trading-engine`
retries slowly) rather than something this step introduced, and fixing it
means changing `trading-engine`'s retry behaviour — outside this step's
scope. Recorded here so it is a decision rather than a surprise.

### Left unaddressed as recorded judgment calls, not oversights

- **None of the behavioral thresholds is principled.** §2.7 admits this in the
  spec and `thresholds.go` repeats it at the constants: 2.0× turnover, 30
  days, −5%, 3 occurrences, 30% share, and the four risk-profile bounds are
  defensible defaults, not derived from anything. They are exported and named
  so that changing them is a one-line edit with a visible blast radius.
- **The cache is never invalidated on trade** (§2.8). A user who trades and
  immediately reloads sees a report up to five minutes stale. `computed_at`
  discloses it; the alternative — invalidating from `trading-engine` — would
  couple two services to make a rule-based report marginally fresher.
- **`ListOrders`' unbounded `LIMIT`** and the absence of an index on `trades`
  were both noticed while adding `GET /trading/trades` and both left alone;
  neither is this step's to change.
- **The gateway's `NewRouter` now takes five interchangeable `http.Handler`
  parameters.** A `Backends` struct would make a mis-ordered call impossible
  rather than merely unlikely. Deferred rather than done as a drive-by.

### The pre-merge review

Five axes, independently, before the squash — the gate Steps 16–19 each
passed through. Every green claim was re-run rather than carried over from
the session that made it: `vet` clean across seven modules, `test` green,
`-race` clean on three services, `test-integration` 63 passed / 0 failed,
D1's diff still empty, and the T5 dependency revert confirmed still holding.

**Two findings, both fixed before the merge.**

**R2 — `panicSelling` diluted its own share test.** The share of sells that
were panics, `occurrences / sells`, counted sells the rule had *refused to
judge* in its denominator. The cause is this step's signature failure for the
fifth time: `previousTradingDayDropped` returned one bare `false` for two
different facts — "there is no pair of prior sessions to judge against" and
"the prior session did not drop" — so the caller could not distinguish them.
A sell in the window's first two sessions is unjudgeable for exactly the
reason a post-`as_of` sell is; the post-`as_of` case had already been excluded
**with that reasoning written down in a comment**, and the early case, four
lines away, had not.

What makes it worth recording is that the direction is a false *negative*:
the rule goes quiet rather than inventing a finding, so no test failed and no
live run misbehaved. It was found by reading the two boundaries against each
other, and confirmed by a test written before the fix — 3 unjudgeable early
sells beside 1 real panic sell score `1/4 = 0.25`, under the 0.30 threshold,
and the finding stays silent.

The fix splits the conflated function into `priorSessions` (does a judgeable
pair exist, and which dates are they) and `droppedAcross` (given that pair,
did it drop), so judgeability is settled *before* the denominator increments.
The `After(asOf)` guard remains and now records why it cannot be folded in:
past the end of the calendar `sort.Search` returns `len(calendar)`, which
satisfies `priorSessions`' own bound and would name the window's last two
days.

**R3 — `pkg` and `services/gateway` did not build outside the workspace.**
`GOWORK=off go build ./...` failed both with `missing go.sum entry for go.mod
file`. Pre-existing on `main` rather than introduced here, but this step
touched exactly those two files to fix `pkg`'s under-specification and
stopped half-way — and it matters now, because **Dockerization is the next
roadmap item but one** and a standard Go Dockerfile is precisely the
`GOWORK=off`, clean-cache case this breaks. `go mod tidy` in both; `go.mod`
was already tidy, so the change is three `go.sum` hash lines. 7/7 modules now
build off-workspace.

Two doc corrections came from the same pass: `ListTrades`' interface comment
claimed a truncated response was usable by reconstruction, which is true of
`Reconstruct` alone and false of the system — `Reconcile` compares the
truncated derivation against the live account and degrades the whole report,
which is the intended direction and is now what the comment says; and
`README.md` still led with "Phase 3" above a table announcing Phase 4.

**Raised and deliberately not acted on**, so that "we chose not to" stays
distinguishable from "nobody looked":

- The internal clients decode upstream responses with no `io.LimitReader`.
  This matches `backtesting`'s and `trading-engine`'s existing clients, so
  changing it is a repo-wide convention decision rather than this step's.
- `ai-insights` uses a bare `http.ListenAndServe` with no `ReadHeaderTimeout`,
  matching all four other engine services; only the internet-facing gateway
  sets one. Worth revisiting at deployment, when these services stop being
  loopback-only.

---

## Still open

- [ ] **Insight generation** — the LLM layer that phrases Step 20's numbers.
      The contract is already fixed by Step 20's design: it may phrase only
      numbers it is given, and may never produce one.
- [ ] **Insights frontend** — Step 21, per the Step 16 → 17 precedent.
- [ ] **Dockerization** and **cloud deployment** (AWS free tier).
- [ ] **Work through `docs/deferred-tuning.md`** — timeouts, connection
      pooling, and other defaults deliberately left unset because the right
      values depend on traffic shape that only exists once deployed.
- [ ] **market-data's store still has no tests** — `historical_price_store.go`.
      Carried over from `PHASE3_CHECKLIST.md`; `ai-insights` owns no database
      (§2.9) so it did **not** become the harness's fourth copy, and the
      extraction trigger in `docs/TESTING_STRUCTURE.md` §6a is still unfired.
- [ ] Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`,
      untouched since Step 11 — carried over from `PHASE2_CHECKLIST.md` and
      `PHASE3_CHECKLIST.md`.
