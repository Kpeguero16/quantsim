# Todo — Insights Frontend: the Report on the Page, and the Prose After It (Step 22)

Tracks `tasks/plan.md`'s 12 tasks and 5 checkpoints. **T1–T10 done. Checkpoints A, B and C passed;
Checkpoint D half done (API level). T11 next — it needs a browser and a sign-in.**

Branch `step22-insights-frontend`, cut from `main` at `89c48e3`. **14 commits, nothing pushed.**

---

## Resuming here — read this first

**What remains is Checkpoint D, T11, T12 and Checkpoint E — all of it evidence and
documentation, and everything still open needs the live stack.** Nothing is half-finished; the
working tree is clean apart from this file, `tasks/plan.md` and root `SPEC.md`, which are
untracked on purpose and must not be committed.

**T10 closed with one structural change, not just a record.** The section types in
`api/types.ts` are now unions on `state` (commit `9548011`), because the adversarial pass found
that nothing — test, build or lint — stopped a degraded section's figures from rendering as
`0.0%`. Full record at T10 below. If you are picking this up cold, read that entry before T11:
the manual pass's degraded case is now defended by the compiler, which changes what T11 is
actually confirming there (that the *copy* is right, not that the guard exists).

**State of the machine.** **The stack is UP as of this session** — six services on 8080-8085
(`/healthz` 200 each) plus Vite on 5173, started with `make run-*`, logs under the session
scratchpad. Postgres and Redis containers up. The database is back at the documented baseline —
`users=20 accounts=20 trades=0 orders=0 positions=0`, `historical_prices=3507` — and Redis holds
no `insights:*` or `narrative:*` keys. Verified by query after the API checks, not assumed.
If you are resuming later, assume the services are gone and restart them.

~~**T10 needs no running stack.**~~ Done. 10 mutations, 8 killed, 2 survived, one of those fixed.

**T11 does**, and it is the task with real prerequisites:
- `ANTHROPIC_API_KEY` **is set in `.env`**, so unlike Step 21's T12 this is not blocked. Budget
  ~10 billable calls, ~$0.20 at Step 21's measured rate.
- `trades=0` means the dev database **cannot produce an `ok` report**. Trades must be seeded
  through the real order path, and restored afterwards **and verified by query**.
- It is the **first time the figure grids are seen in a browser**. Everything so far was verified
  by server-rendering the components against a real report body, which is strong evidence about
  values and placement but proves nothing about layout, wrapping, or dark-theme contrast on the
  up/down colors.
- Signing in is Khalil's step — tokens are memory-only.

**Mutations run and killed: 31.** 21 recorded against the task that ran them (T1, T2, T4, T8),
plus T10's 10. The deliberate hunt for what had not been attacked yet is what found M4 — the same
way Step 21's one real survivor was found, late and by looking rather than by running the list.

**What to be suspicious of.** The parity tables, `describesReport` and `narrativeView` are the
three places where a test could pass while the thing it names is broken. Each has now been
mutated directly (M1, M2, M6, M7, M8, M11) and each went red. What T10 showed is that the
untested layers are where the real gap was: the components and the hooks. One of those two gaps
is closed by the compiler now; the other (`inFlightRef`, M10) is live and is in the carry-over
list with a named home.

---

| | Billable calls to date |
|---|---|
| Checkpoints A–C | 0 — no narrative is generated before T8 |
| Checkpoint D | **0 so far** — the degraded path never reaches the model |
| T11 | budget ~10, ~$0.20 — all of it the `ok` case; the degraded case is free |

---

## Phase 1 — Foundation
- [x] T1 Percent formatting — `formatPercent`, `formatSignedPercent`, plus a **27-case** parity
      table whose expectations were printed by `render.go`'s own `format()` through `KindPercent`
      and `KindSignedPercent`, not by a TypeScript port (D1). `toLocaleString`, not `toFixed`.
      **Two things the sweep did not predict, both found by dumping the real renderer:**
      `useGrouping: false` is required — the backend groups money but not percentages, so a
      1234.5% return would otherwise render `1,234.5%`; and the `+` is decided on the **raw**
      value, so `+0.04999%` renders `+0.0%` on both sides while an exact `0` renders `0.0%`.
      Deciding the sign after rounding would have disagreed and no sweep would have shown it.
      A negative value too small to display renders `-0.0%` on both sides — ugly, and parity
      beats prettifying one side.
      **Found in review, and the most important thing in this task:** the guard was
      one-directional. `render_test.go` already had a parity table pinning Go against `format.ts`
      for money, quantities and dates — but not percentages, because `format.ts` had none when it
      was written. So the new TS table would catch the *frontend* drifting and **nothing at all**
      would catch `render.go` drifting. Changing `places` there from 1 to 2 left every test in both
      languages green while the two disagreed on screen. Eleven percent cases added to the Go
      table (test renamed `TestRender_MatchesTheFrontendFormatter`, since it is no longer
      money-and-dates); the 1→2 mutation now kills 7 of them and the sign-after-rounding mutation
      kills the one case that exists for it. Both directions are now guarded.
