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

## Step 21 — Insight generation: the LLM narrative layer

**Done.** `GET /insights/portfolio/narrative` on `services/ai-insights`: three
short paragraphs, one per Step 20 section, in which **every figure was rendered
by Go from the report struct and none was produced by the model.**

Branch `step21-insight-generation`, squashed and merged `--no-ff`. Spec, plan
and todo archived at `docs/archive/phase4-step21-insight-generation/`.

### How the guarantee is enforced

The model is handed the report *with* its values — it has to know a 34%
drawdown is severe and a 2% one is not — and must write prose in which every
figure is a named placeholder. Go substitutes from the struct. A surviving
digit rejects the draft.

Step 20 computed the numbers first so this step would not have to trust a model
with arithmetic; spending that on a prompt-level instruction would have wasted
it. The checks (`internal/narrative/validate.go`) are: no Arabic digit
anywhere, no number word or bare unit word, no placeholder that was not
offered, plus per-section and total caps. One retry quoting the offending
fragment, then refusal. **Nothing is ever repaired** — stripping a stray number
leaves a sentence that still parses, still reads fluently, and now claims
something its author did not write.

The test that states the property: **a draft carrying a *correct* figure — the
true drawdown, formatted exactly as the renderer would format it — is still
rejected.** Correctness is not the criterion; provenance is. A validator that
accepts that case has become a whitelist.

### Two carry-over items landed first

- **`gofmt` drift in `services/auth`** (open since Step 11) — cleared. Note
  that `git diff --ignore-all-space` alone does *not* prove such a commit is
  formatting-only: it ignores whitespace *within* a line but still reports an
  added blank line and a trailing newline. Proved instead by comparing each
  file's whitespace-normalised token multiset.
- **`trading-engine`'s portfolio pricing** — the Redis-outage 502 recorded at
  the end of Step 20. **The recorded diagnosis was wrong:** there is no retry.
  `Service.price` was a sequential loop issuing one lookup per holding at 3s
  each, so per-symbol timeouts **composed additively** and the endpoint's worst
  case was N × 3s, unbounded in N. Now concurrent under one `PricingBudget`.
  Measured against 5 holdings with a hung Redis: **15.014s → 3.007s**, exactly
  5 × 3s versus 1 × 3s.

### What the verifications actually proved

**397 tests** in `ai-insights`; `vet`, `test`, `-race`, `test-integration`
(63/0) and `GOWORK=off` all green across seven modules, re-run on `main` after
the merge. **28 mutations run**; every one killed.

Three defects were found by mutation testing or by asserting on call counts —
**none by a test that was failing beforehand**:

- **The daily cap was reserved before discovering there was nothing to
  generate.** A no-trade account polling the endpoint would burn its entire
  quota on zero API calls, then be refused the day it finally had something to
  describe — a cost control turning into a denial of service against the users
  it costs nothing to serve. The response was correct throughout; only the
  counter's call count showed it.
- **`context.WithTimeout` around Redis did nothing.** go-redis v9's
  `ContextTimeoutEnabled` defaults to `false`, and while it is false the client
  ignores deadlines entirely and waits its own `ReadTimeout`. That is the real
  cause of the 6.05s measured at Checkpoint 0 — code that reads as bounded,
  compiles, and waits the full default anyway. Both caches are bounded now via
  a constructor that makes it impossible to get wrong at a call site.
- **A mock that reimplements the logic it stands in for cannot test it.** The
  handler's cap-boundary tests drove the *mock* counter, so the real
  implementation's comparison and its INCR-as-reservation were exercised by
  nothing; two mutations survived and exposed it. miniredis now drives the real
  counter.

**SPEC §2.3's single-source claim was resting on a test that could not fail.**
Every `BuildPrompt` test handed it exactly what `Placeholders` would produce,
so rebuilding the vocabulary internally changed nothing — the tests proved the
two agree *when built the same way*, which is the drift they were written to
preclude. Now pinned with a vocabulary `Placeholders` would never return.

**A mutant that does not apply is not a caught mutant**, and it looks exactly
like coverage. Two reported as SURVIVED purely because a replacement string
did not match — one had dropped a `§`, one had wrong regex escaping.

### The manual pass, and what only a real report could show

