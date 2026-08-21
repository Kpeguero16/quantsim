# SPEC — Insight Generation: the LLM Narrative Layer (Step 21)

Status: **Draft, awaiting review.** Four design questions were resolved before drafting (§2.1 separate endpoint, §2.2 placeholder substitution, §2.9 `claude-opus-5` at low effort, §2.10/§2.11 fail-open with a content-hash cache). §6 carries what is still genuinely open.
Scope: `services/ai-insights` only — one new endpoint, `GET /insights/portfolio/narrative`; two new internal packages; one new module dependency (`github.com/anthropics/anthropic-sdk-go`); `.env.example`. **No gateway change** (the existing `/insights/*` wildcard already covers the path), **no migration, no new table, no frontend, and no change to any figure Step 20 computes.**

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase4-step20-portfolio-analytics/`.

---

## 1. Objective

`agents.md` §4 splits AI Insights in two: **Phase 1 — rule-based analytics. Phase 2 — LLM-generated insights.** Step 20 shipped the first half and said what the second half would be allowed to do:

> When narrative generation arrives, it will be handed this object and permitted to *phrase* it, never to produce a figure of its own.

**Objective:** serve **one endpoint** — `GET /insights/portfolio/narrative` — returning short plain-language prose explaining the authenticated user's Step 20 report, in which **every figure the user reads was rendered by Go from the report struct and none was produced by the model.**

**The problem this step actually has to solve.** "The model must not state a number" is not a requirement a prompt can satisfy. A prompt rule is a probability, and its failure mode is the worst available: a hallucinated Sharpe ratio is indistinguishable, on the page, from a real one — same shape, same plausibility, no error, no log line. Step 20 was built first precisely so this step would not have to trust a model with arithmetic. Spending that advantage on a prompt-level instruction would waste it.

So the design constraint is stronger than "instruct the model not to": **the model's output must be incapable of carrying a figure at all.** §2.2 is where that gets solved, and it is the load-bearing decision of the step. Everything else here is plumbing around it.

**Non-goals:**
- **Any frontend.** Step 22, per the Step 16 → 17 and Step 20 → 21 precedent. This step ships an endpoint and nothing that renders it.
- **Streaming.** The output is three short paragraphs behind a 24-hour cache. Streaming is a UI decision and belongs with the UI, if at all.
- **Chat, follow-up questions, or multi-turn.** One request, one narrative. A conversational surface is a different product with a different threat model (the user's next message becomes untrusted input to a prompt) and is not smuggled in here.
- **Tool use.** The model is handed everything it may use. Giving it the ability to fetch is the opposite of this step's constraint.
- **Recommendations, forecasts, or anything a reader could act on as advice.** See §2.8.
- **Narrating backtests.** This phrases the live-portfolio report, as Step 20 does.
- **Persistence.** Redis cache only (§2.10), which is a cache and not a record.
- **Changing any Step 20 figure, threshold, or state machine.** If the prose is awkward because a figure is awkward, that is a finding for a later step, not a licence to edit the analytics to read better. Step 20 §3 named this direction of influence as the thing to avoid.

---

## 2. Design decisions

### 2.1 A separate endpoint, not a section on the existing one

`GET /insights/portfolio/narrative`, alongside the unchanged `GET /insights/portfolio`.

The two have nothing in common operationally. The report is deterministic, costs nothing, and answers in single-digit milliseconds against a warm cache. Generation is billed per call, takes seconds, depends on a third party, and can fail for reasons no other part of QuantSim can fail for. Fusing them would put a paid, slow, externally-dependent step in front of a response that currently renders correctly without it — and would make every existing consumer of `/insights/portfolio` pay for prose it did not ask for.

Separate endpoints also give the frontend the only sequence that reads well in Step 22: render the numbers immediately, fill the prose in when it arrives, and show the numbers alone when it does not.

The response:

```json
{
  "as_of_date": "2026-08-14",
  "report_hash": "a91f3c…",
  "state": "ok",
  "narrative": {
    "risk": "…", "benchmarking": "…", "behavior": "…"
  },
  "model": "claude-opus-5",
  "generated_at": "2026-08-20T18:03:11Z"
}
```

`narrative` is an object of three optional strings keyed to Step 20's three sections, not one continuous passage, so Step 22 can place each paragraph beside the numbers it is about. `report_hash` is the cache key's second half (§2.10) and is returned so a caller can tell two narratives apart — `generated_at` cannot, since a cache hit returns an old one.

### 2.2 The model returns placeholders, and Go substitutes the figures

**This is the step.** The model never writes a number because it is never asked to and its output is rejected if it does.

The model is handed the report — with its real values, because it must be able to judge that a 34% drawdown is severe and a 2% one is not — and is asked to write prose in which **every figure appears as a named placeholder token**:

```
model returns:
  "Your portfolio's deepest fall from a high was {risk.max_drawdown_pct},
   against {benchmarking.SPY.return_pct} for the S&P over the same window."