- [x] T2 `report_hash` on `GET /insights/portfolio` — handler-level `InsightsResponse` wrapper.
      Embedded, so the body gains exactly one top-level key and no existing consumer moves; the
      hashed struct is untouched, so no narrative cache key minted since Step 21 changes.
      Three sub-tests: a full report, a degraded one, and an assertion that the embed **flattened**
      rather than nesting under `"PortfolioInsights"` — the failure that would break every existing
      consumer silently.
- [x] Checkpoint A — `npm test` **89 passed**; `make vet` and `make test` green across all seven
      modules; `go test -race -count=1 ./...` clean on `ai-insights`; `GOWORK=off go build ./...`
      passes for all seven; `make test-integration` **63/0**, the documented baseline; `gofmt`
      clean; `npm test` **91 passed**; `npm run build` clean; `npm run lint` **4 warnings, the
      same 4 the branch point has** — verified by running lint against a stashed tree rather than
      assumed.
      **Deviation from the plan, deliberate:** the two endpoints' agreement on `report_hash` was to
      be checked by eye against the live stack. It is a test instead —
      `TestTheTwoEndpointsAgreeOnTheReportHash` drives both routes off one router and compares.
      Eyes verify one account once; the thing that would break this invariant is a code change, not
      an environment, so a test is the stronger evidence and the cheaper one. It runs with a nil
      generator, so it also pins that a **degraded** narrative still carries a hash to compare —
      the response whose `report_hash` a reader is most likely to assume is absent.

## Phase 2 — The data layer
- [x] T7 prerequisite, pulled forward — `formatRatio` (2dp) and `formatIndex` (3dp).
      **This closed SPEC §2.4's recorded 2-decimal gap instead of carrying it, and changed the
      percent implementation too.** Measuring 3dp before writing the code showed plain
      `toLocaleString` disagrees with Go on **272 of 270,002** values at 2dp and **184** at 3dp —
      decimal literals like `-9.995` whose double sits a hair below the halfway point, where Go's
      scale-then-round sees the boundary and Intl's shortest-form reading does not. A four-line
      port of `roundHalfAway` (round the magnitude, reapply the sign, because `Math.round` sends
      negative halves toward +∞ where Go's sends them away from zero) scores **0 of 270,002 at
      both precisions, and 0 of 60,002 at one place** — so all four formatters now share it and
      the percent tables stayed green unchanged, which is what proves the port is
      behaviour-identical where the old code was already right.
      Recorded honestly: 0 across 330,004 measured values is evidence, not proof — the two
      languages still hand an already-rounded double to two different decimal renderers at the
      last step. The two parity tables are what keep it honest.
      **Mutations, all killed:** the port replaced by plain `toLocaleString` (10 red), `KindRatio`
      2dp→3dp (7 red), `KindIndex` 3dp→2dp (6 red).
- [x] T3 Wire layer — nine interfaces and two client methods. Unions for `state`, `severity`,
      `code` and `risk_profile` rather than `string`, so §2.5's branch is exhaustive at compile
      time; `narrative` nullable; optionals matching Go's `omitempty`.
      **Verified mechanically rather than by eye, twice.** A script diffs every `json:"..."` tag in
      the nine Go structs against the nine TS interfaces — name, order and optionality — and all
      nine match (field ORDER was off by one on `report_hash` and was fixed; `types.ts` claims the
      two files can be diffed by eye, which only holds if the order does). Then the real response
      bodies for a traded account, a degraded account and an `unavailable` narrative were dumped
      from the handler and assigned to the types as object literals under `tsc`: excess-property
      checking makes an undeclared key a compile error, and a negative control with a `bogus_key`
      added confirmed the check has teeth rather than passing vacuously.
      **Found while reading `placeholders.go` for the formatter mapping:** `KindRatio` (Sharpe,
      turnover) and `KindIndex` (HHI) have no counterpart in `format.ts` at all, and T1 did not
      add one because SPEC §2.4 only discussed percentages. Recorded as an explicit addition to
      T7 with the full figure→Kind table, so it is not discovered while writing a component.
- [x] T4 Copy maps and the hash predicate — `narrative-state.ts` and `insights-errors.ts`, 35 new
      tests. `describesReport` fails closed on any absent or empty hash (D2), **six** absence
      combinations asserted rather than four (empty string is as reachable as `undefined` and
      `'' === ''` is true the same way).
      **A test caught a real error in my own spec reading:** §2.6 collapses nine reasons to *four*
      outcomes, not three — silence is an outcome, and it is the one easiest to lose by folding it
      into the generic branch. The code comment said three; the assertion said three; the code did
      four. Fixed in both.
      **The gap this task actually had, closed from the Go side:** the frontend maps three reasons
      **by value**, which makes them a cross-language contract held together by a string literal.
      Change one in `narrative.go` and the frontend keeps compiling, keeps passing its own tests,
      and quietly shows "a written summary isn't available right now" where it should say
      "written summaries are turned off". Nothing in either language could catch that, so
      `reasons_internal_test.go` now pins those three to their exact text and names the frontend
      file in the failure message. The other six are deliberately unpinned — the frontend maps them
      by exclusion, so their text is free to change, which is the point of mapping them that way.
      An internal (`package handler`) test, unlike everything else here, because the constants are
      unexported and widening the package API for a test is the worse trade.
      **Six mutations, all killed:** absence guard removed (2 red), unrecognised reason rendered
      verbatim (10 red), silence folded into the generic branch (2 red), retry offered everywhere
      (1 red), `invalid_token` surfaced (1 red), and `reasonOverCap`'s text changed (the new Go pin
      fires, naming the file and the consequence).