Against a real portfolio (2 holdings, 3 trades, 40 trading days), **25 of 25
figures verified by eye against the JSON**, no advisory language, and the
degraded, cached, capped and Redis-outage paths all exercised. ~10 billable
calls, roughly $0.20.

Four defects that unit tests could not have found:

1. **`max_drawdown_pct` rendered as a signed percentage.** `pkg/portfoliomath`
   reports drawdown "as a positive percentage", so a 1.7% fall printed as
   **"+1.7%"** and read as a gain — in a sentence that said "fell". The unit
   fixture had used `-12.4`, *a value no code path can produce*, which made the
   wrong kind look right. Fixtures now mirror a real response.
2. **A rejection quoted only the bare word.** "three" cannot tell you whether
   the model was counting trades (which has a token) or benchmarks (which did
   not), and those want different fixes.
3. **No token existed for the benchmark count**, so "all three" had no legal
   alternative and cost a whole generation.
4. **The prompt was inviting the rejection it punishes** — it called HHI "an
   index between zero and one", then a draft was discarded for "a value near
   one".

### The pre-merge review, by attacking rather than reading

Two more defects, neither of which any test was failing on:

- **A hostile symbol corrupts the token vocabulary and reaches the prompt as
  data.** Symbols and finding codes arrive over HTTP from `trading-engine` and
  are interpolated straight into token names, so `X} {risk.max_drawdown_pct`
  produced a token the renderer parses as *two*, and a symbol containing a
  sentence would be injected verbatim into the prompt — the one
  prompt-injection surface this design has. It fails closed (an error, not a
  leaked figure) and is unreachable today, because a position only exists for a
  symbol `market-data` could price — but that constraint lives two services away
  and is not this package's to assume. `safeName` now drops anything outside
  `[A-Za-z0-9._-]{1,32}`.
- **The offending fragment was byte-sliced**, so a window around an offence
  could cut a multi-byte rune in half — and that fragment goes into the retry
  prompt sent to the API and into the log line. `around` and `truncate` snap to
  rune boundaries.

Also confirmed in review: `price()` is reachable only from `Positions` and
`Portfolio`, so Step 21's concurrency change cannot touch the order path.

### §6.1 answered with evidence

The banned-word list bans `couple` and `pair`: "a couple of names" for a
two-holding portfolio is true, checkable, and still a figure the model wrote
itself. **`both` was banned for exactly one run and removed** — it cost a
generation for "the two figures are both small", where both figures had already
been named by their own placeholders. The rule is *the model states no figure*,
not *the model uses no number-ish word*; widening past that costs real drafts
and buys nothing. `few`, `several` and `many` stay allowed. Ordinals other than
fraction words were never banned.

First-draft rejection rate went from **3 of 4 drafts to 0 of 1** after the
vocabulary and prompt fixes.

### Things worth knowing

- **`docker stop` does not reproduce a Redis outage.** A stopped container
  *refuses* connections in microseconds, so the unfixed sequential pricing loop
  finished in 2.5s and looked fixed while still broken. `docker pause` is the
  right shape: connections accepted, never answered.
- **The percent format is set by this step and Step 22 must follow it.**
  `frontend/src/format.ts` has `formatPrice`, `formatQuantity` and `formatDate`
  but **no percent formatter**. Every numeric kind here rounds halfway cases
  **away from zero**, because Go's `FormatFloat` rounds them to even while
  `toFixed`, `toLocaleString` and `Intl.NumberFormat` all round away — an exact
  7.25 is `7.2` under Go's rule and `7.3` in a browser. The parity test found
  this; matching the browser is what lets Step 22 write the obvious one-liner
  and still agree with the sentence beside it.
- **A cache hit returns no `generated_at`**, which is how a hit is told from a
  fresh generation. Identical figures give identical prose, word for word —
  correct, and it will read as staleness to someone expecting a new take.
- **`SPEC.md` §2.9's unconfirmed `effort` binding exists**:
  `anthropic-sdk-go` v1.66.0 has `OutputConfigParam.Effort`. Nothing was
  deferred to `docs/deferred-tuning.md`.
- **A refusal is an HTTP 200 with a stop reason**, not an error. Reading the
  content first turns it into an empty draft and burns the retry on something a
  retry cannot fix.
