# Implementation Plan — Insights Frontend: the Report on the Page, and the Prose After It (Step 22)

## Context

Branch: `step22-insights-frontend`, cut from `main` at `89c48e3`. Squashed to one `feat(step22)` commit and merged `--no-ff`, matching Steps 16–21.

Starting state, from `docs/NEXT_SESSION.md`:

```
users=20 accounts=20 trades=0 orders=0 positions=0
historical_prices = 3507 rows
Redis: no leftover narrative:* keys
```

**`trades=0` is load-bearing for this plan.** Every section of the report degrades to `insufficient_data` on an account with no history, which means the default state of the dev database exercises §2.5 and nothing else. T11's manual pass has to create trades to see an `ok` report at all, and restore afterwards — the same shape as Step 21's T12, including the restore-and-verify-by-query.

Both endpoints are live and tested. The gateway's `/insights/*` wildcard already routes both. Nothing in this step's frontend work requires the backend to change except T2.

## Decisions carried in from SPEC.md §2 (not reopened here)

- Sixth tab, no router (§2.1).
- Two requests; figures never wait on prose; no polling (§2.2).
- `report_hash` on the report response; prose suppressed on disagreement (§2.3, §9.1 accepted).
- `toLocaleString` at one decimal place for percents; the 2dp gap recorded, not acted on (§2.4, §9.3 accepted).
- Branch on `state` before reading a figure; degraded sections render their `reason` and nothing else (§2.5).
- Nine backend reasons collapse to four sentences, with the "figures above are unaffected" clause (§2.6).
- First narrative load is automatic; every later generation is a click (§2.9, §9.2 accepted).
- `as_of_date` once, in the panel header, from the report (§9.4 accepted).
- Pure-function tests only; no component rendering tests (§2.10).

## Six decisions this plan adds

**D1 — the parity table's expected values are checked in as literals, produced once from the Go
implementation.** Not computed at test time from a TypeScript port of `roundHalfAway`. This is the
direct lesson of Step 21's T11 survivor: a test whose expectation is derived the same way as the
value under test proves the two agree *when built the same way*, which is not the drift it was
written to preclude. The expectations here are strings the Go renderer actually printed, so the
test can fail if `format.ts` and `render.go` ever diverge — which is its entire purpose.

**D2 — the hash-agreement predicate fails closed.** Absent report hash, absent narrative hash, or
either one empty ⇒ **not** agreement, so the prose is not shown. `report_hash` is `omitempty` on
the narrative response, so "absent" is reachable in normal operation rather than only in a bug, and
`undefined === undefined` is the exact expression that would make a broken check look like a
working one in the manual pass.

**D3 — the backend field lands first, in its own task, before any frontend code refers to it.** If
the frontend's type declared `report_hash` while the server did not send it, D2's fail-closed rule
would suppress every narrative — a total failure that looks like a working suppression rule, and
that the manual pass would diagnose as a narrative outage. Ordering removes the ambiguity.

**D4 — the two hooks share nothing.** Separate status unions, separate errors, separate refetch. A
combined hook would have to invent a state for "report ok, narrative failed", which is the
majority-of-the-time state this whole design exists to render, and is not an error.

**D5 — the three section components carry no logic worth a test, and that is a design constraint,
not an observation.** Anything in them that needs asserting is extracted to a pure module first
(`narrative-state.ts`, `insights-errors.ts`, `format.ts`). If a section component grows a branch
that wants a test, that branch is in the wrong file.

**D6 — the manual pass forces all four states, including the two that cannot occur by waiting.**
`insufficient_data` is the database's default; `ok` needs trades; narrative-`unavailable` needs
`ANTHROPIC_API_KEY` unset; hash disagreement needs an order placed with the tab open. Three of the
four are configuration or action, so none of them is "hopefully observed".

## Dependency graph

```
T1 format.ts percent formatters ──┐
   (pure, no deps)                │
                                  ├──> T5 hooks ──> T6 panel + tab ──> T7 sections ──┐
T2 backend report_hash ──> T3 wire layer ──> T4 pure logic ──┘                       │
   (D3: first)              (types+client)   (copy maps,                             │
                                              hash predicate)                        │
                                                                                     v
                                                            T8 NarrativeBlock ──> T9 order refetch
                                                                                     │
                                                                                     v
                                                              T10 adversarial ──> T11 manual ──> T12 docs
```

T1 and T2 are independent and could be done in either order. Everything after T3 is a chain.