- [x] T5 The two hooks — no polling, request-id guard, `regenerate()` on the narrative side.
      `useNarrative` takes **no arguments at all**, which is how criterion 3 is met structurally
      rather than by discipline: a hook that cannot see the report cannot have an effect that
      re-fires on it, and that effect would have made every fill a billed call.
      A `200` carrying `state: 'unavailable'` is a success — and there is **no branch** making it
      one, because the client throws only on a non-2xx. Verified by reading `client.ts`, not by
      writing a test for a branch that does not exist.
      **Simplified before committing:** the first draft gave `useNarrative`'s error state its own
      copy, which duplicated `narrativeNotice`'s generic sentence in a second file with a different
      apostrophe. A transport failure and a `200` saying "the narrative service is unavailable" are
      the same fact to a reader, so the error state now carries **no message** and both spellings
      come from `narrativeNotice`. Two copies of one sentence in two files is how they drift.
      The `invalid_token` special case went with it: when a 401 survives the client's refresh the
      report request fails too, `InsightsPanel` renders its error state, and no narrative copy is
      reachable.
- [x] Checkpoint B — `npm run build` clean, `npm test` **153 passed**, `npm run lint` **6
      warnings, 4 baseline + 2 new**. The two new ones are the identical
      `react-hooks/exhaustive-deps` complaint the three existing request-id hooks already carry —
      same rule, same line, same reason — and each new hook now carries `use-portfolio.ts`'s
      explanation so the next reader does not read it as an oversight.
      No `any` and no cast in the modules. The **one** cast is in a test and is the point of that
      test: `report_hash` is declared required, so passing `undefined` requires saying so out loud,
      and the assertion is that `describesReport` still fails closed when the declared type turns
      out to be a claim the wire did not honour.
      Verified structurally rather than by rendering (D5): no `setInterval`/`setTimeout` anywhere
      in `src/insights/`, both hooks take no parameters, and both effects depend only on a
      `useCallback` with an empty dependency list — one fetch per mount, and no second path to a
      billed call.

## Phase 3 — Figures on the screen
- [x] T6 `InsightsPanel` + the sixth tab — **visually confirmed by Khalil against the running stack**.
      Header carries `as_of_date` once and hides it when the report has none; the four error codes
      render their copy and `invalid_token` renders nothing; tab not scoped to `selected`;
      `SectionNotice` renders a degraded section's heading and reason and **zero figures**.
      Split from T7 at the section boundary: T6 owns loading, error, header and the degraded path,
      T7 adds the figure grids for `ok` sections. That makes T6 fully verifiable against the dev
      database as it stands, where `trades=0` means every section is degraded.
      **Verified end to end against the running stack**, which is stronger than the unit tests:
      `GET /insights/portfolio` through the gateway returns `report_hash` `12993c07fd81fa6a` and
      `GET /insights/portfolio/narrative` returns **the same hash** — SPEC §2.3's invariant holding
      across two real services and a proxy, not just across one router in a test. Three sections
      degraded with reason `"no trades yet"`, and no `as_of_date`, which is exactly the shape the
      panel was written for.
      **Cleanup done and verified by query, not assumed.** A throwaway account had been registered
      through the API to get a token. Deleted; `accounts` fell with it via the FK cascade, so the
      database is back to `users=20 accounts=20 trades=0 orders=0 positions=0`,
      `historical_prices=3507` — the documented baseline exactly. The curl calls had also left an
      `insights:{user_id}` cache key pointing at a user that no longer existed; it had a 5-minute
      TTL and would have expired on its own, but it was deleted rather than waited out. Redis is
      down to 13 keys, all pre-existing `price:*` and `revoked:*`, and there are **no**
      `insights:*` or `narrative:*` keys. There is no `refresh_tokens` table to orphan — Step 13
      put revocation in Redis.