- **An `httptest` handler that blocks on the request's own context deadlocks
  the test** — `Close` waits for outstanding requests. It needs a release
  channel closed by a function `defer`, which runs before any `t.Cleanup`.

---

## Step 22 — Insights frontend: the report on the page, and the prose after it

**Done.** A sixth dashboard tab renders `GET /insights/portfolio` as three
sections of figures, with `GET /insights/portfolio/narrative` filled in beneath
each section as it arrives. Plus one additive backend field, `report_hash` on
the report response, so a separately-fetched narrative can be checked against
the figures actually on screen.

Branch `step22-insights-frontend`, squashed and merged `--no-ff`. Spec, plan
and todo archived at `docs/archive/phase4-step22-insights-frontend/`.

### The percent convention, and the note Step 21 left that was wrong

Step 21 recorded that every browser formatter rounds halfway cases away from
zero, so this step could match it "with the obvious one-liner". **That is true
of the rule and false of `toFixed`**, and the difference is reachable.
`toFixed` rounds the exact *binary* value: `-99.85` is really
`-99.8499999999999943`, so `toFixed(1)` prints `-99.8` where Go's
scale-then-round prints `-99.9`. Over 60,002 constructed decimals in ±100,
`toLocaleString` disagreed with Go on **0** at one decimal place and `toFixed`
on **960**.

`toLocaleString` is not a free pass either. It rounds the shortest decimal
form, which agrees at one place but diverges at two and three — 272 and 184
mismatches over 270,002 values — and Sharpe (2dp) and HHI (3dp) live there. So
`format.ts` **ports** `roundHalfAway` instead of calling a one-liner: round the
magnitude, reapply the sign, render with fixed digits and no grouping. 0
mismatches over 330,004 values across all three precisions.

`render.go`'s comment has been corrected in place; it was the note most likely
to mislead whoever touched this next.

### Two parity tests, because they catch different faults

- `format.test.ts`'s table owns **rounding**. Its inputs are adversarial
  halfway cases; mutating `fixed()` to `toFixed` turns 14 of them red.
- `insights/parity.live.test.ts` owns **formatter selection** — a signed
  percent rendered unsigned, an HHI at two places, a Sharpe at one. Its
  fixtures are a real report and the real narrative describing it, captured
  from the running stack, and it asserts all 13 figures appear character for
  character in the Go prose.

Neither subsumes the other, and this was measured rather than assumed:
**mutating `fixed()` to `toFixed` leaves the live file entirely green**,
because no figure a real portfolio produces lands on a halfway case. Dropping
the sign, or moving the HHI to 2 or 4 places, or the Sharpe to 1, turns the
live file red and leaves the table green.

The live assertion needed two rounds of sharpening, both found by mutating
rather than reading: a bare `toContain` accepted `"5.9%"` inside `"+5.9%"`, and
a left-only boundary accepted `"0.50"` inside `"0.504"`, which let a 3dp→2dp
HHI mutation survive.

### The guard nothing was holding

Deleting a section's `state` check **together with its now-unused
`SectionNotice` import** — which is what an editor's remove-unused-imports fix
does unprompted — left `npm test`, `npm run build` and `npm run lint` all
green while a degraded report rendered `0.0%` for all five risk figures.
Deleting the check alone failed the build, but only on the orphaned import,
which would not survive a tidy-up. Nothing tested, built or linted defended the
rule the spec's "Never" list puts third.

Fixed structurally rather than recorded: each section type is now a union on
`state`, so the figures exist only on the `ok` arm and reading one without
branching is a compile error naming the field — 12 fields across the three
sections. No logic changed; the components already branched correctly. These
types deliberately **model what may be READ, not what the wire sends** — the
degraded arm hides fields the response really does carry, because Step 20 left
them without `omitempty` on purpose. That divergence is documented at
`DegradedSection`.

`SectionState` was retired in the process. Its own comment claimed only a union
could make the compiler enforce the branch — true, and something a state field
on a flat interface never did.

### The narrative that never appeared

The first time anyone opened the tab in a browser, the panel sat on "Preparing
a written summary…" forever. Both requests returned 200 and the console was
clean; the response was being discarded, not awaited.