**On vertical slicing:** T3–T5 are a horizontal foundation (wire layer, copy maps, hooks) rather
than a vertical slice, deliberately. They total five small files and are shared by all three
sections identically; slicing them per-section would triple the churn in `api/` and produce three
partial hooks. The first vertical slice is T6, which puts a real report on a real screen; T7 fills
it in, T8 adds the prose. Each of T6–T9 leaves the tab working.

---

## Phase 1 — Foundation: the two things nothing else can proceed without

### T1 — Percent formatting (SPEC §2.4, D1)

**Description:** Add `formatPercent` and `formatSignedPercent` to `frontend/src/format.ts`, with
the parity table that pins them to the Go renderer.

**Acceptance:**
- [ ] `formatPercent(12.35)` → `"12.4%"`; `formatSignedPercent(12.35)` → `"+12.4%"`;
      `formatSignedPercent(-12.35)` → `"-12.4%"`; `formatSignedPercent(0)` → `"0.0%"` (no `+`).
- [ ] Parity cases checked in as Go-derived literals (D1), covering at minimum the negative
      halfway values where `toFixed` diverges: `-99.85`, `-99.55`, `-99.35`, `-98.85`, `-7.25`,
      and the positive counterparts.
- [ ] A comment states why `toLocaleString` and not `toFixed`, with the measurement.

**Verify:** `cd frontend && npm test` — new cases pass; existing `format.test.ts` cases unchanged.

**Dependencies:** None · **Scope:** S (2 files)

### T2 — `report_hash` on the report response (SPEC §2.3, D3)

**Description:** Wrap `service.PortfolioInsights` in a handler-level `InsightsResponse` carrying
the content hash, so a separately-fetched narrative can be checked against the figures on screen.

**Acceptance:**
- [ ] `GET /insights/portfolio` gains exactly one JSON key, `report_hash`; every existing key is
      unchanged in name, position and value.
- [ ] The hash is `service.ReportHash` of the **unmodified** report struct — no field added to
      `PortfolioInsights`, so no existing narrative cache key moves.
- [ ] A handler test asserts the response's `report_hash` equals `service.ReportHash` of the same
      report, and that a degraded report still carries one.

**Verify:** `make vet && make test`; `go test -race -count=1 ./...` in `services/ai-insights`;
`curl` the endpoint and diff the key set against `main`'s output for the same account.

**Dependencies:** None · **Scope:** S (2 files)

### Checkpoint A
- [x] `npm test` green; `make vet`/`make test` green across all seven modules
- [x] `GOWORK=off go build ./...` passes for `ai-insights`
- [x] ~~The live endpoint's JSON contains `report_hash` and it matches the narrative endpoint's
      `report_hash` for the same account **in the same minute** — checked by eye, once~~
      **Superseded by a test.** `TestTheTwoEndpointsAgreeOnTheReportHash` drives both routes off
      one router and compares. This assumption is the one the whole of §2.3 rests on, which is an
      argument for asserting it on every run rather than looking at it once; and what would break
      it is a code change, not an environment, so the live check adds nothing the test does not
      already cover. The live stack still exercises this at T11, where a real account is on screen.

---

## Phase 2 — The data layer

### T3 — Wire layer: two response types, two client methods (SPEC §3)

**Description:** Add `PortfolioInsightsResponse` and `NarrativeResponse` to `api/types.ts`, and
`api.insights()` / `api.narrative()` to `api/client.ts`.

**Acceptance:**
- [ ] Types mirror the Go structs field for field, including which fields are optional:
      `reason`, `risk_profile`, `as_of_date`, `report_hash`, `model`, `generated_at` are optional;
      every figure is not.
- [ ] Section `state` is `'ok' | 'insufficient_data'`; narrative `state` is
      `'ok' | 'unavailable'` — unions, not `string`, so §2.5's branch is exhaustive.
- [ ] `narrative` is `Record<string, string> | null`, matching a map that marshals as `null`.
- [ ] Both methods are authenticated and go through `request`, like every other method.

**Verify:** `npm run build` (tsc) clean.

**Dependencies:** T2 · **Scope:** S (2 files)

### T4 — The two copy maps and the hash predicate (SPEC §2.3, §2.6, §2.7, D2)

**Description:** `insights/narrative-state.ts` (reason → copy, hash agreement) and
`insights/insights-errors.ts` (report error code → copy), with tests.

**Acceptance:**
- [ ] All nine backend reasons map per §2.6's table; `no analysis is available to describe` maps to
      *no* copy, distinctly from the generic sentence.
