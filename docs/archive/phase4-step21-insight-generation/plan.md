# Implementation Plan — Insight Generation: the LLM Narrative Layer (Step 21)

## Context

`agents.md`'s Phase 4 splits AI Insights in two: **Phase 1 — rule-based analytics** (Step 20,
merged) and **Phase 2 — LLM-generated insights**. This step is the second half.

One endpoint, `GET /insights/portfolio/narrative`, returning three short paragraphs that explain
the Step 20 report in plain language — one per section, keyed to risk, benchmarking and behavior.

The framing that shapes every task below: **every figure the user reads is rendered by Go from
the report struct, and none is produced by the model.** Step 20 computed the numbers first
precisely so this step would not have to trust a model with arithmetic. The model writes prose
containing named placeholders; Go substitutes; any surviving digit rejects the draft. That is a
structural property, not a prompt-level intention, and Phase 1 of this plan is where it lands.

`SPEC.md` is **drafted, awaiting approval**. This plan turns it into **15 tasks across 6 phases**
on branch `step21-insight-generation`, plus **2 carry-over tasks in Phase 0**. One commit per
task.

Baseline to re-check at every checkpoint:
```bash
docker exec quantsim-postgres psql -U quantsim -d postgres -tAc \
  "SELECT 'users=' || (SELECT count(*) FROM users) || ' accounts=' || (SELECT count(*) FROM accounts) \
       || ' trades='  || (SELECT count(*) FROM trades)"
# users=20 accounts=20 trades=0   (T12's manual pass moves trades; it restores them)
```

**A second baseline this step introduces, which no prior step had:** spend. Every checkpoint from
C onward records how many billable calls have been made. Phases 1–3 must record **zero**.

---

## Decisions carried in from SPEC.md §2 (not reopened here)

- A **separate endpoint**, `GET /insights/portfolio/narrative`; the existing report endpoint is
  untouched — §2.1
- The model returns **placeholders**; Go substitutes from the struct; a surviving digit is a
  rejection — §2.2
- The placeholder vocabulary is **generated from the report by one function used twice** — once
  to build the prompt, once to substitute — §2.3
- **Formatting lives in Go**, in the renderer, in one place — §2.4
- **Three validation checks** — no digit, no number word, no unknown token — plus caps;
  **one retry, then refuse**; never repair — §2.5
- A **frozen system prompt**, per-request user message; no encouragement, no softening — §2.6
- A **per-user daily generation cap**, failing **closed** when Redis is unavailable — §2.7
- The narrative **explains and never advises**; enforced by prompt and spot-check, and the
  spec says so rather than claiming a guarantee it does not have — §2.8
- **`claude-opus-5`**, adaptive thinking, effort `low`, `MaxTokens` 2000, non-streaming — §2.9
- Cache `narrative:{user_id}:{report_hash}`, **24h TTL**, hash **excludes `computed_at`**;
  errors never cached; fail-open both ways — §2.10
- **A failed report is an error; failed phrasing is not.** Report errors (404/401/502)
  propagate; generation failures are a 200 with `narrative: null` and a `reason` — §2.11
- Degraded sections are **omitted**; all three degraded means **no API call at all** — §2.12

## Six decisions this plan adds

**D1 — `Value` carries a kind and a raw value, never a pre-formatted string.**
This is a strengthening of §2.2 that only becomes visible once you write the type. If
`Placeholders` returned `"12.4%"`, the prompt would show the model the exact string it must not
produce, and every draft would be one copy-paste away from a violation that the digit check then
has to catch. Supplying the raw `float64` and formatting at render time keeps the rendered form
**out of the model's context entirely**. The model can still see `12.4` and could still type it —
check 1 catches that — but it is never shown the finished sentence-ready figure.

**D2 — the hash function lives beside the `PortfolioInsights` struct and zeroes `computed_at`
rather than listing fields to include.**
`json.Marshal` over a Go struct is already canonical — declaration order for fields, sorted keys
for maps — so the hash is `sha256(json.Marshal(reportWithComputedAtZeroed))`. Zeroing the one
excluded field rather than enumerating the included ones puts the failure in the safe direction:
a measurement added to the report in some later step participates in the hash **by default**, and
the narrative correctly invalidates. An include-list would silently keep serving stale prose for
a figure nobody remembered to add.

**D3 — the generator interface returns a raw draft string, not a narrative.**
`NarrativeGenerator.Draft(ctx, prompt) (string, error)`. Validation, substitution and rendering
live in `narrative/`, outside `llm/`. The SDK wrapper therefore has no knowledge of the guarantee
and cannot be the place it is weakened — a future change to the client cannot accidentally return
something already-rendered. The seam is where it is on purpose.