The tell was the request count across one mount: **report twice, narrative
once.** `useInsights` has no in-flight guard, so StrictMode's re-invoked effect
simply fires a second request that lands. `useNarrative` has one, and the two
guards deadlocked: the cleanup bumped `requestIdRef` to disown the in-flight
request, the guard then blocked the re-run from issuing a replacement, and the
response arrived to a bumped id and was dropped with nothing left to re-trigger
it. `load()` now re-adopts the in-flight request's id.

Development-only in effect — production does not double-invoke effects, and a
real remount allocates fresh refs — but it blocked every browser check of the
narrative, and the old code's correctness rested on refs being recreated.

**It landed in the one place deliberately left untested.** The double-spend
guard was recorded as a carry-over an hour earlier, on the argument that
standing up `renderHook` late in the step cost more than it bought. The first
bug found after that decision was in that guard's interaction with its
neighbour.

### Two backend defects this step cannot fix

Both were found by driving the real stack, and both contradict something
`SPEC.md` asserted; the spec now carries inline amendments at §2.9 and §9.2.

**The fill-invalidation window.** §2.9 says a fill refetches the report and its
hash changes. The refetch fires correctly, exactly once. But the backend serves
the cached report from `insights:{user_id}` (TTL 300s), so the identical report
and hash come back — after a real fill the panel still read "no trades yet".
For up to five minutes the reader sees figures that predate their own trade,
with nothing marking them stale.

**`ReportHash` is not stable for unchanged data**, and this is the more
consequential of the two. Twelve recomputes of one untouched account produced
**six distinct hashes**. The figures shown were identical; the drift is in the
last floating-point digits of `portfolio_sharpe`,
`annualized_volatility_pct` and `concentration_hhi`. Ordering is not the cause
— positions and benchmarks return stably, `concentrationHHI` is a sequential
loop — and `ReportHash` correctly zeroes `ComputedAt`. The drift is upstream in
the reconstruction, which fetches histories concurrently through an `errgroup`;
float addition is not associative.

It breaks two claims. The narrative cache key is
`narrative:{user_id}:{report_hash}`, so a new hash is a cache miss and a cache
miss is a **billable generation** — §9.2's "one generation per day for an
unchanged account" is wrong, and sustained viewing can cost roughly one per
five minutes, bounded only by the 50/day cap. And it makes §2.3 fire in the
*false-positive* direction: two independent computations of identical data can
disagree, so correct prose is occasionally replaced by a staleness warning. A
mechanism built to stop a wrong number reaching the reader will sometimes
suppress a right one.

The frontend is not at fault in either case; `describesReport` failing closed
on two disagreeing hashes is still correct.

### What the verifications actually proved

**179 frontend tests** across 9 files; `npm run build` and `npm run lint` clean
(5 warnings, all pre-existing `exhaustive-deps` on sibling hooks). `make vet`
and `make test` green across all seven modules. **31 mutations run in total**,
21 during implementation and 10 in the adversarial pass; **29 killed, 2
survived** — the section guard, fixed structurally above, and the double-spend
guard, which the browser then broke for real.

The manual pass drove all four states against the running stack: a degraded
report, a mixed report with one `ok` section beside two degraded ones, a full
`ok` report over a 72-trading-day window, a narrative made unavailable by
unsetting the key, and a forced hash disagreement with its regenerate click.
Four billable generations, about $0.08.

### Things worth knowing

- **A `git checkout --` revert inside a mutation driver will silently discard
  an uncommitted fix in the same file.** It happened twice. Both times it was
  caught only because mutations that had previously reported `build=PASS`
  started reporting `build=FAIL`. Restore from a copy of the pre-mutation file,
  not from `HEAD`, whenever the tree carries uncommitted work.
- **`vitest` does not typecheck.** A `@ts-expect-error` proves nothing under
  `vitest run`; it has to be confirmed with `tsc`, and confirmed by *removing*
  the suppression and watching the error appear.
- **Adding a `useRef` to a mounted component cannot hot-reload.** React Fast
  Refresh raises "Rendered more hooks than during the previous render" and the
  page goes blank. A full reload fixes it — and takes the memory-only token
  with it, so it costs a sign-in.
- **`docker exec` without `-i` silently discards a heredoc.** The `psql`
  invocation returns success having run nothing.
- **The degraded narrative path costs nothing.** `no analysis is available to
  describe` is decided before any model call, confirmed by an untouched service
  log and an absent cache key.