Go renders:
  "Your portfolio's deepest fall from a high was 12.4%,
   against 8.1% for the S&P over the same window."
```

The user-visible number is produced by `strconv` from a struct field. There is no path by which a model-generated token reaches the page as a figure — not a path that is discouraged, a path that does not exist. Any raw digit surviving in the model's output is a validation failure (§2.5), not a value to be copied.

**Why this rather than validating free prose.** The obvious alternative is to let the model write numbers and check each one against the report. It leaks in both directions. `12.4`, `12.40`, `12.4%`, `roughly 12%` and `about an eighth` are the same claim to a reader and five different strings to a validator, so a whitelist either rejects correct prose or admits approximations it cannot check. And it validates the wrong property: a whitelist asks *is this number right*, when the property that matters is *where did this number come from*. Substitution answers the second question by construction.

**What it costs.** The model must hold an unusual output contract, so some drafts will violate it — that is §2.5's retry, and it is a cost paid in latency and tokens rather than in correctness. Prose is slightly more constrained than free writing: a sentence wanting a figure the vocabulary has no token for cannot be written. That is the intended trade.

### 2.3 The placeholder vocabulary is generated from the report, never hand-listed

One function, `Placeholders(PortfolioInsights) map[string]Value`, produces the whole vocabulary, and its output is used **twice**: rendered into the prompt as the list of tokens the model may use with their current values, and used as the substitution table when the draft comes back.

One source for both halves is what makes the contract airtight. A token the prompt advertises always renders, and a token that renders was always advertised — neither can drift from the other, because there is no second list to drift from. A hand-maintained prompt listing tokens the renderer does not know is the exact bug this forecloses, and it would surface as a rejected draft rather than as a wrong number, which is survivable but wasteful.

Names mirror the JSON paths, so a reader of the raw draft can check a claim against the report by eye:

| Group | Tokens |
|---|---|
| Window | `as_of_date`, `window.start_date`, `window.trading_days` |
| Risk | `risk.position_count`, `risk.cash_weight_pct`, `risk.concentration_hhi`, `risk.largest_position_pct`, `risk.largest_position_symbol`, `risk.annualized_volatility_pct`, `risk.max_drawdown_pct` |
| Per position | `risk.positions.{SYMBOL}.weight_pct`, `.market_value`, `.quantity` |
| Benchmarking | `benchmarking.portfolio_return_pct`, `benchmarking.portfolio_sharpe`, `benchmarking.{SYMBOL}.return_pct`, `.excess_return_pct`, `.sharpe` |
| Behavior | `behavior.trade_count`, `behavior.finding_count`, `behavior.findings.{CODE}.turnover_ratio`, `.occurrences` |

The per-position and per-benchmark tokens are why generation must be derived: the symbol set is the user's, not a constant.

`risk.largest_position_symbol` and the symbol-keyed groups exist so the model can name a holding without counting one, and so prose like "your largest position" is anchored to a rendered symbol rather than to the model's recollection of the list.

### 2.4 Formatting lives in Go, in one place

Each `Value` carries its kind, and the renderer owns the formatting: percentages to one decimal with an explicit sign where the sign carries meaning, Sharpe to two decimals, HHI to three, counts as bare integers, dates through one fixed layout, symbols verbatim.

The consequence is worth stating plainly because it is easy to trip over in Step 22: **the prose arrives pre-formatted, and the frontend cannot restyle a figure inside it.** `frontend/src/format.ts` will format the JSON report and this service will format the same figure inside the sentence, so the two must agree by convention rather than by sharing code. Where they disagree the user sees the same number written two ways on one screen. Matching `format.ts`'s existing choices is therefore a requirement of this step, not a nicety — including its `{timeZone: 'UTC'}` handling for `as_of_date` and `window.start_date`, which are calendar dates rather than instants.

### 2.5 Validation: three checks, and refusal rather than repair

Every draft passes three mechanical checks before substitution:

1. **No Arabic digit survives.** A single `[0-9]` anywhere in the raw draft rejects it. Total, mechanical, and covers the realistic failure.
2. **No number word or bare unit word.** A curated list — spelled numerals, fraction words (`half`, `third`, `quarter`), magnitude words (`hundred`, `thousand`, `million`), and unit words used without a token (`percent`, `dollars`, `basis points`). This closes the "twelve percent" hole that check 1 alone leaves open.
3. **Every `{token}` referenced exists in the vocabulary.** An invented token is a hallucinated *reference*, which is the same failure one layer up, and it must never render as literal braces on the page.

Plus a per-section character cap and a total cap, so a runaway draft is a rejection rather than a wall of text.

**On failure: one retry, with the offending fragment quoted back.** Then, if the second draft also fails, the endpoint fails open (§2.11) — no narrative, a reason, nothing cached, and a log line naming the check and the section.

**Refusal, not repair** — deliberately the same rule as Step 20 §2.12's reconciliation invariant, for the same reason. Stripping a stray number out of a sentence leaves a sentence that still parses, still reads fluently, and now claims something its author did not write. A rejected draft costs a retry; a repaired one is undetectable.

**The accepted false-positive rate.** Check 2 will occasionally reject legitimate prose — "on the one hand", "second-guess". The prompt tells the model to avoid number words entirely, the retry absorbs the rest, and the failure direction is toward saying nothing rather than toward saying something unverifiable. That is the correct direction, and §6.1 asks whether the list is drawn in the right place.

### 2.6 The prompt

A frozen system prompt carrying the role, the output contract, and the boundaries; a per-request user message carrying the report, the vocabulary with current values, and which sections are available.

What it is told: the reader is learning to trade in a **paper-trading simulator**; write three short paragraphs, one per section, each two to four sentences; explain the mechanism behind a figure rather than restating it ("concentration this high means a single name moves the whole account", not "your HHI is high"); plain language, no jargon left undefined; every figure as a placeholder from the supplied list; no number words.

What it is not told to do: be encouraging, congratulate, or soften. A portfolio that lost money should read as a portfolio that lost money — the analytics are neutral and the prose that explains them should be too.

The report goes in the **user** message, not the system prompt, so the system prompt stays byte-stable across users. Prompt caching is not claimed as a benefit here — the stable prefix is near the ~1024-token minimum and the §2.10 cache is what actually bounds cost — but a stable system prompt costs nothing and keeps the option open.

### 2.7 Cost control: the cache does most of it, a per-user cap does the rest

The content-hash cache (§2.10) means a user who is not trading generates once a day. A user who *is* trading changes the report hash on every fill, and each changed hash is a new billable call behind an ordinary authenticated endpoint. That is a cost-amplification surface, and it is the one genuinely new class of risk this step introduces — every other QuantSim endpoint costs CPU when abused; this one costs money.

**A per-user daily generation counter in Redis, `narrative:count:{user_id}:{yyyy-mm-dd}`, with a cap.** Over the cap, the endpoint returns the fail-open shape with `reason: "daily generation limit reached"`. The counter increments on a *generation*, never on a cache hit, so ordinary reading is unaffected.

**When Redis is unavailable, generation is refused rather than allowed** — and this is deliberately the opposite posture to §2.10's fail-open cache, for a stated reason. The same outage removes both the cache and the cap simultaneously, which is precisely the combination that turns a normal traffic pattern into an unbounded bill: every request becomes a miss *and* every request becomes uncounted. Failing open on the cache costs latency; failing open on the cap costs money without a ceiling. The user-visible result is the same degraded response either way.

**This premise is false today, and the plan fixes it first.** With Redis down, `/insights/portfolio` currently returns a **502** rather than a degraded 200 — not because the cache fails closed, but because `trading-engine`'s `GET /trading/portfolio` degrades to 8.7s against 5.8ms healthy and trips this service's 5s upstream timeout. The cause is not the retry behaviour `docs/NEXT_SESSION.md` records: **there is no retry.** `Service.price` is a *sequential loop* issuing one price lookup per holding at 3s each, so per-symbol timeouts **compose additively** and the endpoint's worst case is N × 3s, unbounded in N. `tasks/plan.md` C2 bounds that loop before any work in this step lands. Without it the whole of §2.7 reasons about a difference the user could never observe, because the response is a 502 in both branches.

### 2.8 The narrative explains; it never advises

The prose describes what happened and why the figures mean what they mean. It does not tell the reader what to do about it: no imperative about buying or selling a named security, no forward-looking claim about a price or a return, no "you should".

This is a paper-trading simulator, and QuantSim's repository is public. Prose that says "you should reduce your NVDA position" reads exactly the same whether the account is simulated or not.

**This one cannot be enforced the way §2.2 enforces figures**, and the spec says so rather than implying a guarantee it does not have. It is a system-prompt rule, checked by reading output during the manual pass and during review. The honest description is: figures are structurally impossible, advice is prompted against and spot-checked. §6.3 asks whether that asymmetry is acceptable or whether a deny-list check is worth its false positives.

### 2.9 Model: `claude-opus-5`, adaptive thinking, low effort

`claude-opus-5` — $5/$25 per MTok. The payload is roughly 1.5K tokens in and 400 out, so a generation runs around two cents. Adaptive thinking is on by default on Opus 5 (leaving `Thinking` unset runs adaptive; the adaptive union is equivalent) and `budget_tokens` is removed on this model — sending it is a 400.

Effort `low`: the task is constrained phrasing of a supplied object, not analysis. `MaxTokens` of 2000 with a non-streaming call, which is comfortably inside the SDK's default timeout and inside §2.11's budget.

**One binding is unconfirmed.** `output_config.effort` has no documented Go binding in the reference material available at drafting. At implementation, confirm the field against the installed `anthropic-sdk-go` and let the compiler settle the name; **if the installed version has no binding for it, ship at the default effort and record that in the checklist** rather than dropping to raw HTTP for one parameter. The SDK is the interface this project uses; a hand-rolled HTTP call to work around a missing field would be a worse trade than a slightly more expensive request.

The model ID is a constant in one place, overridable by `ANTHROPIC_MODEL` for experimentation, and is echoed in the response so a narrative can be attributed to what wrote it.

### 2.10 Caching: keyed on the report's content hash, 24-hour TTL

Key: `narrative:{user_id}:{report_hash}`, where `report_hash` is a SHA-256 over the canonical JSON of the Step 20 report **with `computed_at` excluded**.

That exclusion is the whole mechanism. `computed_at` changes on every recomputation, so hashing the report as serialized would produce a fresh key every five minutes and a cache that never hit once — a defect that looks like working code and shows up only on the bill. Nothing else is excluded: every field that is a *measurement* participates, so any change to any figure produces a new narrative, and no change to any figure produces the old one.

TTL 24 hours. Errors are never cached, so a degraded response does not stick — the same rule Step 20 §2.8 set. Fail-open in both directions: a cache read failure computes, a cache write failure returns the narrative anyway.

**The accepted consequence:** identical numbers give identical prose. A user re-reading an unchanged portfolio sees the same three paragraphs, word for word. That is correct — the same facts should not be described differently on a refresh — but it will read as staleness to someone expecting a fresh take, and it is a deliberate choice rather than an oversight.

### 2.11 What fails how: the report is an error, the phrasing is not

The narrative endpoint calls the **same** `service.PortfolioInsights` path the existing endpoint calls. It re-derives nothing, so it inherits the reconciliation guard, the 404 on an unavailable symbol, the 401 on a refused token, and the 502 on an upstream outage, unchanged and untested twice.

**If the report fails, the narrative endpoint returns the report's error** — 404, 401 or 502 as appropriate. There is nothing to phrase, and inventing a 200 with an empty narrative would hide a real failure behind a cosmetic one.

**If generation fails, the endpoint returns 200** with `narrative: null`, `state: "unavailable"`, and a short machine-readable `reason`. Every figure in the report is already correct and available at the other endpoint; the prose is an enhancement, and its absence is not an outage. This covers a missing API key, a timeout, a rate limit, a refusal, an over-cap user (§2.7), and two failed validations (§2.5).

`ANTHROPIC_API_KEY` unset is a supported configuration, not a boot failure — matching `REDIS_URL`'s precedent in this service — and is logged loudly at startup for the same reason: running without it is a surprise nobody chose rather than a decision somebody made.

**Timeout budget:** 20 seconds on the generation call, inside the gateway's 30-second `ResponseHeaderTimeout` with margin. The generation call is a separate client from the 5-second `requestTimeout` in `internal/client` that bounds `trading-engine` and `market-data`, so the two budgets do not interact. No retry on timeout; the retry in §2.5 is for a rejected draft, not for a slow one.

**The trap to avoid is the one §2.7 describes, one layer up.** A slow upstream tripping a *shorter* downstream timeout is exactly what turns a Redis outage into a 502 today, and it happens because a per-item timeout was mistaken for a per-request bound. The generation call has one item and one timeout, so it does not have that shape — but any future work that loops over symbols, sections or users inside this endpoint reintroduces it, and **the bound belongs on the loop, not on the call.**

### 2.12 Degraded sections are omitted, not filled

Step 20's sections carry a `state` and can be `insufficient_data`. The prompt names which sections are unavailable and why, and the model writes nothing for them — the corresponding key is simply absent from `narrative`.

**If all three sections are degraded, no API call is made at all.** There is nothing to phrase, and an account with no trades would otherwise produce a paid call whose entire output is encouragement — which §2.6 forbids anyway. The endpoint returns the fail-open shape with `reason: "no analysis is available to describe"`.

---

## 3. What is deferred, and why it is recorded here

| Deferred | Why it is not in this step |
|---|---|
| **Insights frontend** | Step 22. §2.4's formatting note and §2.1's per-section shape are the two things it will need from here. |
| **Streaming** | A UI affordance for a response that is short and cached. Revisit only if Step 22 shows the wait is felt. |
| **Conversational follow-up** | The user's message would become untrusted input to a prompt that currently has no untrusted input at all. A different threat model deserves its own spec. |
| **Narrating backtest runs** | The same placeholder machinery would apply to `backtests`; worth doing once this one has run in anger. |
| **Prompt-caching the system prompt** | §2.6 keeps the prefix stable so this stays available; the §2.10 cache makes it not worth measuring yet. |
| **A deny-list check for advisory language** (§2.8) | Its false-positive behavior is unknown and the manual pass is the cheapest way to find out whether it is needed. §6.3. |
| **Per-user prompt personalization** | Nothing in the schema describes a user's experience level, and inventing one to feed a prompt is a data-model decision, not a phrasing one. |

---

## 4. Testing strategy

Unit-first, matching `docs/TESTING_STRUCTURE.md`. **No `integration/` package** — this step, like Step 20, touches no database. **No test makes a network call**: the generator sits behind an interface in `service/interfaces.go` with a mock in `service/mock/`, as every other external dependency in this service does.

**The validator (§2.5)** is the highest-value target and gets exhaustive table-driven coverage: a digit in the first character, the last, mid-word, inside a token name, inside a decimal; each spelled numeral and fraction word; a bare unit word; an unknown placeholder; an unclosed brace; nested braces; an empty draft; a draft that is only whitespace; a draft over each cap.

**The adversarial case that states the property:** a draft containing a number that is *correct* — the true drawdown, formatted exactly as the renderer would format it — must still be rejected. Correctness is not the criterion; provenance is. A validator that passes this case has quietly become a whitelist and the step's guarantee is gone.

**The vocabulary/renderer pair (§2.3, §2.4)** — a property test that every key `Placeholders` returns renders to a non-empty string, and that the prompt's advertised list and the substitution table are the same map, not two maps that agree today. Golden tests for each value kind's formatting, with hand-written expected strings rather than values captured from a first run (Step 18 §4).

**Formatting parity with the frontend (§2.4)** — a table of figures formatted here and the expected `format.ts` output beside it, so a drift shows up as a failing Go test rather than as two spellings of one number on a screen in Step 22.

**Degradation (§2.11, §2.12)** — a missing API key, a timeout, a rate limit, an over-cap user, and two failed validations each produce 200 with `narrative: null` and their own `reason`; a report-level 404/401/502 propagates unchanged; all three sections degraded makes **zero** generator calls, asserted on the mock's call count rather than on the response alone.

**Caching (§2.10)** — two reports differing only in `computed_at` produce the same hash; two differing in any measurement produce different hashes; a failed generation writes nothing; a Redis outage still returns 200 for the cache and refuses generation for the counter (§2.7).

**Adversarial pass before merge**, per this project's standing practice: make the digit check return `nil`; delete one word from the banned list; make `Placeholders` omit a key the prompt advertises; invert the cap comparison; make the counter fail open. Each must break a test. A green suite that survives those mutations is not evidence.

**Manual pass:** a real key against the dev database's portfolio. Read the prose. Check **every figure in it against the JSON report field by field** — that is the acceptance criterion for the whole step, and it is done by eye because that is what a user will do. Then read it again asking only "is any sentence here advice?" (§2.8). Restore the dev database to its `users=20, accounts=20` baseline afterwards.

---

## 5. Structure, commands, and conventions

**Layout**, extending the existing service:

```
services/ai-insights/internal/
  llm/         client.go, prompt.go                      // §2.6, §2.9
  narrative/   placeholders.go, render.go, validate.go,
               generate.go, types.go                     // §2.2-§2.5
  cache/       redis_narrative_cache.go                  // §2.10, §2.7
  handler/     narrative.go, router.go (one route added)
  service/     interfaces.go (+NarrativeGenerator), mock/