**D4 — cost control lands before the first billable call, not after.**
Phases are ordered so the Redis cache (T6) and the daily cap (T7) are written and tested against
the **fake** generator, and T9 — the first real API call in the project's history — happens with
both already proven. The alternative ordering makes every subsequent dev test run cost money and
puts the cap's first exercise in production.

**D5 — no test makes a network call, and the SDK wrapper is tested through `option.WithBaseURL`.**
The generator sits behind an interface with a mock, as every external dependency in this service
does. The one thing worth testing in `llm/client.go` is its **error → `reason` mapping** (429,
5xx, timeout, refusal), and that is done against an `httptest` server via the SDK's supported
base-URL option. No test carries an API key; no test reaches api.anthropic.com. The one live
check is T12's manual pass.

**D6 — the two unanswered §6 questions ship at their recommended values, in one constant block.**
§6.1's banned-word list rejects `one` upward plus fractions and magnitudes; §6.4's daily cap is
50. Both are one file, `narrative/limits.go`, so changing either after the manual pass is a
one-line edit. Flagged in T11 and T12 as the two things most likely to want adjusting once real
output exists.

**D7 — the two blocking carry-over items land first, in their own phase, before any Step 21 code.**
One of them is not optional: SPEC §2.7's fail-closed cap reasoning rests on "the user-visible
response is the same degraded 200 either way", and with Redis down the report endpoint returns a
**502** today. Writing Step 21 on top of that would build a careful degradation story on a premise
that is false. The other is the `gofmt` drift, which is one commit and belongs before any new work
in a module `make vet` touches. Everything else carried in `docs/NEXT_SESSION.md` is deferred
explicitly below, with a named home — nothing is left merely unmentioned.

---

## Carry-over items, and where each one goes

Every open item recorded in `docs/NEXT_SESSION.md` at the close of Step 20, accounted for. Two are
tasks in this plan; the rest are deferred **with a reason and a destination**, so none of them is
hanging on an unwritten assumption that someone will remember.

| Carry-over item | Disposition |
|---|---|
| **Redis outage → `/insights/portfolio` 502** (NEXT_SESSION item 2) | **C2, this step.** Blocking: SPEC §2.7 is premised on it being fixed. The recorded diagnosis is also wrong — see C2. |
| **`gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`** (item 3) | **C1, this step.** One commit, and it should precede any `fmt` check landing in CI. |
| **`market-data`'s store has no tests** (`historical_price_store.go`, item 3) | **Deferred — its own small step.** It is real coverage debt in a service this step does not touch, and folding it into a narrative-layer branch would make the diff unreviewable for both. Named as the next standalone item after Step 21 in T13's `NEXT_SESSION.md` rewrite. |
| **Integration harness exists in three copies** (`docs/TESTING_STRUCTURE.md` §6a trigger, item 3) | **Deferred — trigger still unfired, correctly.** Step 21 adds no `integration/` package (`ai-insights` still owns no database), so no fourth copy appears and the documented extraction trigger is not reached. Re-confirmed in T13 rather than assumed. |
| **Security backlog item 8 — Unicode-normalise passwords** (item 4) | **Deferred — its own small step, and it should be soon.** It gets more expensive as real accounts accumulate, which is an argument for scheduling it, not for doing it inside an LLM branch. |
| **Security backlog item 3 — Argon2id** (item 4) | **Deferred — its own step**, as `docs/security-backlog.md` already says: it carries a migration strategy. |
| **`docs/deferred-tuning.md`** — timeouts, pooling (item 1) | **Deferred — unblocked by deployment, not by this step.** T9 adds one entry to it (the `effort` binding, if unavailable); it does not work through the file. |
| **Dockerization, then cloud deployment** (item 1) | **Deferred — the next roadmap items.** T9's `GOWORK=off` criterion exists specifically so this step does not make them harder. |

---

## Dependency graph

```
C1 gofmt ─┐
          ├─→ (Step 21 proper)
C2 bounded pricing in trading-engine ─┘   ← unblocks SPEC §2.7 and T12's Redis criterion

T1 placeholders (pure) ─┬─→ T2 render (pure) ──┐
                        │                      ├─→ T4 generate: draft→validate→retry→render
                        └─→ T3 validate (pure) ┘        │
                                                        ├─→ T5 handler + route + wiring
                                                        │        │
                                    T6 cache (hash+TTL) ←────────┤
                                    T7 daily cap        ←────────┘
                                          │
                        T8 prompt ────────┴──→ T9 SDK client   ← FIRST BILLABLE CALL
                                                    │
                                    T10 degradation ─┴─→ T11 adversarial ─→ T12 manual ─→ T13 docs
```