- **oxlint's `exhaustive-deps` stopped flagging `use-narrative`'s cleanup** once
  `requestIdRef.current` was also written in `load` — the rule only treats it as
  a cleanup-only ref. The cleanup itself is unchanged; the comment was corrected
  so it no longer claims a warning that does not appear.
- **`toLocaleString('en-US')` emits an ASCII hyphen, not U+2212.** Checked in
  the browser rather than assumed, because a typographic minus would have broken
  character-identity with Go while looking correct on screen.

---

## Step 23 — ReportHash stability

Closes the defect Step 22 called the one that mattered most. `ReportHash` was not stable for unchanged data, so the narrative cache never hit, every view billed a fresh generation, and Step 22 §2.3's report-versus-narrative check warned about disagreements it had invented itself.

### The cause was two loops, and neither looked wrong

`Reconstruct` summed each date's equity over `range holdings`, and `ComputeRisk` summed `invested` over `range r.Holdings`. Go randomizes map iteration order per pass and float64 addition is not associative, so identical holdings at identical closes summed to results differing in their last bits. The equity loop runs once per date, so a 79 day window compounds it.

Nothing displayed ever changed. Every figure rounds the difference away at 1, 2, 3 or 4 decimals. `ReportHash` is defined on the serialized bytes and saw all of it.

### Why it survived three steps of review

**The existing order test asserted a tolerance the hash does not have.** `TestReconstruct_SameTimestampTradesAreOrderInsensitive` compares through `assertFloats` at `eps = 1e-9`. The drift is around 1e-11 on values near 1e5. The test was right about what it checked.

**`TestReportHash_IsStableAcrossCalls` already existed and passed throughout.** It hashes one struct value twice, which proves the function is pure and says nothing about whether computing a report twice yields the same struct. Its name claimed the property the system lacked. It now carries a comment pointing at the test that owns the real one.

**The codebase had the right instinct, aimed one step short.** `calendar.go` and `risk.go` both already say results must never be left in map iteration order. Both are about ordering something a reader sees. Neither anticipated that an accumulation nobody sees needs the same discipline for a different reason.

### The fixture nearly proved the wrong thing

The first bit-stability fixture showed 1 distinct equity curve over 200 runs against the **unfixed** code, which reads as "no bug here". `bars()` closes at whole dollars, whole dollars are exact in binary, and Go's small-map randomization gives rotations rather than permutations, so seven rotations of seven exact values agreed. Rounding the closes to cents took the same test from 1 distinct curve to 199. `driftBars` carries that in its comment, because the next person writing a stability test here will reach for `bars()`.

### Rounding the hash was rejected

`NEXT_SESSION.md` proposed rounding each figure to its published precision before hashing. Published precision is per `Kind`, so that needs every float field mapped to one, which is an include-list, and `hash.go` spends a paragraph explaining why this struct is hashed by exclusion. It would also put a third copy of the precision rule beside `narrative/render.go` and `frontend/src/format.ts`. Sorting the accumulation is four lines, removes the cause rather than the symptom, and fixes the figures too rather than only the hash. SPEC §4.1.

### What the verifications actually proved

| | |
|---|---|
| Backend | `make vet` clean; `make test` green across all seven modules; `make test-integration` **63/0**, unchanged; `GOWORK=off go build ./...` passes for all seven |
| Tests | Three added, each confirmed to fail against the unfixed code, and each owning a different loop. Reverting only `risk.go` leaves the reconstruction test green and the other two red. |
| Mutations | **5 run, 4 killed, 1 intentional survivor.** Reversing the sort order survives and should: the spec asks for reproducibility, not a particular order. |
| Live stack | Seeded three-position account, 79 trading days, `insights:{user_id}` cleared between calls. **Unfixed: 9 distinct hashes over 10 recomputes. Fixed: 1 over 12.** |
| Cost | **$0.00.** The narrative endpoint was never called. |
| Dev database | Restored and verified by query: `users=20 accounts=20 trades=0 orders=0 positions=0`, `historical_prices=3525`. No `insights:*` or `narrative:*` keys. |

The live run also settled a question the spec had left open: two separately started processes produced the same hash for the same account, so it is stable across restarts.

### Things worth knowing