- [x] T7 The three sections — each owns its own `state` branch, because "branch before reading" is
      only worth anything in the same file as the reading. `risk_profile` absent ⇒ no band;
      `detail` shown never parsed; `evidence_trade_ids` not rendered, not linked, not counted.
      **Two things the acceptance criteria did not anticipate, both found while writing it:**
      *(a)* `turnover_ratio` and `occurrences` are **already inside `Finding.detail`**, written by
      Go. Rendering them as their own figures — which the plan's table said to do — would have put
      the same number on screen twice, and in the turnover case in two spellings: the sentence
      uses `%.1fx` ("3.4x") where a `KindRatio` formatter renders two places ("3.40"). "3.4x" above
      "3.40" is exactly the disagreement this step exists to prevent, arrived at by being helpful
      rather than by a model. They exist on the struct so the *narrative* can name them as
      placeholders — a different consumer with a different need. Plan table corrected.
      *(b)* `window.start_date` and `window.trading_days` were carried by the report and rendered
      **nowhere**. A 1.2% volatility measured over three days and the same figure over three years
      are not the same claim. Both now sit in the header beside `as_of_date`. `computed_at` stays
      unrendered on purpose — it is cache age, not data age, and showing it beside these figures
      would date them wrongly.
      **Checkpoint C, verified mechanically rather than by eye:** a script walks all **42** fields
      on the report type and confirms each is either rendered or on a 9-item deliberate list with
      its reason — nothing is merely forgotten. And the components derive nothing: no arithmetic,
      no `reduce`, no `Math.`, no `.filter`; only `.length > 0` guards, sign comparisons and one
      pluralisation.
      **The `ok` path was rendered and read figure by figure without a database.** `trades=0` means
      the dev stack cannot produce an `ok` report, so the real traded-account body was
      server-rendered through the three components in a one-off (not committed) and every figure
      checked against the JSON it came from: `98.95052473763118` → `99.0%`, `1.0494752623688157` →
      `1.0%`, HHI `1` → `1.000`, `5.298553258534417` → `5.30`, `-4.95000000000001` → `-5.0%`,
      `market_value 1050` → `$1,050.00`. Best of all, `portfolio_return_pct 0.04999999999999449`
      → **`+0.0%`** — the sign-decided-before-rounding rule from T1, visible on a real value.
      **No browser check for this task, deliberately.** A degraded report carries no `as_of_date`,
      so the new header renders nothing, and the three sections render the same `SectionNotice`
      with the same props as before — the degraded tab is byte-identical to the one already
      approved. The figure grids cannot be seen in a browser until T11 seeds trades, and the
      server-render above is stronger evidence than a screenshot of an empty state would be.
- [x] Checkpoint C — 42/42 report fields accounted for (33 rendered, 9 deliberately not, each with
      a recorded reason); no derived figures anywhere in the components; degraded report shows
      three reasons and zero numbers. `npm run build`, `npm test` (153) and `npm run lint` (6, the
      T5 baseline) all clean

## Phase 4 — The prose, composed safely
- [x] T8 `NarrativeBlock`, `NarrativeStatus`, and `narrativeView` — 11 new tests.
      **The task turned out to be a pure function, not a component.** "Stale prose never reaches
      the DOM" written as four conditions in JSX can be satisfied in four places and violated in a
      fifth with nothing failing. `narrativeView` collapses request state + report into ONE value
      whose `kind: 'prose'` is the only route to a sections map, so the negative property has a
      single producer and is testable without rendering anything.
      **Prose is passed INTO the section rather than rendered beside it**, which makes "a degraded
      section shows no prose" structural: the degraded branch returns before the line that would
      render it. Confirmed by handing a degraded section prose in a one-off server-render — the
      string is absent from the HTML, not merely hidden (`leaked: false`).
      Placement confirmed the same way: prose renders **last inside the section card**, after every
      figure.
      **The notice is once per panel, not once per section.** A narrative describes the whole
      report; three copies of "a written summary isn't available right now" is one fact stated
      three times. It sits below the figures because the copy says the figures *above* are
      unaffected — the geometry is part of the sentence.
      **Three mutations, all killed:** the hash check removed (5 red), the null-map guard disabled
      (1 red), `pending` collapsed into `silent` (1 red).
- [x] T9 Refetch on fill, regenerate on click. `Dashboard` counts fills and passes the count to
      `InsightsPanel` → `useInsights(refreshKey)`, which joins it to the **mount effect's**
      dependencies rather than adding a second effect: mount fetches once, each fill fetches once,
      and no path fetches twice.
      `useNarrative` has no equivalent and takes no arguments, so a fill changes the figures, the
      hash stops matching, `narrativeView` replaces the stale prose with a notice, and buying a
      replacement is a button the reader presses. Verified by grep that `refreshKey`/`fills` reach
      nothing narrative-shaped and that `regenerate` is reachable only from the two
      `NarrativeStatus` render sites.
      `useInsights` no longer returns `refetch` — nothing called it once the counter existed, and
      an exported handle nobody holds is a second way to trigger a fetch with no caller to justify
      it.
      **The double-spend half landed in T8, and not as the plan specified.** The plan said
      "disabled while in flight"; `disabled` and a conditionally-absent button both take effect
      only on the NEXT render, and two clicks fit inside one frame. The real guard is a synchronous
      `inFlightRef` check at the top of `use-narrative`'s `load`.