- [ ] An unrecognised reason falls through to the generic sentence and never renders the raw string.
- [ ] `describesReport(report, narrative)` returns **false** when either hash is absent or empty
      (D2), asserted for all four absence combinations.
- [ ] All four report error codes map per §2.7; unknown codes and status-0 transport errors both
      have copy.

**Verify:** `npm test` — new suites pass.

**Dependencies:** T3 · **Scope:** S (4 files)

### T5 — The two hooks (SPEC §2.2, D4)

**Description:** `use-insights.ts` and `use-narrative.ts`. Status unions, fetch on mount, no
polling, explicit refetch. `use-narrative` additionally exposes `regenerate()`.

**Acceptance:**
- [ ] Neither hook sets an interval.
- [ ] Both use the request-id guard from `use-portfolio.ts` so a stale response cannot land after a
      newer one or after unmount.
- [ ] `use-narrative` fetches once on mount and thereafter only when `regenerate()` is called
      (§2.9) — no effect re-fires it on a report change.
- [ ] A narrative request that fails at the transport level is an error state; a `200` with
      `state: 'unavailable'` is **not** — it is a successful response carrying a reason (§2.6).

**Verify:** `npm run build` clean; `npm run lint` clean. Behaviour is verified at T6/T11, not by a
rendering test (D5).

**Dependencies:** T3, T4 · **Scope:** S (2 files)

### Checkpoint B
- [ ] `npm run build`, `npm run lint`, `npm test` all clean
- [ ] Both hooks compile against the real response types with no `any` and no cast

---

## Phase 3 — Figures on the screen

### T6 — The panel and the sixth tab (SPEC §2.1, §2.2, §2.7, §9.4)

**Description:** `InsightsPanel.tsx` — header with `as_of_date`, loading/error/ok states for the
report, and the three section slots. Sixth `Tab` value wired into `Dashboard.tsx`.

**Acceptance:**
- [ ] Tab renders; loading state matches the skeleton convention already in `Dashboard.tsx`.
- [ ] All four report error codes render their §2.7 copy; a 401 renders nothing (the client has
      already handled it).
- [ ] `as_of_date` appears once, in the header, from the report — not per section (§9.4).
- [ ] The tab is not scoped to `selected`; `OrderTicket` still renders beside it.

**Verify:** `npm run build`; drive the tab in the browser against the dev stack with `trades=0` —
the degraded path is what this state produces, and it must render without a single figure.

**Dependencies:** T5 · **Scope:** M (2 files)

### T7 — The three sections (SPEC §2.5, §2.8, D5)

**Description:** `RiskSection.tsx`, `BenchmarkingSection.tsx`, `BehaviorSection.tsx`.

**Acceptance:**
- [ ] Each branches on `state` before reading any figure, and a degraded section renders its
      `reason` and zero figures (§2.5).
- [ ] Every figure uses the formatter matching the `Kind` the backend gives that same figure in
      `placeholders.go`, which is the authoritative mapping and was read off it during T3:

      | figure | backend Kind | frontend |
      |---|---|---|
      | `cash_weight_pct`, `largest_position_pct`, `annualized_volatility_pct`, `weight_pct` | `KindPercent` | `formatPercent` |
      | `max_drawdown_pct` | `KindPercent` — **unsigned on purpose**, a drawdown is reported as a magnitude and the wire really does carry it positive | `formatPercent`, never `formatSignedPercent` |
      | `portfolio_return_pct`, `return_pct`, `excess_return_pct` | `KindSignedPercent` | `formatSignedPercent` |
      | `market_value` | `KindMoney` | `formatPrice` |
      | `quantity` | `KindQuantity` | `formatQuantity` |
      | `trade_count`, `occurrences`, `trading_days` | `KindCount` — grouped, so 1234 reads "1,234" | `formatQuantity` |
      | `portfolio_sharpe`, `sharpe` | `KindRatio` — 2 decimals | `formatRatio` |
      | `turnover_ratio`, `occurrences` | `KindRatio` / `KindCount` | **not rendered** — both are already inside `Finding.detail`, written by Go, and the turnover sentence uses one decimal where a `KindRatio` formatter uses two |
      | `concentration_hhi` | `KindIndex` — 3 decimals | `formatIndex` |

- [x] **Added after T3, landed before T4:** `formatRatio` (2dp) and `formatIndex` (3dp), with the
      same two-directional parity treatment T1 gave percents. This also closed SPEC §2.4's recorded
      2-decimal gap rather than carrying it — see the todo entry.