**A three-position account hides half this bug.** `weight_pct` and `concentration_hhi` were bit-identical across all ten unfixed live runs while `portfolio_sharpe` drifted every time. That account would have cleared a `risk.go` audit completely. It is why the invested loop has its own test rather than riding the end-to-end one.

**A passing `/healthz` proves a server is there, not that it is yours.** Restoring the fix and restarting produced 11 distinct hashes out of 12, which reads as the fix failing. `pkill -f "ai-insights/cmd/server"` matched nothing, because `go run` compiles to a temp binary whose process is named `server`. The old process kept port 8085, the replacement died with `bind: address already in use`, and the health check returned 200 from the wrong build throughout. Confirm the log says `listening` and that the PID holding the port is new.

**An equivalent mutant is a design signal.** A scratch slice threaded through `sortedSymbols` to avoid per-date allocation produced a mutant that could not be killed, because the scratch was length zero and never reassigned, making `scratch[:0]` and `scratch` the same slice. Benchmarking said it saved 23 allocations and nothing measurable in time. Removed.

**Determinism tests and correctness tests do not cover for each other.** Mutating `equity += holdings[symbol] * px` to `equity += px` survives all three new tests, which only ever ask whether two runs agree, and dies against eight existing ones. Same property Step 22 recorded for `format.test.ts` and `parity.live.test.ts`.

---

## Step 24 — Report cache invalidation on a fill

Closes the last of Step 22's three defects. A fill's report refetch was defeated by the five-minute `insights:{user_id}` cache, so for up to five minutes the dashboard showed figures computed before the reader's own trade, unmarked.

### Write-side, and the read-side case was real

trading-engine deletes the key when it fills an order. The alternative — ai-insights checking freshness before serving its cache — keeps the dependency direction as it already runs and needs no Redis in trading-engine, which is a genuine argument. It lost on two facts. `ListTrades` orders `executed_at ASC`, so `?limit=1` returns the *oldest* trade and there is no cheap freshness probe to call; read-side needs a new endpoint first. And it would pay an HTTP round trip on every cached read, forever, to catch something that happens once per fill, against a cache that exists to avoid round trips.

Redis pub/sub was ruled out on the way past: it is fire-and-forget, so a subscriber that is down loses the message and the report stays stale, which is the defect being fixed wearing a different hat.

### Three details, each of which would have shipped looking correct

**Synchronous, not a goroutine.** The event this exists for is the dashboard refetching immediately after a fill. A goroutine racing that refetch has no ordering guarantee at all, and losing the race restores the stale report for the full TTL. This is the part most likely to be "tidied up" later into a background send, which is why the comment at the call site says so.

**`context.WithoutCancel`.** The fill has committed, then the client hangs up, the request context is cancelled, the `DEL` never runs, and the stale report survives — the original defect returning through a path nobody would think to look at.

**It can never fail an order.** The trade is durable before invalidation runs, so there is nothing to roll back and nothing a retry could fix. Errors are logged and swallowed, and `REDIS_URL` is optional: without it trading-engine behaves exactly as it did before this step.

### The key had to stop being a literal

`insightsKey` was unexported inside ai-insights. Two services produce that string now, and the obvious version — `"insights:" + userID` written out in trading-engine — leaves a format change breaking nothing at compile time and everything at runtime, in the direction that serves stale reports rather than the one that errors.

It moved to `pkg/cachekeys` and takes a `uuid.UUID` rather than a string. ai-insights parses the JWT subject to a UUID before keying on it precisely so an arbitrary subject cannot become an arbitrary Redis key, and so a subject containing a colon cannot be shaped to look like another namespace. A string parameter lets a caller opt out of that; this signature does not.

### What the verifications actually proved

| | |
|---|---|
| Backend | `make vet` clean; `make test` green across all seven modules; `make test-integration` **63/0**, unchanged; `GOWORK=off go build ./...` passes for all seven, including `pkg`'s new package |
| Tests | Ten across three packages, including the invalidator driven against `miniredis` rather than a mock that would reimplement the delete it stands in for |
| Mutations | **6 run, 6 killed** |
| Live stack | With Redis: key deleted on the fill, report and database agree at 6 trades. Without: key survives, report stale at 4 while the database held 5 |
| Cost | **$0.00.** The narrative endpoint was never called |
| Dev database | Restored and verified by query: `users=20 accounts=20 trades=0 orders=0 positions=0`, `historical_prices=3525`. No `insights:*` or `narrative:*` keys |