- [~] Checkpoint D — **API-level half done; the browser half is T11 and needs a sign-in.**
      Stack up: six services healthy on 8080-8085 (`/healthz` 200 each), Vite on 5173, Postgres and
      Redis already running. **Billable calls so far: 0** — see why below, it is a real finding.

      Checks run against a throwaway account (`step22-apicheck@local.invalid`), created through the
      real register path and **deleted afterwards, baseline re-verified by query**:

      | Check | Result |
      |---|---|
      | `GET /insights/portfolio` unauthenticated | 401 `invalid_token` — the code §2.7 maps to a silent error |
      | `GET /insights/portfolio/narrative` unauthenticated | 401 `invalid_token`; the gateway wildcard covers both paths |
      | `report_hash` on the report | **present — `12993c07fd81fa6a`**. T2's field works end to end through the gateway |
      | three sections on a fresh account | all `insufficient_data`, reason `"no trades yet"` |
      | narrative on a degraded report | `state: unavailable`, reason `"no analysis is available to describe"`, `narrative: null` |
      | narrative's `report_hash` vs the report's | **identical** — `describesReport` returns true on real bodies |
      | hash stability across two calls | same hash, same `computed_at` — served from the 5-minute report cache |
      | Redis after the run | `insights:{user_id}` written; **no `narrative:*` key**, nothing was generated |

      **The degraded narrative path costs nothing.** `no analysis is available to describe` is
      decided before any model call — confirmed by an untouched ai-insights log and by the absent
      narrative cache key. So T11's degraded case is free, and the ~10-call budget is entirely for
      the `ok` case. Worth knowing before seeding.

      **The live report is the §2.5 hazard in the flesh.** The degraded body really does carry
      `cash_weight_pct: 0`, `max_drawdown_pct: 0` and three more — five zeros that measured
      nothing, exactly as Step 20 intended. Those real bodies were then fed through the real
      `narrative-state` module rather than through fixtures: `describesReport` true,
      `narrativeNotice` → `{show: false}`, `narrativeView` → `{kind: 'silent'}`.
      And `tsc` refuses `r.risk.cash_weight_pct` on that body —
      `Property 'cash_weight_pct' does not exist on type 'DegradedSection'` — verified by removing
      the `@ts-expect-error` and watching the error appear. The T10 union holds against live data,
      not just fixtures.

      Still open for T11: the `ok` report (needs seeded trades), the figure-by-figure parity read,
      narrative `unavailable` with the key unset, and the hash-disagreement case.