- [ ] No local formatting anywhere in a section component.
- [ ] `risk_profile` absent ⇒ the band is not rendered at all (§2.5).
- [ ] `Finding.detail` is displayed and never parsed; `evidence_trade_ids` are not rendered, not
      linked, and not counted (§2.8).
- [ ] Severity uses design tokens (`warn` → `--color-down`, `info` → `--color-ink-muted`), no raw hex.

**Verify:** `npm run build`, `npm run lint`; browser check against both a degraded and an `ok`
report (the `ok` one needs T11's seeded trades — if that is not yet done, this is verified degraded
here and re-verified at T11).

**Dependencies:** T6 · **Scope:** M (3 files)

### Checkpoint C
- [ ] Every figure the report carries appears on screen exactly once, and no figure it does not
      carry appears at all
- [ ] A degraded report shows three reasons and zero numbers
- [ ] `npm run build`, `npm run lint`, `npm test` clean

---

## Phase 4 — The prose, composed safely

### T8 — `NarrativeBlock` (SPEC §2.3, §2.6, §2.9)

**Description:** One section's prose, or the explanation of its absence. Consumes T4's map and
predicate.

**Acceptance:**
- [ ] Prose renders beneath its section's figures, never above and never in place of them.
- [ ] `describesReport` false ⇒ prose is replaced by "These figures have changed since this summary
      was written" plus the regenerate control. **No stale prose reaches the DOM** — it is not
      rendered dimmed, collapsed, or behind a disclosure.
- [ ] Each of the four §2.6 copy outcomes renders, with the "figures above are unaffected" clause
      where the table says so.
- [ ] A narrative section present for a report section that is degraded is not rendered (the
      figures it would sit under do not exist).

**Verify:** `npm run build`; browser check with `ANTHROPIC_API_KEY` unset (unavailable path) and
set (ok path).

**Dependencies:** T7 · **Scope:** M (2 files)

### T9 — Refetch on fill, regenerate on click (SPEC §2.9)

**Description:** `onOrderPlaced` refetches the report; the narrative is left alone until the user
asks for a new one.

**Acceptance:**
- [x] Placing an order with the tab open refetches the report and **not** the narrative.
- [ ] The refetched report's new hash makes `describesReport` false, so T8's stale-prose branch is
      what the user sees — this is the mechanism, **verified at T11 by observing it**, not by
      trusting it. This is the one part of T9 a browser has to confirm.
- [x] "Write a new summary" issues exactly one narrative request per click. **Not via `disabled`,
      as this criterion originally said** — `disabled` and a conditionally-absent button both take
      effect on the NEXT render, and two clicks fit inside one frame. A synchronous `inFlightRef`
      check at the top of `use-narrative`'s `load` is the only thing that can stop the second call
      before it is made; the control is *absent* while pending on top of that.

**Verify:** Browser: place an order on the insights tab; observe the figures change, the prose
replaced by the notice, and the network panel showing no narrative request until the button is
pressed.

**Dependencies:** T8 · **Scope:** S (2 files)

### Checkpoint D
- [ ] The full flow works end to end against the live stack
- [ ] `npm run build`, `npm run lint`, `npm test` clean; `make vet`/`make test` green
- [ ] Billable generations to date recorded

---

## Phase 5 — Evidence and documentation

### T10 — Adversarial pass

**Description:** Mutate the code and confirm the tests fail. Step 21's T11 found one real survivor
by doing this properly; the mutations here target the claims this step actually makes.

**Acceptance — at minimum these mutations, each confirmed applied before being judged:**
- [ ] `toLocaleString` → `toFixed` in both percent formatters ⇒ the parity table fails. **If it
      passes, the table is the wrong table** and D1 was not honoured.
- [ ] `describesReport`'s absence guard removed (bare `===`) ⇒ a test fails on the
      both-hashes-absent case.
- [ ] The unrecognised-reason fallthrough changed to return the raw string ⇒ a test fails.
- [ ] `state` branch removed from one section so figures render on a degraded report ⇒ caught by
      the manual pass if not by a test; if nothing catches it, record that as the finding.
- [ ] The `+` dropped from `formatSignedPercent` ⇒ a test fails.
- [ ] Each mutation reverted and the revert verified, not assumed. A mutation reported as SURVIVED
      whose replacement never actually applied is the trap Step 21 hit; re-check the diff.

**Verify:** `npm test` per mutation; survivors recorded in `todo.md` with what was done about each.

**Dependencies:** T9 · **Scope:** S