T1–T7 are reachable, testable and complete with a fake generator. Nothing before T9 costs a cent.

---

## Phase 0 — Carry-over items, landed before any Step 21 code (D7)

Both tasks are additive to existing, well-tested services that this step otherwise does not touch.
Neither depends on the other. Step 20's plan set the precedent for a phase like this and for
gating it behind its own review.

### C1 — `gofmt` drift in `services/auth`
**Description:** Run `gofmt -w` on `services/auth/internal/service/{interfaces.go,types.go}`,
untouched since Step 11. Nothing else — no rename, no reorder, no "while I'm here".

**Acceptance criteria:**
- `gofmt -l ./...` is empty across all seven modules, not just the two files.
- The diff is whitespace and alignment only. `git diff --ignore-all-space` on those two files is
  **empty** — that is the task's proof, and it is the thing that distinguishes a formatting commit
  from a formatting commit that quietly changed something.
- One commit, `style:`-prefixed, touching nothing else.

**Verification:** `gofmt -l`, the `--ignore-all-space` diff above, then `make vet && make test`.

**Dependencies:** None · **Files:** `services/auth/internal/service/{interfaces.go,types.go}` ·
**Scope:** S

### C2 — Bound `trading-engine`'s portfolio pricing (NEXT_SESSION item 2)
**Description:** `GET /trading/portfolio` takes **8.7s against 5.8ms healthy** when Redis is down,
tripping `ai-insights`' 5s upstream timeout and turning a Redis outage into a 502 on an endpoint
whose every figure comes from Postgres.

**The recorded diagnosis is wrong, and the correction is the task.** `docs/NEXT_SESSION.md` says
the fix "means changing `trading-engine`'s retry behaviour". There is no retry. `Service.price`
(`internal/service/trading.go:233`) is a **sequential loop** issuing one `LatestPrice` HTTP call
per holding, each bounded by `requestTimeout = 3 * time.Second` in `internal/client`. Per-symbol
timeouts **compose additively**: the endpoint's worst case is N × 3s with no bound on N, and three
degraded holdings at ~2.9s each is precisely the observed 8.7s. The bug is not the per-call
timeout, which is correct; it is that nothing bounds the loop.

**Fix: price concurrently under one overall deadline.** Concurrency alone takes the worst case
from N × 3s to ~3s; the overall deadline is what makes it a *guarantee* rather than a smaller
number that still grows with N. Holdings not priced in time come back unpriced, which
`Portfolio` already handles correctly and deliberately — "an unpriceable position is valued at
cost — never dropped from the total, never counted as zero" (`trading.go:201`). The degraded
answer this task produces is one the service was already written to give.

**Acceptance criteria:**
- Independence is preserved: one symbol that cannot be priced must not blank the others. This is
  the property `price`'s existing comment is about, and concurrency is exactly where it would be
  lost — a shared error variable would reintroduce it silently.
- The whole loop is bounded: a portfolio of N unpriceable holdings returns in roughly one timeout,
  not N. Asserted with a fake price client that blocks, across N = 1, 3 and 10.
- `Portfolio`'s totals are unchanged for a fully priceable portfolio — same numbers, same order.
- Position order in the response is **deterministic** and matches holdings order. Concurrent
  writes into a pre-sized slice by index, never appends from goroutines.
- `go test -race -count=1 ./...` clean on `trading-engine`.

**Verification:** unit tests against the existing mock price client, extended to block on demand.
Then, through the running stack: stop Redis, time `GET /trading/portfolio`, and confirm it returns
in roughly one timeout rather than N — and that `GET /insights/portfolio` consequently returns
**200**, which is the criterion Step 21 actually depends on. Mutation: remove the overall deadline
and confirm the N = 10 test fails; share one error across goroutines and confirm the independence
test fails.

**A note on scope discipline:** this is a read path in a service that moves money, so the change is
confined to `price`. No signature outside it changes, `positionsFor` and `Portfolio` keep their
shape, and nothing in the order or trade path is touched.

**Dependencies:** None · **Files:** `services/trading-engine/internal/service/trading.go`,
`internal/service/mock/mock.go`, `_test.go` · **Scope:** M

