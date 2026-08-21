# Todo — Insight Generation: the LLM Narrative Layer (Step 21)

Tracks `tasks/plan.md`'s 15 tasks (2 carry-over + 13). **C1, C2, T1–T11 done; Checkpoints 0, A, B and C passed. T12 is BLOCKED on an API key; T13 waits on T12.**

Branch `step21-insight-generation`. `SPEC.md` **drafted, awaiting approval** — §6.1 (banned-word
list) and §6.4 (daily cap) ship at their recommended values per plan D6 and are the two things
T12 revisits with real output in hand. One commit per task.

Baseline, re-checked at every checkpoint:
`users=20 accounts=20 trades=0` (T12's manual pass moves `trades`; it restores them).

**Spend, re-checked at every checkpoint:** Phases 1–3 must record **zero** billable calls. The
first one is T9.

| Checkpoint | Billable calls to date |
|---|---|
| 0 | 0 |
| A | 0 |
| B | 0 |
| C | 0 |
| D | — (blocked, no API key) |
| D | — |
| E | — |

## Phase 0 — Carry-over items, landed first (D7)
- [x] C1 `gofmt` drift in `services/auth` — alignment, one blank line after the package clause,
      and a missing trailing newline on `types.go`. `gofmt -l` now empty across all seven modules.
      **The planned proof was stated imprecisely.** `git diff --ignore-all-space` alone is *not*
      empty here: it ignores whitespace within a line but still reports the added blank line and
      the trailing newline, both whole-line changes. The check needs `--ignore-blank-lines` too.
      Proved instead by comparing the whitespace-normalised **token multiset** of each file before
      and after — identical for both.
- [x] C2 Bound `trading-engine`'s portfolio pricing — concurrent under one `PricingBudget` for the
      whole pass, written by index into a pre-sized slice so order stays the holdings' order.
      **The recorded diagnosis was wrong and the correction is confirmed live:** there is no
      retry; `price()` was a sequential loop whose per-symbol timeouts composed additively.
      Measured against a 5-holding portfolio with Redis hung — **sequential 15.014s, concurrent
      3.007s**, i.e. exactly 5 × 3s versus exactly 1 × 3s. Healthy is 3.4ms either way.
      Mutations: removing the overall deadline hangs `TestPositions_PricingImposesItsOwnBudget`
      until the test timeout; sharing one error across the goroutines fails the independence test.
      **A test that passed against the broken code, caught before implementing.** The first draft
      of the budget test passed a 900ms parent context and went green on the *unfixed* sequential
      loop — the parent was supplying the bound and the service was never exercised. Rewritten to
      pass a deadline-free parent, so the bound must come from the service. A test whose deadline
      is tighter than the budget it means to check is testing its own setup.
- [x] Checkpoint 0 — `/insights/portfolio` returns **200** with Redis hung. Baseline restored and
      verified by query (`users=20 accounts=20 trades=0 orders=0 positions=0`); all services
      killed; `lsof` on 808x clear.
      **Three findings from the live pass, each of which changes a later task:**
      1. **`docker stop` is the wrong outage and does not reproduce the bug.** A stopped container
         *refuses* connections in microseconds, so every lookup fails fast and the sequential loop
         finished in 2.5s — it looked fixed while still broken. The 8.7s came from something that
         *hung*. `docker pause` is the right shape: connections accepted, never answered. **T12
         must use `docker pause`, not `docker stop`.**
      2. **`ai-insights`' own cache read is unbounded against a hung Redis** — the endpoint took
         **6.05s** on the zero-trade short-circuit path, which never touches `trading-engine` at
         all. It fails open correctly, but slowly. T6/T7 must bound their Redis calls with a
         context, or the narrative endpoint adds a second such hang on top of this one.
      3. **A careless `pkill -f` pattern killed sibling services mid-measurement** and produced a
         bogus 8ms reading that looked like a spectacular result. Verify what is still listening
         before trusting any timing number.
      **Not proved directly, and left to T12:** that a portfolio *with trades* returns 200 rather
      than 502. With `trades=0` the report short-circuits at `insights.go:157` before it ever
      calls `trading-engine`, so the hop this task fixed is not on that path. The claim follows
      arithmetically — 3.007s is inside the 5s upstream timeout where 15.014s was not — but it is
      an inference, not an observation. Building a consistent order+trade chain by hand to close
      that gap risked leaving inconsistent rows behind for marginal evidence.
      **Incidental:** `NVDA` has **no** rows in `historical_prices`. The seven symbols with bars
      are AAPL, AMZN, GOOGL, MSFT, QQQ, SPY, TSLA — T12 fixtures must stay inside that set.

## Phase 1 — The guarantee, as pure functions
- [x] T1 The placeholder vocabulary — `Placeholders()` + the kinded `Value` type (D1). A section
      not in `state: ok` contributes nothing, and the same rule reaches one level down to the two
      finding fields carrying `omitempty`, where zero means absent rather than measured-as-zero.
      `largest_position_symbol` is withheld for an all-cash portfolio — nothing is the largest of
      nothing, and a wrong ticker reads as confidently as a right one.
      Mutations caught: risk emitted unconditionally; the `omitempty` zero ignored;
      `largest_position_symbol` offered with no positions.
- [x] T2 The renderer and its formatting. Money/quantity/date parity with `format.ts`, with the
      expected strings **produced by running that file's exact `toLocaleString` calls under node**
      rather than recalled.
      **That parity test found a real divergence and it changed the design.** Go's `FormatFloat`
      rounds halfway cases to even; every JavaScript formatter the frontend would reach for
      (`toFixed`, `toLocaleString`, `Intl.NumberFormat`) rounds them away from zero. An exact 7.25
      is `7.2` under Go and `7.3` under the browser. Since `format.ts` has **no** percent
      formatter, this step sets that convention — and setting it to Go's rule would mean Step 22
      disagreeing with the sentence beside it the moment anyone writes the obvious one-liner.
      Every numeric kind now rounds away from zero. `KindPercent` split into signed and unsigned:
      a weight's sign carries no meaning, a return's is the point.
      Mutations caught (5): half-to-even restored; unknown token passing through; kindless value
      rendering empty; money losing its grouping; dates rendering zero-padded.
- [x] T3 The validator — three checks, caps, typed `ValidationError`, fixed check order so the
      same bad draft always reports the same reason. **The correct-number-still-rejected test
      passes**: a draft carrying the true drawdown, formatted exactly as the renderer would format
      it, is still rejected. Correctness is not the criterion; provenance is.
      **Two checks added beyond the spec, both from writing it out:** bare unit symbols (`%`, `$`)
      are rejected, since the renderer supplies the unit and `{x}%` would render `-12.4%%` — a
      model reaching for a unit expected to write the figure too; and nested braces are caught
      separately from unbalanced ones, since `{a{b}` has matching counts and still cannot be read.
      Mutations caught (7): digit check disabled; a banned word removed; unknown-token check
      disabled; unit-symbol check disabled; word boundaries dropped; section cap raised; total cap
      ignored.
- [x] Checkpoint A — `make vet` and `make test` green across all seven modules; `go test -race`
      clean on `ai-insights`; `GOWORK=off go build` passes for all seven. **92 tests** in
      `internal/narrative`. Billable calls: **zero**. 15 mutations run across T1–T3, 15 caught,
      none survived.

## Phase 2 — A narrative end to end, with no model behind it
- [x] T4 `generate.go` — draft → validate → retry once → render. Exactly two attempts, bounded by
      a literal rather than a condition someone can widen without noticing what it costs. A code
      fence is tolerated (refusing would spend the single retry on formatting, not the guarantee).
      **Deviation from the plan:** the `Generator` interface lives in `narrative`, not
      `service/interfaces.go`. Interfaces belong where they are consumed, and `service` does not
      consume this one — the handler orchestrates the two. The plan's placement was a reflex from
      Step 20's convention and would have made `service` import `narrative` for no reason.
      **Mutation testing found a real coverage gap.** Replacing `Render`'s error with a `continue`
      left every test green while partial output escaped: `Validate` and `Render` check different
      things, so one can pass where the other fails — `Validate` does not inspect a `Value`'s kind
      and `Render` refuses a kindless one. Now covered; the mutant dies.
      Other mutations caught (4): retry bound raised to three; a generator error retried instead
      of returned; the empty-vocabulary short circuit removed; the correction not appended.
- [x] T5 `GET /insights/portfolio/narrative`, its route, and the binary wiring. Inherited report
      errors asserted rather than assumed (404/401/502 all propagate, and none becomes a 200).
      Four distinct `reason` values, because "we could not phrase it", "there was nothing to
      phrase" and "phrasing is switched off" are three different facts, and collapsing them would
      make the first outage indistinguishable from a missing environment variable.
      `narrative` marshals as **null**, not `{}`, asserted against the raw JSON — a typed decode
      cannot tell those apart either. `ANTHROPIC_API_KEY` optional, logged loudly at boot.
- [x] Checkpoint B — vet/test green, `-race` clean, `GOWORK=off` builds pass for all seven.
      **145 tests** across `internal/narrative` and `internal/handler`. Billable calls: **zero**.
      Baseline restored and verified by query; services killed; 808x clear.
      **The plan asked for "rendered prose through the gateway with the fake generator", and that
      is split across two mechanisms rather than done in one.** Rendered prose over a real HTTP
      path is proven by the handler tests, which run the real chi router and the real JWT
      middleware through `httptest`. The gateway proxy hop and the binary's wiring are proven live
      — `/insights/portfolio/narrative` returns 200 in 15ms through `:8080`, with `narrative:
      null` and the not-configured reason, and 401 without a token. Injecting a fake generator
      into the production binary to join the two would mean shipping test-only code in `main.go`
      for one checkpoint. Live rendered prose arrives at Checkpoint D, where a real generator
      exists.

## Phase 3 — Cost control, still with no model behind it (D4)
- [x] T6 Report hash + narrative cache. `ReportHash` zeroes `computed_at` rather than listing
      fields to include, so a measurement added later participates by default.
      **The Checkpoint 0 finding chased to its real cause:** wrapping the Redis calls in
      `context.WithTimeout` did **nothing**. go-redis v9's `ContextTimeoutEnabled` defaults to
      `false`, and while it is false the client ignores context deadlines entirely and waits its
      own `ReadTimeout` — code that reads as bounded, compiles, and waits the full default anyway.
      Both caches are bounded now via a `cache.NewClient` constructor, proven against a listener
      that accepts and never answers. Mutations caught (4); one equivalent survivor recorded (a
      cache read error taking the miss path changes only a log line, by design); one near-miss
      where a replacement string dropped a `§` and never applied.
- [x] T7 Daily generation cap — INCR-then-compare so the slot is *reserved*, not checked; fails
      **closed**, deliberately unlike the cache beside it, and a nil counter counts as broken
      because unset Redis produces the same uncached-and-uncapped state an outage does.
      **Mutation testing found a gap that needed a new tool.** "Cap off by one" and "GET instead
      of INCR" both survived, because the handler's boundary tests drive the *mock* counter, which
      reimplements the cap comparison — the real implementation was exercised by nothing. A double
      that reimplements the logic it stands in for cannot test it. miniredis (test-only) now
      drives the real counter: both boundary sides, reservation, TTL, next-day reset, per-user
      isolation. Mutations caught (7).
- [x] Checkpoint C — vet/test green, race clean, `GOWORK=off` passes for all seven. **346 tests**
      at that point. Billable calls: **zero**.

## Phase 4 — The real model
- [x] T8 Prompt construction — frozen system prompt (asserted byte-identical across reports),
      per-request user message with sections, each degraded section's own reason, and the
      vocabulary with **raw** values. Asserted directly that the prompt contains `-12.4` and does
      **not** contain `-12.4%`: showing the rendered figure would hand the model the exact string
      it is forbidden to produce. Tokens listed sorted, so the prompt is reproducible rather than
      reshuffled by map iteration.
- [x] T9 The Anthropic client — code, wiring and unit tests complete. **No billable call has been
      made** (see T12).
      **The `effort` binding SPEC §2.9 flagged as unconfirmed exists**: `anthropic-sdk-go` v1.66.0
      has `OutputConfigParam.Effort` / `OutputConfigEffortLow`. Nothing deferred to
      `deferred-tuning.md`, no raw HTTP needed.
      A refusal is checked **before** the content is read — it arrives as a 200 with a stop reason,
      so reading content first would turn it into an empty draft and burn the retry on something a
      retry cannot fix. `classify` checks the context before the status code, so our own expired
      budget is not logged as Anthropic being down.
      Every test drives the SDK against `httptest` via `option.WithBaseURL`; the request body is
      asserted on the wire, including that `budget_tokens` is **absent** (Opus 5 rejects it).
      **One test deadlocked:** a handler blocking on the request's own context hangs `httptest`'s
      `Close`, which waits for outstanding requests. It needs a release channel closed by a
      function `defer`, which runs before any `t.Cleanup`.
      `GOWORK=off` still builds all seven modules with the new dependency.
- [ ] Checkpoint D — **BLOCKED, no API key.** Everything not requiring one is done: the binary
      boots with the client wired, and the suite is green. What is outstanding is the live half —
      a real narrative generated, read, and checked figure by figure, plus the raw pre-render
      draft read once by hand, plus the first-draft rejection rate that decides §6.1.

## Phase 5 — Degradation, evidence, and documentation
- [x] T10 Degradation and error mapping — **nine** distinct reasons, not seven, each asserted
      reachable, pairwise distinct, returning a null narrative, and caching nothing.
      **That table found a real defect.** The cap was reserved *before* `Generate` discovered the
      vocabulary was empty, so a fully degraded report cost an allowance while costing no API
      call: an account with no trades that polled the endpoint would burn its whole daily quota
      without a single call being made, then be refused on the day it finally had something to
      describe — a cost control turning into a denial of service against the users it costs
      nothing to serve. Found by asserting on the counter's call count; the response was correct
      throughout.
- [x] T11 Adversarial pass — **20 mutations**, 19 caught, 1 real survivor now closed, 1 first
      reported as not-applied and re-pointed.
      **The survivor mattered:** replacing `BuildPrompt`'s passed-in vocabulary with a fresh
      `Placeholders(r)` call changed nothing, because every test handed it exactly what
      `Placeholders` would produce. Those tests proved the two agree *when built the same way* —
      the drift they were written to preclude, not evidence against it. SPEC §2.3's single-source
      claim was resting on a test that could not fail. Now pinned with a vocabulary
      `Placeholders` would never return.
      **The not-applied one is the trap the plan names:** "word boundaries dropped" reported as
      SURVIVED because the replacement string's escaping never matched. Re-applied correctly it
      kills two tests.
- [ ] T12 Manual pass — every figure checked by eye; the no-advice read; database restored and
      **verified by query**; spend recorded
- [ ] T13 Documentation — checklist entry, `NEXT_SESSION.md`, `agents.md`, archive; record the
      percent convention for Step 22
- [ ] Checkpoint E — pre-merge; adversarial review of the branch

## Carry-over items deferred out of this step, with a named home
Recorded so none of them rests on someone remembering. Full reasoning in `tasks/plan.md`.
- `market-data` store tests (`historical_price_store.go`) — its own small step, next after 21
- Integration harness in three copies — `TESTING_STRUCTURE.md` §6a trigger stays **unfired**;
  Step 21 adds no `integration/` package, so no fourth copy appears. Re-confirmed in T13
- Security backlog item 8 (Unicode-normalise passwords) — its own small step, and soon
- Security backlog item 3 (Argon2id) — its own step; carries a migration strategy
- `docs/deferred-tuning.md` — unblocked by deployment; T9 adds one entry, does not work the file
- Dockerization, then cloud deployment — next roadmap items; T9's `GOWORK=off` criterion exists
  so this step does not make them harder