```

**Wiring:** `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL`, and the daily cap to `.env.example`. No `go.work` change, no Makefile change, no gateway change — the module and the `/insights/*` route already exist.

**The new dependency must not break off-workspace builds.** `github.com/anthropics/anthropic-sdk-go` goes in `services/ai-insights/go.mod`, and `cd services/ai-insights && GOWORK=off go build ./...` must pass afterwards. All seven modules pass today and Dockerization is the next roadmap item, which is exactly the off-workspace case — regressing it here would surface as a confusing Docker build failure in a later step rather than as a test failure in this one.

**Conventions carried, not re-decided:** the `code` + `message` JSON error shape; `service` returns sentinel errors and `handler` maps them; every external dependency behind an interface with a mock; loopback bind; the caller's `Authorization` forwarded on the report path exactly as Step 20 §6.5 settled it. The Anthropic client is constructed once at boot and reused, like the Redis client.

---

## 6. Open questions for review

1. **Where is the banned-word list drawn?** (§2.5 check 2.) Rejecting `one` and `two` will bounce ordinary prose — "on the one hand" — and rejecting neither leaves "you made two trades" unverifiable. **Recommendation: reject everything from `one` upward, plus fractions and magnitudes, and accept the retries.** `behavior.trade_count` and `risk.position_count` exist precisely so small counts have tokens; a model reaching for the word instead of the token is a model that has slipped the contract, and catching that is the point.
2. **Is a 20-second generation timeout too generous for a user-facing endpoint?** It is sized to the gateway's 30-second ceiling rather than to anyone's patience. **Recommendation: keep 20s for now and let Step 22 set it.** A timeout tuned before there is a UI to feel it is a guess, and `docs/deferred-tuning.md` is where this class of number already lives.
3. **Is prompt-plus-spot-check the right enforcement for the no-advice rule (§2.8)?** It is materially weaker than §2.2's guarantee, on a public repo. **Recommendation: yes for this step, and revisit after the manual pass with real output in hand.** A deny-list on "should", "consider", "recommend" would fire on legitimate explanatory prose, and building it before seeing a single generated paragraph would be tuning against an imagined failure.
4. **What is the daily per-user generation cap (§2.7)?** **Recommendation: 50.** Far above any honest use with a 24-hour content-hash cache in front of it, and low enough that a runaway client costs about a dollar rather than an open-ended amount. It is one constant in one file.
5. **Should `narrative` be three keyed strings or one passage?** §2.1 chose three, for Step 22's layout. If the UI turns out to want one flowing summary instead, joining three paragraphs is trivial and splitting one is not — which is the argument for keeping three, but it is worth confirming before the prompt is written around it.