### Checkpoint 0
- [ ] `make vet`, `make test`, `make test-integration` green across all seven modules
- [ ] `gofmt -l ./...` empty; C1's `--ignore-all-space` diff empty
- [ ] `go test -race -count=1 ./...` clean on `trading-engine`
- [ ] **With Redis stopped:** `GET /trading/portfolio` returns in roughly one timeout, and
      `GET /insights/portfolio` returns **200** — the premise SPEC §2.7 and T12 both rest on
- [ ] Redis restarted, baseline `users=20 accounts=20 trades=0` verified by query
- [ ] Review before proceeding — this is the only work touching services outside the step

---

## Phase 1 — The guarantee, as pure functions

No HTTP, no SDK, no Redis, no network. Three pure packages-worth of code, and the highest-value
work in the step: if Phase 1 is right, the rest is plumbing, and if it is wrong, nothing later
can recover the property.

### T1 — The placeholder vocabulary (SPEC §2.3, D1)
**Description:** `narrative/placeholders.go` — `Placeholders(PortfolioInsights) map[string]Value`
and the `Value` type carrying a **kind** (`Percent`, `Ratio`, `Index`, `Money`, `Count`, `Date`,
`Symbol`) and a raw value, per D1. Static tokens for the window and the three sections; dynamic
tokens derived from the user's actual symbols (`risk.positions.{SYMBOL}.weight_pct`,
`benchmarking.{SYMBOL}.return_pct`) and finding codes
(`behavior.findings.{CODE}.turnover_ratio`).

**Acceptance criteria:**
- Every key in the returned map is non-empty and every `Value` has a kind set — no zero-valued
  `Value` escapes, since a kindless value would render through a default branch.
- The symbol-derived keys reflect the report's actual holdings and benchmarks, not a constant
  list; a report with no positions yields no `risk.positions.*` keys at all.
- A section in `insufficient_data` contributes **no** tokens for its figures — a token whose
  underlying figure was never computed must not be offerable.
- `risk.largest_position_symbol` is present whenever there is at least one position, so prose
  can name a holding without counting one.
- Token names match their JSON paths exactly, so a draft can be checked against the report by eye.

**Verification:** table-driven over fixtures — a full report, an all-cash report, a single-position
report, a report with each section degraded in turn, a report with all three degraded. Property
test: every key returned renders to a non-empty string under T2's renderer (this test is written
in T2 and is the pair's real proof). Mutation: make one section's builder return its tokens
unconditionally and confirm the degraded-section test fails.

**Dependencies:** None · **Files:** `services/ai-insights/internal/narrative/{placeholders,types}.go`
+ `_test.go` · **Scope:** M

### T2 — The renderer and its formatting (SPEC §2.4, D1)
**Description:** `narrative/render.go` — substitute `{token}` occurrences in a draft from the
vocabulary map, formatting each by its kind. Percentages to one decimal with an explicit sign
where the sign carries meaning; Sharpe to two decimals; HHI to three; money two-decimal and
thousands-separated; counts as bare integers; dates through one fixed layout; symbols verbatim.

**Acceptance criteria:**
- An unknown token is an **error**, not a passthrough — literal `{risk.nonsense}` must never
  reach the page. (T3 rejects it earlier; this is defence in depth at the seam that renders.)
- Repeated tokens in one draft all substitute; adjacent tokens substitute independently.
- Money matches `frontend/src/format.ts`'s `formatPrice`: two decimals, thousands-separated,
  no currency symbol.
- Dates match its `formatDate`: `en-US` calendar-date rendering read off the timestamp's own
  UTC calendar fields — Go layout `1/2/2006`, and **not** the service's local zone.
- Output contains no braces once substitution succeeds.

**Verification:** golden tests per kind with **hand-written** expected strings, not values captured
from a first run (Step 18 §4). A parity table for money and dates listing the Go output beside the
`format.ts` output for the same input.

**A constraint this task discovers and hands to Step 22:** `format.ts` has `formatPrice`,
`formatQuantity` and `formatDate` — and **no percent formatter at all**. Percentages have no
frontend precedent, so this step sets it, and Step 22 must follow *this* convention rather than
inventing a second one. Record the chosen percent format in `PHASE4_CHECKLIST.md` (T13) as a
requirement on Step 22, not as an internal detail — otherwise the same figure appears two ways on
one screen, which is exactly the failure §2.4 exists to prevent.

**Dependencies:** T1 · **Files:** `narrative/render.go` + `_test.go` · **Scope:** M