### T11 — Manual pass against the live stack (D6)

**Description:** All four states forced, every figure read by eye, database restored and verified.

**Acceptance:**
- [ ] **Degraded:** default database (`trades=0`) ⇒ three reasons, zero figures.
- [ ] **`ok`:** trades seeded through the real order path ⇒ every figure on the card compared
      character by character against the same figure in the sentence beside it (§8.4 of the spec —
      this is the acceptance test for T1's parity claim, and the only one run against real values).
- [ ] **Narrative unavailable:** `ANTHROPIC_API_KEY` unset ⇒ figures intact, §2.6 copy shown.
- [ ] **Hash disagreement:** order placed with the tab open ⇒ prose replaced, regenerate offered,
      new summary correct after the click.
- [ ] Database restored to `users=20 accounts=20 trades=0 orders=0 positions=0`,
      `historical_prices=3507`, **verified by query**, not by assumption. Redis `narrative:*` keys
      cleared. All services killed.
- [ ] Total billable calls and spend recorded.

**Verify:** the queries themselves, pasted into `todo.md`.

**Dependencies:** T10 · **Scope:** M

### T12 — Documentation

**Acceptance:**
- [ ] `docs/PHASE4_CHECKLIST.md` — Step 22 entry, including T10's survivors.
- [ ] `docs/NEXT_SESSION.md` — rewritten, not appended. Phase 4's AI half is now visible; what
      remains is Dockerization, cloud deployment, `deferred-tuning.md`, and the standing small
      items (`market-data` store tests, security backlog 8 then 3).
- [ ] `agents.md` — "Insights frontend — Step 22" moves from planned to done, with what it shipped.
- [ ] The §2.4 measurement recorded somewhere durable, and `render.go`'s claim that "toFixed…
      rounds away from zero" corrected in place — it is the note that would mislead the next person
      to touch this, and leaving it costs nothing to fix now.
- [ ] `docs/TESTING_STRUCTURE.md` §6a extraction trigger **re-confirmed unfired** with the reason
      (no new `integration/` package), rather than left unmentioned for a fourth step.
- [ ] Spec, plan and todo archived to `docs/archive/phase4-step22-insights-frontend/`; root
      `SPEC.md` and `tasks/` do not survive onto `main`.

**Dependencies:** T11 · **Scope:** S

### Checkpoint E — pre-merge
- [ ] `npm test`, `npm run build`, `npm run lint` clean
- [ ] `make vet`, `make test` green across all seven modules; `make test-integration` 63/0
- [ ] `go test -race -count=1 ./...` clean on `ai-insights`
- [ ] `GOWORK=off go build ./...` passes for all seven modules
- [ ] Adversarial pass complete, survivors recorded
- [ ] Manual pass complete, database restored **and verified by query**, services killed
- [ ] Total spend recorded
- [ ] Adversarial review of the branch before merge — green tests are not evidence on their own

---

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| The dev database's `trades=0` means every default render is degraded, so the `ok` path is easy to leave unverified | High — it is the path with all the figures in it | T11 seeds trades through the real order path and restores after, with a verifying query. T7's browser check explicitly says "re-verified at T11" rather than pretending the degraded check covered it |
| `describesReport` written as a bare `===` looks correct and suppresses nothing when both hashes are absent | High — silently reintroduces the exact failure §2.3 exists to prevent | D2 fails closed; T4 asserts all four absence combinations; T10 mutates the guard away and requires a failure |
| The parity test written as a TypeScript port asserted against itself | High — a test that cannot fail, which is the defect Step 21 shipped and caught late | D1: expectations are Go-produced literals; T10 mutates to `toFixed` and requires the table to go red |
| Manual pass burns generations at $0.02 each; a regenerate button makes it easy to burn more | Low — capped at 50/day/user, ~$1 worst case | Budget ~10 calls per Step 21's measured figure; T9 disables the control while in flight; spend recorded at Checkpoint D and T11 |
| Prose and figures disagree only in a window the manual pass never happens to hit | Medium | T11 forces the window rather than waiting for it (D6) |
| Scope creep into a chart, now that there is a surface to put one on | Medium — the report carries no series, so any chart is a client-side derivation | Named as a non-goal in SPEC §1 and a "never" in the boundaries; nothing in this plan produces a series to plot |

## Open questions

None. SPEC §9's four were resolved before this plan was written: 9.1 yes (T2), 9.2 automatic first
load (T5, T9), 9.3 render Sharpe (T7), 9.4 header only (T6).