## Phase 5 — Evidence and documentation
- [x] T10 Adversarial pass — **10 mutations run this session, 8 killed, 2 survived.** Full record
      below. Five named mutations minimum. **Three already run and killed, at the
      point the code was written rather than saved for the end:** `toLocaleString`→`toFixed`
      (10 parity cases red), `useGrouping: false` removed (2 red), and the handler hashing an empty
      report instead of the real one (all three `report_hash` sub-tests plus the agreement test
      red). **Two more from the pre-commit review, on the Go side:** `KindPercent` 1dp→2dp (7 red)
      and the signed-percent `+` decided after rounding instead of before (1 red) — neither of
      which anything could have caught before the review closed that gap. Each reverted and the
      revert re-run green. The remaining named mutations still apply. `toLocaleString`→`toFixed` **must** turn
      the parity table red; if it does not, the table is wrong and D1 was not honoured. Verify each
      replacement actually applied before judging it survived — Step 21's trap

      **Result — 10 mutations, each verified applied (non-empty diff) before being judged and
      verified reverted after. Cumulative with the 21 run earlier: 31.**

      | # | Mutation | Verdict |
      |---|---|---|
      | M1 | `fixed()` → plain `toFixed(places)` | KILLED — 14 red, every one a halfway case |
      | M2 | `describesReport` absence guard → bare `===` | KILLED — 2 red (both-absent, both-empty) |
      | M3 | unrecognised reason leaks the raw backend string | KILLED — 11 red |
      | M4 | a section's `state` guard removed | **SURVIVED → fixed structurally, see below** |
      | M5 | `+` dropped from `formatSignedPercent` | KILLED — 13 red |
      | M6 | `narrativeView`'s hash check deleted | KILLED — 5 red |
      | M7 | `formatRatio` 2dp → 3dp | KILLED — 15 red |
      | M8 | `formatIndex` 3dp → 2dp | KILLED — 12 red |
      | M9 | `invalid_token` returns a sentence instead of `null` | KILLED — 1 red |
      | M10 | `use-narrative`'s `inFlightRef` double-spend guard removed | **SURVIVED → carry-over** |

      M1 turning the parity table red on exactly the halfway cases is the check the plan demanded:
      the table is the right table and D1 was honoured.

      **M4 — the real survivor, and the finding this pass existed to produce.** Removing a
      section's `state` branch *and* its now-unused `SectionNotice` import — which is what an
      editor's remove-unused-imports fix does unprompted — left `npm test`, `npm run build` and
      `npm run lint` **all green** while a degraded report rendered `0.0%` for all five risk
      figures. Removing the guard alone failed the build, but only on the unused import, which is
      incidental and would not survive a tidy-up. Nothing tested, built or linted defended the one
      rule the spec's "Never" list puts third.
      **Fixed rather than recorded** (Khalil's call, after the cost was measured): each section
      type is now a union on `state`, so the figures exist only on the `ok` arm. The mutation is
      now a compile error naming each field — 12 across the three sections. Zero logic changed;
      the components already branched correctly. Commit `9548011`.
      Note the direction: this is the same trade §2.3 made — spend a few lines to make a silent
      wrong number impossible rather than unlikely — applied to a hazard §2.5 left to review.

      **A trap worth writing down, hit twice.** The mutation driver reverted with
      `git checkout -- <file>`, which also reverted an *uncommitted* fix in the same file, leaving
      the tree red while the batch still reported per-mutation verdicts. Both times it was caught
      only by noticing that mutations which had previously reported `build=PASS` had started
      reporting `build=FAIL`. Restore from a copy of the pre-mutation file, not from HEAD, whenever
      the working tree carries uncommitted work. The verdicts themselves were unaffected.

> **CLEANUP DONE — baseline restored and verified by query.**
> The throwaway account `step22-verify@local.invalid` and everything under it are deleted.
> `backtests` was checked first (0 rows) because it does not cascade on a user delete.
> ```
> users=20 accounts=20 trades=0 orders=0 positions=0 historical_prices=3507
> insights:* keys = 0   narrative:* keys = 0
> ```
> The services and the Vite dev server are still RUNNING. Kill them before calling the machine
> clean. The parity fixtures in `frontend/src/insights/__fixtures__/` are kept on purpose: they
> are the evidence for `parity.live.test.ts` and contain only synthetic dev figures.

- [x] T11 Manual pass — **all states verified in the browser. Three real bugs found: one fixed,
      two recorded as backend work.**

      | Case | Result |
      |---|---|
      | Degraded (`trades=0`) | three reasons, **zero figures**, narrative correctly **silent** — no second explanation of the same emptiness (§2.6) |
      | Mixed (`trades=1`) | Behavior `ok` rendering "Trades / 1" **beside** two degraded sections; `risk_profile` correctly absent (derived from two degraded sections) |
      | Hash disagreement | fill → refetch → new hash → prose replaced by "These figures have changed since this summary was written." + "Write a new summary"; click → new prose matching the new report (§2.3, §2.9) |
      | Narrative unavailable | key unset → "Written summaries are turned off.", **no retry button**, figures untouched (§2.6) |
      | Full `ok` report | **DONE** — 4 trades backdated across 72 trading days; all three sections `ok`, `as_of_date` 2026-07-28, `risk_profile: aggressive` |
      | Percent parity (criterion 4) | **DONE, and as a test rather than a memory of having looked** — see below |

      **Criterion 4 is closed, by test (commit `29dce08`).** Khalil approved backdating the seeded
      fills so the reconstruction spans real history: 4 trades (AAPL/MSFT/SPY) moved to
      April–June 2026 against the stored bars, giving a 72-trading-day window and an all-`ok`
      report. The narrative describing it was captured alongside, and
      `insights/parity.live.test.ts` now asserts all 13 figures render character-for-character
      into the Go prose — cash weight, largest position, HHI, volatility, drawdown, portfolio
      return and Sharpe, and return/excess/Sharpe for both benchmarks.

      **The two parity checks catch different faults, and this was measured rather than assumed:**
      mutating `fixed()` to `toFixed` leaves the live file entirely GREEN (no figure a real
      portfolio produces lands on a halfway case — SPEC.md §2.4's own 20,000-double result showing
      up in practice) while turning 14 of the unit table red. Dropping the sign, or moving the HHI
      to 2 or 4 places, or the Sharpe to 1, turns the live file red and leaves the table green. So
      the table owns ROUNDING and the live file owns FORMATTER SELECTION, which nothing tested
      before.
      **The assertion needed two rounds of sharpening, both found by mutation, not by reading:** a
      bare `toContain` accepted `"5.9%"` inside `"+5.9%"`, and a left-only boundary accepted
      `"0.50"` inside `"0.504"`, which let a 3dp→2dp HHI mutation survive. Guarded on both sides
      now.

      **BUG 1 (fixed, commit `a699d46`).** The narrative never resolved in the dev server — the
      panel sat on "Preparing a written summary…" forever. `useNarrative`'s two guards deadlocked:
      the mount effect's cleanup bumped `requestIdRef` to disown the in-flight request, and the
      in-flight guard then blocked the re-run from issuing a replacement, so the response arrived
      to a bumped id and was discarded with nothing left to re-trigger it. The tell was the request
      count across one mount: report **twice**, narrative **once**. Dev-only in effect (production
      does not double-invoke effects, and a real remount allocates fresh refs) but it blocked every
      browser check of the narrative, and the old code's correctness rested on refs being
      recreated. `load()` now re-adopts the in-flight request's id; no request is issued on that
      path, so the double-spend guard is untouched.
      **This landed exactly in the M10 carry-over area** — the untested hook guard we chose not to
      test an hour earlier. That decision is worth revisiting.

      **BUG 2 (open, not this step's code).** §2.9's premise is "the report refetches, its hash
      changes". It does not, for up to five minutes. The fill-triggered refetch fires correctly
      (verified: exactly one extra `/insights/portfolio` after the fill) but the backend serves the
      cached report from `insights:{user_id}` (TTL 300s), so the **identical** report and hash come
      back. Observed directly: after the first order the panel still read "no trades yet", and only
      changed once the cache key was deleted by hand. So a user who trades and opens Insights sees
      figures that predate their own fill, with nothing marking them stale — a wrong number on
      screen, which is the class of failure this whole step exists to prevent. Every
      hash-disagreement check above required clearing the cache manually first.
      The frontend cannot fix this; it has no way to demand a fresh report. Fixing it means
      invalidating `insights:{user_id}` when a trade is recorded, which is `services/ai-insights`
      or `trading-engine` work and squarely in the spec's **"Ask first"** list.

      **BUG 3 (open, backend, and the most consequential of the three).** `ReportHash` is NOT
      stable for unchanged data. Twelve recomputes of one untouched account produced **six
      distinct hashes**. The figures displayed are identical; the drift is in the last
      floating-point digits of three fields:

      ```
      portfolio_sharpe          -1.250186398016479  /  -1.2501863980164782  /  -1.2501863980164765
      annualized_volatility_pct  6.595452504842138  /   6.5954525048421395  /   6.595452504842146
      concentration_hhi          0.503853148389607  /   0.5038531483896073
      ```

      Ordering is NOT the cause — positions and benchmarks come back in a stable order, and
      `concentrationHHI` is a plain sequential loop. The drift is upstream, in the reconstruction
      that feeds those figures; `histories.go` fetches price histories concurrently through an
      `errgroup`, and float addition is not associative, so a different completion order gives a
      different last digit. `ReportHash` zeroes `ComputedAt` before hashing, so the timestamp is
      correctly excluded and is not the cause.

      **Why it matters — it breaks two of this step's stated claims:**

      1. **The cost model (§9.2).** The narrative cache key is `narrative:{user_id}:{report_hash}`.
         A new hash is a cache miss, and a cache miss is a billable generation. §9.2 says opening
         the tab repeatedly on an unchanged account "costs one generation per day". It does not:
         roughly half of recomputes mint a new key. The five-minute report cache is the only thing
         limiting recomputes, so sustained viewing can cost about one generation per five minutes,
         bounded only by the 50/day cap.
      2. **§2.3's composition check, in the false-positive direction.** The two endpoints compute
         the report independently. If the report cache expires between them, identical data can
         hash differently, `describesReport` returns false, and the panel replaces correct prose
         with "These figures have changed since this summary was written" — a stale-data warning
         when nothing is stale. §2.3 was built to stop a wrong number reaching the reader; this
         makes it occasionally suppress a right one.

      Not a frontend defect, and `describesReport` failing closed is still the correct behavior
      given two disagreeing hashes. The fix belongs in `services/ai-insights` — either make the
      reconstruction deterministic, or round the figures to their published precision before
      hashing so the hash reflects what is actually shown. **"Ask first" scope; recorded, not
      attempted.**

      **Billable generations: 4** (the fourth describes the backdated `ok` report and is the one
      the parity fixtures were taken from). Earlier count was 3 (initial, regenerate-after-fill, and one after the key was
      restored). Cap is 50/day/user. Narrative cache keys confirmed as
      `narrative:{user_id}:{report_hash}` — one per report hash, exactly as designed.

- [ ] T11 Manual pass — all four states forced (degraded / `ok` / narrative unavailable / hash
      disagreement); every figure read against the sentence beside it; database restored to
      `users=20 accounts=20 trades=0 orders=0 positions=0`, `historical_prices=3507` and
      **verified by query**; Redis `narrative:*` cleared; services killed; spend recorded
- [x] T12 Documentation — done.
      - `docs/PHASE4_CHECKLIST.md` — full Step 22 entry: the percent convention and the note Step
        21 got wrong, the two parity tests and why neither subsumes the other, the guard nothing
        was holding, the narrative that never appeared, the two backend defects, the verification
        totals, and seven "things worth knowing". "Still open" updated: Step 22 closed, three new
        items opened.
      - `docs/NEXT_SESSION.md` — **rewritten**, not appended. Leads with the fact that the branch
        is complete but NOT merged.
      - `agents.md` — Step 22 marked done with what it actually shipped; `ReportHash` instability
        and the fill-invalidation gap added to the roadmap.
      - `services/ai-insights/internal/narrative/render.go` — **the wrong claim corrected in
        place**, comment only, with the measured numbers. `NEXT_SESSION.md` carried the same claim
        and was corrected too. gofmt clean, `GOWORK=off go build` OK.
      - `SPEC.md` — **amended inline at §2.9 and §9.2**, marked as amendments rather than
        rewritten, because the running stack disproved a stated premise in each.
      - `docs/TESTING_STRUCTURE.md` §6a — **re-confirmed unfired**, checked rather than assumed:
        Step 22 added no `integration/` package, harness still in three copies.
      - Archived to `docs/archive/phase4-step22-insights-frontend/` (SPEC, plan, todo), matching
        the Step 21 layout.

      **Full verification re-run at documentation time, not quoted from memory:** frontend 179
      tests / 9 files, build clean, lint 5 warnings; `make vet` clean; `make test` 0 failures
      across seven modules; `make test-integration` **63/0**; `GOWORK=off go build ./...` OK for
      all seven modules.
- [x] Checkpoint E — **passed.**

      **Full green, re-run at documentation time rather than quoted from memory:** frontend 179
      tests across 9 files, `npm run build` clean, `npm run lint` 5 warnings (all pre-existing
      `exhaustive-deps` on sibling hooks); `make vet` clean; `make test` 0 failures across all
      seven modules; `make test-integration` **63/0**, unchanged; `GOWORK=off go build ./...`
      green for all seven modules.

      **Adversarial review: `/code-review ultra`, run by Khalil in the cloud over the branch diff
      (34 files, +3828/-78). Zero findings.**

      That is meaningful precisely because it was independent. The mutation work, the browser
      pass and all three defect diagnoses in this step were mine, so the branch had been reviewed
      almost entirely by the agent that wrote it. The three places flagged as least trustworthy
      going in — `use-narrative.ts` (the deadlock fix, and the only file here with no test
      coverage), the section unions in `api/types.ts` (which deliberately diverge from the wire
      shape), and `parity.live.test.ts` (captured fixtures, so the test most able to rot quietly)
      — all came back clean.

      **What the clean result does NOT cover, and this bound matters.** The review was scoped to
      the diff. Both open backend defects live in code this branch never touched, so they were
      never in scope: `ReportHash` instability and the report cache defeating a fill's refetch
      remain open, documented in `PHASE4_CHECKLIST.md` and amended into `SPEC.md` at §9.2 and
      §2.9. Zero findings means the branch is clean, not that the system is.

## Carry-over items deferred out of this step, with a named home
Recorded so none of them rests on someone remembering. Full reasoning in `tasks/plan.md`.
- ~~The 2-decimal-place parity gap~~ — **closed**, not carried. The `fixed()` port removed it at
  every precision rather than only documenting it. Money still goes through `formatPrice`, which
  keeps its own plain `toLocaleString` and therefore keeps the gap: it predates this step, its
  values are dollar amounts rather than computed ratios, and changing it would move a figure
  already shipped in Steps 8/15/17. Named here rather than silently left
- **`use-narrative`'s `inFlightRef` double-spend guard is untested (T10/M10).** Removing it passes
  test, build and lint. It protects a *billed* call: two clicks inside one frame buy two
  generations. Untested because §2.10 scopes tests to pure functions and the hooks have none, and
  standing up `renderHook` late in this step is a larger change than the union above — deliberate,
  not overlooked. **Home:** the first step that adds hook-level tests; until then it is exercised
  by hand in T11's hash-disagreement case, where the regenerate button is clicked anyway. Verify
  by eye there that a double-click yields one generation, and record the observed call count.
- `market-data` store tests (`historical_price_store.go`) — its own small step; still unstarted
  after being "next" since Step 20
- Integration harness in three copies — `TESTING_STRUCTURE.md` §6a trigger stays **unfired**; this
  step adds no `integration/` package. Re-confirmed in T12
- Security backlog item 8 (Unicode-normalise passwords) — its own small step, and soon
- Security backlog item 3 (Argon2id) — its own step; carries a migration strategy
- Dockerization, then cloud deployment — next roadmap items after this one
- `docs/deferred-tuning.md` — unblocked by deployment; this step adds no entry