### T3 — The validator (SPEC §2.5, D6)
**Description:** `narrative/validate.go` and `narrative/limits.go` — the three checks and the caps.
(1) no `[0-9]` anywhere in the raw draft; (2) no banned number word, fraction word, magnitude word
or bare unit word; (3) every `{token}` referenced exists in the vocabulary. Plus a per-section
character cap and a total cap. Returns a typed failure naming the check and quoting the offending
fragment, because T4's retry feeds that fragment back to the model.

**Acceptance criteria:**
- A digit rejects from any position: first character, last character, mid-word, inside a decimal,
  and **inside a token name** — `{risk.positions.AAPL2.weight_pct}` is a rejection, not a lookup.
- Each banned word rejects, case-insensitively, on word boundaries — `quarter` rejects,
  `headquarters` does not.
- An unknown token rejects and names the token.
- An unclosed brace, a nested brace, an empty draft, a whitespace-only draft and an over-cap draft
  each reject with their own reason.
- The failure value carries enough to build a retry message without re-scanning the draft.

**Verification:** exhaustive table-driven, one case per bullet above and one per banned word.

**The adversarial case that states the property, and the single most important test in the step:**
a draft containing a number that is *correct* — the true drawdown, formatted exactly as T2's
renderer would format it — **must still be rejected.** Correctness is not the criterion;
provenance is. A validator that passes this case has quietly become a whitelist and the step's
entire guarantee is gone. It gets its own test with that reasoning in a comment.

**Dependencies:** T1 · **Files:** `narrative/{validate,limits}.go` + `_test.go` · **Scope:** M

### Checkpoint A
- [ ] `make vet`, `make test` green across all seven modules
- [ ] Billable calls so far: **zero**
- [ ] The correct-number-still-rejected test exists and passes
- [ ] Mutations: delete the digit check → a test fails; remove one banned word → a test fails;
      make an unknown token render as literal braces → a test fails
- [ ] Review before proceeding — everything after this depends on Phase 1 being right

---

## Phase 2 — A narrative end to end, with no model behind it

The vertical slice. By the end of Phase 2 the endpoint is reachable through the gateway and
returns rendered prose — with a fake generator supplying the draft. The whole guarantee is
exercised against a real HTTP path before a single API call exists.

### T4 — `generate.go`: draft → validate → retry → render (SPEC §2.5, D3)
**Description:** The orchestration, and the `NarrativeGenerator` interface in
`service/interfaces.go` with a mock in `service/mock/` — `Draft(ctx, prompt) (string, error)`,
returning a raw string per D3. On a validation failure, one retry with the offending fragment
quoted back; on a second failure, return a typed "generation unusable" error. Never repair.

**Acceptance criteria:**
- A clean first draft renders and makes exactly **one** generator call.
- A dirty first draft and clean second makes exactly **two**, and the second prompt contains the
  offending fragment.
- Two dirty drafts make exactly two calls — never three — and return the typed failure.
- A generator error (not a validation failure) is **not** retried; the retry exists for rejected
  drafts, not slow or broken ones (§2.11).
- Nothing partially-rendered escapes on any failure path.

**Verification:** mock-driven, asserting on **call counts**, not just on the response — a retry
loop that silently runs three times still returns the right answer and is still a bug.
Mutation: change the retry bound to two and confirm a test fails.

**Dependencies:** T1, T2, T3 · **Files:** `narrative/generate.go`, `service/interfaces.go`,
`service/mock/mock.go` · **Scope:** M

### T5 — The endpoint, the route, and the wiring (SPEC §2.1, §2.11)
**Description:** `handler/narrative.go`, the route in `handler/router.go`, and construction in
`cmd/server/main.go`. The handler calls the **existing** `service.PortfolioInsights` for the
report — re-deriving nothing — then the generator. Response shape per §2.1, with `report_hash`
stubbed until T6.

**Acceptance criteria:**
- The route sits in the same authenticated group as `/insights/portfolio`; unauthenticated → 401.
- The report's errors propagate unchanged: `symbol_unavailable` → 404, `invalid_token` → 401,
  `upstream_unavailable` → 502 (§2.11). Verified by test, not by inspection — this is inherited
  behavior and inherited behavior is what stops being true silently.
- `narrative` is an object of up to three strings; absent sections are **absent keys**, never
  `null` and never `""`.
- No gateway change is needed — confirm `/insights/*` already routes the sub-path rather than
  assuming it. (It does; the wildcard covers it. Step 16's routing bug was the bare-prefix case,
  which does not apply here.)

**Verification:** handler tests against the mock generator and a mock insights path; then, through
the running stack, `curl` the endpoint via the gateway with a real JWT and confirm rendered prose
comes back with the fake generator in place.