### Things worth knowing

**The narrative cache needs no invalidation, and a fill now costs a generation.** `narrative:{user_id}:{report_hash}` is keyed on content, so a fill changes the report, changes the hash, and misses. Nothing to delete. The consequence is a real cost change: after this step, the next narrative view following a fill is a fresh billed generation. That is correct, since the prose describes figures that moved, and it only became possible once Step 23 made the hash stable enough to key on.

**A rejected order must not invalidate.** It writes an `orders` row and nothing else, and the report never reads that table. Invalidating would discard a valid cached report to recompute a byte-identical one. The mutation that moved the call above `ExecuteOrder` was caught by that test and nothing else.

**A position quantity does not move after a fill, and it looks like a miss.** Holdings describe `as_of_date`, where the bar calendar ends, and a trade after that date is projected forward for the reconciliation guard only. §2.12's documented tail truncation. `behavior.trade_count` is the figure that moves, and it is the one to check when verifying this by hand.

**A mutant that does not build is not a caught mutant, again.** Replacing `cachekeys.Insights(userID)` with a literal left the import and the parameter unused, so it failed to compile and would have read as KILLED in a results table. Re-run as `cachekeys.Insights(uuid.Nil)`. Step 21 recorded this and it still cost a cycle.

**A test that cannot reach its own code path passes anyway.** Cancelling the context before `PlaceOrder` is refused at the price fetch and never reaches a fill, so the cancellation test proved nothing until `mock.TradingStore.OnExecute` let it cancel *during* the fill. Before that hook it passed against code containing no `WithoutCancel` at all.

**`auth` and `market-data` build Redis clients without `ContextTimeoutEnabled`.** Found while surveying for this step, not fixed by it. Both ignore context deadlines on every Redis call, which is the same defect that cost 6.05s in Step 21, sitting unexercised in two services. Now in "Still open".

---

## Still open

- [x] ~~**Insights frontend** — Step 22~~ — done. The percent convention was
      followed by porting `roundHalfAway` into `format.ts` rather than by the
      one-liner Step 21 expected; see the Step 22 entry for why.
- [x] ~~**`ReportHash` is not stable for unchanged data**~~ — Step 23. The
      cause was two float64 accumulations running in map iteration order, not
      the rounding `NEXT_SESSION.md` expected; see the Step 23 entry for why
      rounding the hash was rejected.
- [x] ~~**A fill's report refetch is defeated by the 5-minute report cache**~~ —
      Step 24. trading-engine deletes `insights:{user_id}` after a fill, with
      the key format shared through `pkg/cachekeys` so the two services cannot
      drift apart silently.
- [ ] **`auth` and `market-data` build Redis clients without
      `ContextTimeoutEnabled`** — so `context.WithTimeout` around their Redis
      calls does nothing and each waits the client's own `ReadTimeout`. The
      defect that cost 6.05s in Step 21, latent in two services. Consolidating
      all four construction sites is its own step; it touches auth's token
      revocation path.
- [ ] **The frontend hooks have no tests at all** — `use-narrative`'s
      double-spend guard protects a billed call and broke in Step 22 without a
      single test noticing. Needs `renderHook`; `@testing-library/react` is
      installed and still unused.
- [ ] **Dockerization** and **cloud deployment** (AWS free tier).
- [ ] **Work through `docs/deferred-tuning.md`** — timeouts, connection
      pooling, and other defaults deliberately left unset because the right
      values depend on traffic shape that only exists once deployed.
- [ ] **market-data's store still has no tests** — `historical_price_store.go`.
      Carried over from `PHASE3_CHECKLIST.md`; `ai-insights` owns no database
      (§2.9) so it did **not** become the harness's fourth copy, and the
      extraction trigger in `docs/TESTING_STRUCTURE.md` §6a is still unfired.
- [x] ~~Pre-existing `gofmt` drift in `services/auth`~~ — cleared in Step 21.
- [ ] **Security backlog item 8** (Unicode-normalise passwords) — the cheap one
      left, and it gets more expensive as accounts accumulate. Its own step.
- [ ] **Security backlog item 3** (Argon2id) — its own step; carries a
      migration strategy.