**Dependencies:** T4 · **Files:** `handler/{narrative,router}.go`, `cmd/server/main.go`,
`.env.example` · **Scope:** M

### Checkpoint B
- [ ] `make vet`, `make test` green; `make test-integration` still 63/0
- [ ] Billable calls so far: **zero**
- [ ] `curl` through the gateway returns rendered prose end to end with the fake generator
- [ ] Every figure in that prose is traceable to a struct field by eye
- [ ] Baseline `users=20 accounts=20 trades=0` unchanged

---

## Phase 3 — Cost control, still with no model behind it (D4)

Both tasks are written and proven against the fake generator. This is deliberate: the first
billable call in the project's history happens in T9, with the cache and the cap already working.

### T6 — The report hash and the narrative cache (SPEC §2.10, D2)
**Description:** `cache/redis_narrative_cache.go` and the hash beside the report struct.
`sha256(json.Marshal(report with ComputedAt zeroed))`, per D2. Key
`narrative:{user_id}:{report_hash}`, 24h TTL, errors never cached, fail-open both ways.

**Acceptance criteria:**
- Two reports differing **only** in `computed_at` hash identically. This is the task's real proof:
  without it the cache never hits once and the defect is invisible except on the bill.
- Two reports differing in **any** measurement hash differently — asserted per section, including
  a change to one position's quantity and to one finding's occurrence count.
- A failed generation writes nothing.
- A Redis read failure computes and returns 200; a write failure returns the narrative anyway.
- **Every Redis call is bounded by its own context deadline.** Checkpoint 0 measured
  `/insights/portfolio` at **6.05s** against a *hung* Redis — on the zero-trade path, which never
  reaches `trading-engine`. The existing cache fails open correctly but takes an unbounded time to
  do it, because a paused server accepts the connection and never answers. Adding a second
  unbounded read here would stack another such wait on top. Bound it, and assert the bound.
- `report_hash` is returned in the response and matches the key actually used.

**Verification:** unit tests over fixtures for the hash; the cache against a Redis test double,
matching `cache/redis_insights_cache_test.go`'s existing pattern. Mutation: include `computed_at`
in the hash and confirm the identical-hash test fails.

**Dependencies:** T5 · **Files:** `cache/redis_narrative_cache.go`, `service/insights.go` (hash
helper) · **Scope:** M

### T7 — The daily generation cap (SPEC §2.7, D6)
**Description:** `narrative:count:{user_id}:{yyyy-mm-dd}` in Redis, incremented on a
**generation** and never on a cache hit, capped at 50 (D6). Over the cap → the fail-open shape
with `reason: "daily generation limit reached"`. **Redis unavailable → generation refused**, not
allowed.

**Acceptance criteria:**
- A cache hit does not increment. This is what keeps ordinary reading unaffected and is the
  easiest thing to get backwards.
- The counter key expires; a cap is per-day, not cumulative forever.
- At exactly the cap the next generation is refused, and the boundary is tested on both sides.
- **Redis unavailable refuses generation** — with the reasoning in a comment, since it is
  deliberately the opposite of the cache's fail-open two files away and will otherwise read as an
  inconsistency: the same outage removes the cache *and* the cap at once, so failing open here
  costs money with no ceiling while failing open there costs only latency.
- The user-visible response is the same degraded 200 either way.

**Verification:** unit tests including both sides of the boundary and the Redis-down path.
Mutation: make the counter fail open and confirm a test fails; move the increment to the cache-hit
path and confirm a test fails.

**Dependencies:** T6 · **Files:** `cache/redis_narrative_cache.go`, `narrative/limits.go` ·
**Scope:** S

### Checkpoint C
- [ ] `make vet`, `make test` green
- [ ] Billable calls so far: **zero** — the last checkpoint at which this is true
- [ ] Cache hit/miss and cap behavior demonstrated through the running stack with the fake
      generator, by watching `redis-cli MONITOR` rather than by trusting the tests alone
- [ ] Review before proceeding — T9 spends money

---

## Phase 4 — The real model

### T8 — Prompt construction (SPEC §2.6, §2.8, §2.12)
**Description:** `llm/prompt.go` — a frozen system prompt (role, output contract, the no-figures
rule, the no-advice rule, tone) and a per-request user message carrying the report, the vocabulary
with current values, and which sections are unavailable and why. The vocabulary rendered into the
prompt comes from **the same `Placeholders` map** T4 substitutes from (§2.3) — passed in, never
rebuilt.

**Acceptance criteria:**
- The system prompt is byte-identical across users and across requests — asserted by test, since
  a stray timestamp or user ID in it is the classic silent cache-invalidator and would also make
  the prompt non-reproducible.
- The token list in the user message is exactly the key set of the map passed in — same map,
  proven, not two lists that agree today (§2.3's whole point).
- Degraded sections are named as unavailable; a fully-degraded report produces **no prompt at
  all** and the caller makes no API call (§2.12).
- The prompt shows raw values, never rendered strings (D1).

**Verification:** golden test on the system prompt; a property test asserting prompt tokens ==
map keys; a test asserting zero generator calls for a fully-degraded report, on the mock's count.

**Dependencies:** T7 · **Files:** `llm/prompt.go` + `_test.go` · **Scope:** M

### T9 — The Anthropic SDK client (SPEC §2.9, §2.11, D5) — **first billable call**
**Description:** `llm/client.go` — `github.com/anthropics/anthropic-sdk-go`, `claude-opus-5`,
adaptive thinking, effort `low`, `MaxTokens` 2000, non-streaming, 20s timeout. Constructed once at
boot and reused. Model ID overridable by `ANTHROPIC_MODEL`; echoed in the response. Implements
`NarrativeGenerator`, returning a raw draft string (D3).

**Acceptance criteria:**
- `ANTHROPIC_API_KEY` unset is a **supported configuration**, logged loudly at boot, matching
  `REDIS_URL`'s precedent in this same service's `main.go`. Not a `log.Fatal`.
- Errors map to distinct `reason` values: rate limit, timeout, refusal, upstream error,
  not-configured. Tested against an `httptest` server via `option.WithBaseURL` (D5).
- No retry on a slow or failed call (§2.11) — the only retry in this step is T4's.
- `cd services/ai-insights && GOWORK=off go build ./...` **passes** after the dependency is added.
  All seven modules pass today and Dockerization is next; a standard Go Dockerfile copies one
  module's `go.mod`/`go.sum` and runs `go mod download`, which is exactly this case. Regressing it
  here surfaces as a confusing Docker failure in a later step rather than a test failure in this
  one.

**The one binding that is unconfirmed** (SPEC §2.9): `output_config.effort` has no documented Go
binding in the reference material. Confirm the field against the installed SDK and let the
compiler settle the name. **If the installed version has no binding for it, ship at the default
effort and record that in the checklist** — do not hand-roll an HTTP call to reach one parameter.
The SDK is this project's interface to the API; working around it for a cost knob is the worse
trade, and `docs/deferred-tuning.md` is where a deferred cost knob belongs.

**Verification:** unit tests for the error mapping via `option.WithBaseURL`; then **one real call**
against the dev portfolio. Read the draft *before* rendering — that raw string is the only direct
evidence of whether the model honours the placeholder contract, and it is worth reading by hand
once even though the validator's verdict is what actually matters.

**Dependencies:** T8 · **Files:** `llm/client.go` + `_test.go`, `cmd/server/main.go`,
`services/ai-insights/go.mod`, `.env.example` · **Scope:** M

### Checkpoint D
- [ ] `make vet`, `make test` green; `GOWORK=off go build ./...` passes for all seven modules
- [ ] Billable calls so far: a handful, and each one recorded in `todo.md`
- [ ] A real narrative has been generated, read, and checked figure by figure against the JSON
- [ ] The raw pre-render draft has been read once by hand
- [ ] First-draft rejection rate observed and recorded — it decides whether §6.1's word list is
      drawn in the right place

---

## Phase 5 — Degradation, evidence, and documentation

### T10 — Degradation and error mapping (SPEC §2.11, §2.12)
**Description:** Consolidate every failure path into the documented shape and prove each one.
Missing key, timeout, rate limit, refusal, over-cap, two failed validations, fully-degraded report
→ 200 with `narrative: null`, `state: "unavailable"` and a distinct `reason`. Report-level
failures propagate unchanged.

**Acceptance criteria:**
- Seven distinct `reason` values, each reachable and each asserted.
- No failure path caches anything.
- The fully-degraded path asserts **zero** generator calls on the mock's count, not merely a
  correct response — a wasted paid call that returns the right JSON is invisible otherwise.
- A report-level 404/401/502 is never converted into a 200.

**Verification:** table-driven across all seven, plus the three propagating errors.
Mutation: convert a report error into the fail-open shape and confirm a test fails.

**Dependencies:** T9 · **Files:** `handler/narrative.go`, `narrative/generate.go` · **Scope:** M

### T11 — Adversarial pass
**Description:** The project's standing pre-merge practice. Mutate and confirm a test fails, for
each: the digit check returning `nil`; one word removed from the banned list; `Placeholders`
omitting a key the prompt advertises; the cap comparison inverted; the counter failing open;
`computed_at` included in the hash; the retry bound raised to two; an unknown token rendering as
literal braces; a report error converted to a 200.

**Acceptance criteria:**
- Every mutation above breaks at least one test, and the failing test is the one you would expect.
- **A mutant that does not compile, or no longer applies, is not a caught mutant** — re-point stale
  mutants when code has moved, and rewrite non-compiling ones so they actually run. Both report as
  something other than SURVIVED and both look like passing coverage.
- Survivors are recorded in `todo.md` with what they revealed, not silently fixed.

**Verification:** the run itself, recorded task by task.

**Dependencies:** T10 · **Scope:** M

### T12 — Manual pass against the live stack
**Description:** Six services up, a real key, the dev portfolio.

**Acceptance criteria:**
- **Every figure in the prose checked against the JSON report, field by field, by eye.** This is
  the acceptance criterion for the whole step, done the way a user would do it.
- A second read asking only *"is any sentence here advice?"* (§2.8) — the no-advice rule is the
  one this step cannot enforce mechanically, so the spot-check is its only evidence.
- Cache behavior confirmed: an unchanged portfolio returns the identical narrative; a trade
  changes the hash and produces a new one.
- The cap's boundary exercised once, deliberately.
- Redis stopped → generation refused, report endpoint still serving, response still a 200 (§2.7).
  **This is only true because of C2**; re-confirm it here rather than trusting Checkpoint 0, since
  everything in Phases 1–5 landed in between.
- Total spend for the pass recorded.
- **Dev database restored to `users=20 accounts=20 trades=0` and verified by query**, not assumed;
  children before parents when deleting (`backtests` has no `ON DELETE CASCADE` on `user_id`).
- All six services killed afterwards; `lsof -nP -iTCP -sTCP:LISTEN | grep 808` shows nothing.

**Two things to decide here, with real output in hand** (D6): whether §6.1's banned-word list is
drawn in the right place, judged by the observed rejection rate, and whether §6.3's no-advice rule
needs a deny-list after all, judged by whether any generated sentence read as advice.

**Dependencies:** T11 · **Scope:** M

### T13 — Documentation
**Description:** `PHASE4_CHECKLIST.md`'s Step 21 entry; `docs/NEXT_SESSION.md` rewritten;
`agents.md`'s Phase 4 list updated; `SPEC.md`, `tasks/plan.md` and `tasks/todo.md` archived to
`docs/archive/phase4-step21-insight-generation/`.

**Acceptance criteria:**
- The Step 21 entry records what each mutation caught and what each verification actually proved,
  not what was intended — the convention Step 20's entry set.
- **The percent format chosen in T2 is written down as a requirement on Step 22**, since
  `format.ts` has no percent formatter and the frontend will otherwise invent a second convention.
- Any deferred cost knob from T9 (effort binding) lands in `docs/deferred-tuning.md`.
- **`docs/NEXT_SESSION.md`'s carry-over list is rewritten against this plan's disposition table**,
  not appended to: C1 and C2 are closed and removed, and each deferred item is restated with the
  destination named here. The wrong diagnosis C2 corrected is recorded — "per-symbol timeouts
  compose additively in a sequential loop", not "retry behaviour" — because the next person to
  read that file would otherwise go looking for a retry that was never there.
- The `docs/TESTING_STRUCTURE.md` §6a extraction trigger is **re-confirmed unfired**, with the
  reason (no fourth `integration/` copy), rather than left unmentioned for a third step running.
- Root `SPEC.md` and `tasks/` do not survive onto `main` — they are archived, and the branch is
  squashed to one `feat(step21)` commit and merged `--no-ff`, matching Steps 16–20.

**Dependencies:** T12 · **Scope:** S

### Checkpoint E — pre-merge
- [ ] `make vet`, `make test` green across all seven modules; `make test-integration` 63/0
- [ ] `go test -race -count=1 ./...` clean on `ai-insights`
- [ ] `GOWORK=off go build ./...` passes for all seven modules
- [ ] Adversarial pass complete, survivors recorded
- [ ] Manual pass complete, database restored **and verified by query**, services killed
- [ ] Total spend recorded
- [ ] Adversarial review of the branch before merge — green tests are not evidence on their own
