# SPEC — Insights Frontend: the Report on the Page, and the Prose After It (Step 22)

Status: **Implemented and merged.** Two sections carry T11 amendments (§2.9, §9.2) where the running stack disproved a stated premise; both are marked inline. §7 carries what is genuinely open. §2.3 and §2.4 are the two decisions worth arguing about; the rest is the Step 15/17 pattern applied again.

Scope: `frontend/src/` — a new `frontend/src/insights/` directory, additions to `frontend/src/api/{client.ts,types.ts}`, two new functions in `frontend/src/format.ts`, and a sixth tab in `frontend/src/market/Dashboard.tsx`. Plus **one additive backend field** (§2.3): `report_hash` on `GET /insights/portfolio`. No migration, no new table, no gateway change (the `/insights/*` wildcard at `services/gateway/internal/handler/router.go:134` already covers both paths), and **no change to any figure Step 20 computes or any rule Step 21 enforces**.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase4-step21-insight-generation/`.

---

## 1. Objective

Steps 20 and 21 shipped a complete, tested analytics API that no user can see. `GET /insights/portfolio` returns risk, benchmarking and behavior for the authenticated account; `GET /insights/portfolio/narrative` returns three paragraphs of prose about that same report in which every figure was rendered by Go from the report struct. Both are proxied through the gateway. Neither has a pixel of UI.

**Objective:** wire both endpoints into the existing dashboard as a sixth tab — the report's figures rendered as a card layout, and the narrative filled in beneath each section as it arrives — using the conventions Steps 8/13/15/17 established: a typed wire layer in `api/`, status-union hooks, Tailwind design tokens, WHY-only comments, `vitest` on the logic-bearing units.

**The problem this step actually has to solve.** Step 21's guarantee is that no figure on the page was written by a model. That guarantee lives inside one service, and this step is the first thing to compose its output with a separately-fetched copy of the same report. Two requests, two independently-computed reports, one screen. If they disagree, the page shows a card reading `-12.4%` next to a sentence reading `-11.8%`, both correct for the report *they* were computed from, and the reader has no way to tell which. That is Step 21's failure mode reappearing one layer up, and it is not solved by fetching carefully — it is solved by making the mismatch detectable (§2.3).

The second thing this step has to get right is smaller and sharper: **the frontend now formats percentages that the backend also formats**, and the two must agree character for character or the same figure reads two ways on one screen. §2.4 is that, with measurements.

**Non-goals:**

- **Charts.** No equity curve, no drawdown plot, no weight pie. The report carries no series — `Reconstruction` never leaves the service layer, exactly as Step 16's `EquityCurve` didn't. Charting something the backend does not send would mean deriving it client-side, which is the one thing this whole phase was built to avoid.
- **Streaming the narrative.** Step 21 §1 deferred it here; the answer is still no. Three short paragraphs behind a 24-hour cache arrive as one response in a few seconds. Streaming would buy a typing animation and cost a second transport path.
- **Chat or follow-ups.** Step 21's non-goal, unchanged. There is no endpoint for it.
- **Backtest narratives.** `/insights/portfolio*` is the live account only.
- **A separate insights page or a router.** §2.1.
- **Any change to thresholds, bands, finding rules, or the prompt.** If a figure reads badly on the page, that is a finding for a later step. Step 21 §1 named this direction of influence as the thing to avoid, and a UI is a much stronger temptation to violate it than a JSON body was.
- **Persisting or exporting a narrative.** The Redis cache is a cache. There is no "save this summary".

---

## 2. Design decisions

### 2.1 A sixth tab, not a page

`Dashboard.tsx` drives five views as `Tab` state (`chart | positions | orders | portfolio | backtest`). Add `'insights'` as a sixth.

Same reasoning that closed Step 15 §2.1 and Step 17 §2.1, and nothing has changed: no stated need for a deep link or browser-back between views, and a sixth string in an existing union costs nothing a router wouldn't cost in dependency weight and provider wiring.

Like the backtest tab and unlike the four trading tabs, the insights tab is **not scoped to `selected`** — the report is about the whole account, not the charted symbol. `OrderTicket` stays pinned in the right column as it does on every other tab; §2.9 covers what happens when an order is placed while this tab is open.

### 2.2 Two requests. The numbers render first and are never blocked by the prose

Two independent hooks, both fired on tab mount:

- `useInsights()` → `GET /insights/portfolio`. Everything the page shows structurally: the three sections, their figures, their states.
- `useNarrative()` → `GET /insights/portfolio/narrative`. Prose only.

The report renders as soon as it lands. The narrative renders into each section when *it* lands, and a narrative that is slow, degraded, or absent changes nothing about the figures already on the screen. This is the arrangement `docs/NEXT_SESSION.md` scoped ("render the numbers first and fill the prose in after, which is why they are two endpoints") and it is also the only arrangement consistent with Step 21 §2.11's split: a failed report is an error, failed phrasing is not. A page that waited for prose before showing figures would convert the second failure into the first.

No polling on either. Both are derived from a report computed as of the last completed trading day; intraday price ticks do not move a single figure in it. `useOrders`'s precedent (Step 15 §2.3) applies exactly: nothing outside this browser session changes the inputs, so there is no clock-driven reason to refresh.

### 2.3 The composition race, and the one backend field that closes it

**The problem.** The two endpoints each compute the report independently. Both read the same five-minute report cache (`insights:{user_id}`), so in the ordinary case they see the identical report and agree. But the cache can expire or be evicted between the two requests, and if the account's trade history changed in that window the narrative's substituted figures describe a *different* report from the one on the card. The page then shows two versions of the same number, both correct, with nothing marking which.

This is rare. It is also exactly the failure Step 21 spent a whole step making structurally impossible one layer down, and "rare" is the wrong standard for it — a hallucinated-looking figure that came from a real report is indistinguishable from one that didn't, which was the original argument.

**What is available today.** The narrative response already carries `report_hash` — `service.ReportHash(report)`, the content hash of the report it describes. The report response carries no hash, so the frontend has nothing to compare it against.

**Recommendation: add `report_hash` to `GET /insights/portfolio`, and have the frontend refuse to show prose whose hash does not match the report on screen.**

Implemented as a handler-level wrapper, not a new field on the hashed struct:

```go
// InsightsResponse is the report plus the identity of the report, so a
// separately-fetched narrative can be checked against the figures a caller
// is actually looking at rather than assumed to describe them.
//
// The hash is attached HERE rather than added to PortfolioInsights, because
// ReportHash marshals that struct: a field on it would hash itself, and
// every existing narrative cache key would change for a field that is not a
// measurement.
type InsightsResponse struct {
    service.PortfolioInsights
    ReportHash string `json:"report_hash"`
}
```

Embedded, so the JSON shape gains exactly one key and every existing consumer is unaffected. `ReportHash` is computed from the unmodified struct, so no cache key moves.

**Cost:** ~10 lines of Go, one handler test, and a scope line that says "frontend only" becoming "frontend plus one additive field". **Bought:** the page cannot display a figure and a sentence that disagree — not "is unlikely to", cannot. Given that the entire justification for Steps 20 and 21 being separate steps was to make that guarantee structural rather than probabilistic, spending ten lines to preserve it at the composition layer is the consistent call.

**The alternative** — accept the race, on the grounds that both requests fire within milliseconds and only this user's own trades can move the report — is defensible and cheaper. It is rejected because the failure is silent, and because §2.9 makes the window bigger than "milliseconds on mount": an order placed while the tab is open re-fetches the report and leaves the previously-fetched prose on screen describing the account as it was before the fill. That case is not rare at all, and the hash check is what catches it (§2.9).

### 2.4 Percent formatting: `toLocaleString`, not `toFixed`, and this is measurable

Step 21 §2.4 put every figure's formatting in Go and left a note for this step:

> Percentages have no counterpart there: `format.ts` has no percent formatter at all. This is therefore the convention, and Step 22 has to follow it rather than invent a second one.

The convention is: **one decimal place, halfway cases rounded away from zero**, via `roundHalfAway` (`services/ai-insights/internal/narrative/render.go`), which multiplies by the scale and calls `math.Round`. Signed percents carry an explicit `+` when positive.

`render.go`'s own comment says the browser's formatters all round away from zero and Step 22 can therefore "format a figure with the obvious one-liner". **That is true of the rule and false of one of the named functions**, and the difference is reachable. Measured against the Go implementation over 60,002 constructed decimal values in ±100 (every `X.X5` and every `X.XX5`) plus 20,000 random doubles:

| JS formatter | mismatches vs Go, 1 decimal place | mismatches vs Go, 2 decimal places |
|---|---|---|
| `toLocaleString('en-US', {min:1, max:1})` | **0 / 60,002** | 270 / 60,002 |
| `toFixed(1)` / `toFixed(2)` | **960 / 60,002** | 1,650 / 60,002 |

`toFixed` rounds the *exact binary value*: `-99.85` is really `-99.8499999999999943`, so it renders `-99.8` where Go's scale-then-round produces `-99.9`. `toLocaleString` rounds the shortest decimal representation with `halfExpand`, which is what Go's `v*scale` accidentally-but-reliably reproduces at one decimal place.

**Recommendation: `toLocaleString`, at one decimal place, for every percent.** Zero disagreement across every value tested, it is what `formatPrice`/`formatQuantity` already use, and it needs no port of Go's algorithm into TypeScript.

Two functions in `format.ts`, beside the existing three:

```ts
/** One decimal place. For percentages whose sign carries no meaning -- a
 * weight, a volatility. Matches KindPercent in the backend's renderer
 * (services/ai-insights/internal/narrative/render.go), which is the
 * convention because format.ts had no percent formatter when it was
 * written. toLocaleString, not toFixed: toFixed rounds the exact binary
 * value and disagrees with the backend on ~1.6% of one-decimal halfway
 * cases (SPEC.md 2.4). */
export function formatPercent(pct: number): string

/** Same, with an explicit "+" on positives. For percentages whose sign is
 * the point -- a return, an excess, a drawdown. Matches KindSignedPercent. */
export function formatSignedPercent(pct: number): string
```

The 2-decimal-place row is recorded rather than acted on. It affects `KindRatio` (Sharpe) and `KindMoney`, both of which reach the page through a 2dp `toLocaleString` — `formatPrice` already, and a Sharpe rendered the same way. Its 270 disagreements are all three-decimal constructed inputs (`-19.955`); the 20,000-random-double sweep found none at either precision, because a Sharpe computed from real arithmetic lands on an exact three-decimal boundary essentially never. §5 pins the tested cases so a future change to either side cannot move this quietly. It is a known, bounded, unexercised gap, not a silent one.

### 2.5 `insufficient_data` sections render their reason, and never a zero

Each of the three sections carries `state` (`ok` | `insufficient_data`) and, when degraded, a `reason` string. The figures are **not** `omitempty` — a degraded section serializes `max_drawdown_pct: 0`, and that zero is not a measurement.

Step 20 §2.5 made this explicit and the frontend must honour it: **branch on `state` before reading a single figure.** A degraded section renders its heading and its `reason` as prose, and no figure grid at all. Rendering `0.0%` there would be inventing a measurement — the same class of error as a model writing one, arrived at through carelessness instead.

The `reason` strings are backend-authored, user-safe, and read as sentences ("fewer than two trading days of history"). They are shown directly, like backtesting's `invalid_request` messages in Step 17 §2.6 and unlike trading's generic ones.

`behavior.risk_profile` is the one legitimately-`omitempty` field in the response (Step 20's own note: it is derived from the other two sections and is genuinely absent, not zero). Absent → the band is not rendered. Not "unknown", not a default.

### 2.6 Narrative states map to copy; the nine reasons collapse to four sentences

`NarrativeResponse.state` is `ok` or `unavailable`, with `reason` drawn from a closed set of nine. A user does not need nine sentences — but the nine do not collapse to one, because they are not all the same *kind* of fact and two of them are actionable.

| `reason` | Shown as |
|---|---|
| `no analysis is available to describe` | *(nothing — §2.5 has already explained why the sections are empty; a second explanation of the same emptiness is noise)* |
| `narrative generation is not configured` | "Written summaries are turned off." |
| `daily generation limit reached` | "You've reached today's summary limit. The figures above are unaffected." |
| `no usable narrative could be generated`, `the narrative service is unavailable`, `the narrative service is rate limited`, `narrative generation timed out`, `the model declined to describe this portfolio`, `narrative generation is unavailable without a generation counter` | "A written summary isn't available right now. The figures above are unaffected." + a retry control |

The "figures above are unaffected" clause is doing real work: this is the one place in the app where a visible failure sits directly beneath correct data, and the default reading of an error message is that something on the page is wrong.

The mapping is a pure function in its own module, tested — the direct analogue of `rejection-reason.ts` (Step 15) and `backtest-errors.ts` (Step 17). An unrecognised reason falls through to the generic sentence rather than rendering the raw backend string: the set is closed today, and a string added in a later step should degrade to safe copy rather than leak a phrase written for a log.

### 2.7 Report request errors — four codes, distinct copy

`writeServiceError` (`services/ai-insights/internal/handler/insights.go`) emits four codes. These arrive as the request's own HTTP error, not a `200` body field, so they are handled in the hook's catch:

| code | status | Copy |
|---|---|---|
| `symbol_unavailable` | 404 | The backend's message, shown directly — it names the symbol and the date, and was written to be read (Step 20 §2.10). |
| `invalid_token` | 401 | Not surfaced. The client's refresh-retry (`api/client.ts`) handles expiry; a 401 that survives it means the session is gone and `AuthBridge.onRefreshFailed` has already cleared it. |
| `upstream_unavailable` | 502 | "Portfolio or market data is unavailable right now, so no analysis was computed." |
| `internal_error` | 500 | "Something went wrong computing your analysis." |

Plus the two transport pseudo-codes every hook already handles (`timeout`, `network_error`, status 0).

### 2.8 Findings and the risk profile — a list with evidence, not a verdict

`behavior.findings` is `[]Finding`: `code` (`overtrading` | `panic_selling`), `severity` (`info` | `warn`), `detail` (backend prose, "must not be parsed"), plus a kind-specific figure and `evidence_trade_ids`.

Rendered as a list, severity as a token color (`warn` → `--color-down`, `info` → `--color-ink-muted`), `detail` as the text. **`detail` is displayed, never parsed** — the Go comment says so and this is the layer that would be tempted to.

`evidence_trade_ids` are **not rendered as links.** There is no trade-detail view to link to, and `OrdersTable` lists orders rather than trades, so a link would go nowhere. The count of them is not shown either — that would be a figure the frontend derived rather than one the backend computed, and `behavior.finding_count` exists in the placeholder vocabulary precisely because that distinction was taken seriously one step ago. They are carried in the response and used by the narrative; here they are ignored, deliberately, and this line is why.

`risk_profile` renders as a labelled band (`aggressive` | `moderate` | `conservative`), or not at all (§2.5).

### 2.9 An order placed while this tab is open invalidates the prose, and regeneration is a click

`OrderTicket` is pinned across every tab, so a fill can happen with the insights tab on screen. `onOrderPlaced` already refetches portfolio and orders. It should also refetch the **report** — the fill changes the account, and a stale report is a wrong report.

It should **not** auto-refetch the narrative. Generation is billed per call and capped at 50/day/user (`narrative.DailyGenerationCap`); the cache is keyed on the report's content hash, so a report that just changed is guaranteed to miss and guaranteed to cost. Refetching automatically would make every fill a paid API call for prose the user may not be reading.

So: the report refetches, its hash changes, §2.3's check fails, the prose is replaced by "These figures have changed since this summary was written" and a **"Write a new summary"** button.

> **AMENDED AT T11, by observation.** Two of this paragraph's premises are false against the
> running stack, and both were found in the browser rather than by reasoning.
>
> 1. **"The report refetches, its hash changes" — not for up to five minutes.** The refetch does
>    fire, exactly once per fill, as specified. But the backend serves the cached report from
>    `insights:{user_id}` (TTL 300s), so the identical report and hash come back. Observed: after a
>    real fill the panel still read "no trades yet", and only changed once the cache key was
>    deleted by hand. For that window the reader sees figures that predate their own trade, with
>    nothing marking them stale. Every hash-disagreement check in T11 required clearing the cache
>    first. The frontend cannot fix this; it has no way to demand a fresh report.
> 2. **The hash also changes when nothing has.** See the amendment at §9.2 — `ReportHash` is not
>    stable across recomputes of identical data, so this mechanism can also fire when no fill
>    happened at all, replacing correct prose with a staleness warning.
>
> The frontend behaviour specified here is correct and implemented. Both defects are in
> `services/ai-insights` and are recorded for a follow-up step. The first narrative of a session arrives automatically (§2.2); every subsequent generation is a deliberate click. Spend follows intent, and the one thing that is never shown is prose describing figures that are no longer on the page.

### 2.10 Test coverage — the same scoping, plus a parity table

Step 15 §2.10 introduced `vitest` scoped to logic-bearing units, asserted as pure functions rather than through rendering; Step 17 §2.10 continued it. Same scoping here. The logic-bearing units are:

1. `format.ts`'s two new percent formatters — including the §2.4 parity table, with expected values taken from the Go implementation, not from what JavaScript happens to print.
2. The narrative-reason → copy mapping of §2.6, including the unrecognised-reason fallthrough.
3. The report-error-code → copy mapping of §2.7.
4. The hash-agreement predicate of §2.3 — trivial as a comparison, worth pinning because its *degenerate* cases are the interesting ones: a report with no hash, a narrative with no hash (both possible: `report_hash` is `omitempty` on the narrative response), and whether either counts as agreement. **It must not.** Absent evidence of agreement is not agreement.

Component rendering stays untested, as in every prior frontend step. `@testing-library/react` is installed and still unused; this step does not change that, and does not remove it either.

---

## 3. Project structure

```
frontend/src/
  insights/
    InsightsPanel.tsx        # the tab's content: three sections, states, refetch (2.1, 2.2)
    RiskSection.tsx          # weights, HHI, volatility, drawdown (2.5)
    BenchmarkingSection.tsx  # portfolio return/Sharpe + benchmark rows (2.5)
    BehaviorSection.tsx      # trade count, findings, risk profile (2.8)
    NarrativeBlock.tsx       # one section's prose, or its absence explained (2.6)
    narrative-state.ts       # reason -> copy; hash agreement (2.6, 2.3)
    narrative-state.test.ts
    insights-errors.ts       # report error code -> copy (2.7)
    insights-errors.test.ts
    use-insights.ts          # GET /insights/portfolio, refetch (2.2)
    use-narrative.ts         # GET /insights/portfolio/narrative, manual regenerate (2.9)
  api/client.ts              # + insights(), narrative()
  api/types.ts               # + the two response shapes
  format.ts                  # + formatPercent, formatSignedPercent (2.4)
  format.test.ts             # + the parity table
  market/Dashboard.tsx       # + the sixth tab

services/ai-insights/internal/handler/
  insights.go                # + InsightsResponse wrapper (2.3)
  insights_test.go           # + the hash-is-present-and-correct case
```

## 4. Configuration

None. No new environment variable, no new dependency in `package.json`, no `.env.example` change. The narrative service's own configuration (`ANTHROPIC_API_KEY`, `REDIS_URL`) is unchanged and its absence is already a supported state that §2.6 renders.

## 5. Testing strategy

- **Frontend:** `vitest`, colocated `*.test.ts`, scoped per §2.10. Run with `npm test` in `frontend/`.
- **Parity:** the §2.4 table is checked into `format.test.ts` as explicit cases with Go-derived expectations. The generator script that produced the sweep is not checked in — it is a one-off measurement, and a checked-in script that nobody runs is worse than a recorded number that a test enforces.
- **Backend:** one handler test for §2.3 asserting the response carries `report_hash` and that it equals `service.ReportHash` of the same report — the property that makes the frontend's comparison meaningful. `make test` for `ai-insights`; `make test-integration` unaffected (no new `integration/` package, so `docs/TESTING_STRUCTURE.md` §6a's extraction trigger stays unfired for a fourth step — to be re-confirmed, not assumed, at documentation time).
- **Manual:** the tab driven against a real account for each of: an `ok` report with prose, a degraded report (`insufficient_data`), a narrative `unavailable` (unset `ANTHROPIC_API_KEY`), and a hash mismatch (forced by placing an order with the tab open). Every figure on the card checked by eye against the same figure in the sentence beside it — which is the §2.4 measurement's real acceptance test.

## 6. Code style

WHY-only comments, as everywhere in this repo — the comment explains the decision, not the mechanism. Design tokens from `index.css`, never raw hex. Status-union hooks (`{status: 'loading' | 'ok' | 'error'}`), never a bare boolean pair.

```tsx
// Branch on state before touching a figure. A degraded section serializes
// max_drawdown_pct: 0 because Step 20 deliberately left the figures without
// omitempty (Step 20 SPEC.md 2.5) -- so reading one here without checking
// would print a measurement nothing measured.
if (risk.state !== 'ok') {
  return <SectionNotice heading="Risk" reason={risk.reason} />
}
```

## 7. Commands

```
Frontend dev:    cd frontend && npm run dev            # :5173
Frontend build:  cd frontend && npm run build          # tsc -b && vite build
Frontend test:   cd frontend && npm test               # vitest run
Frontend lint:   cd frontend && npm run lint           # oxlint
Backend vet:     make vet
Backend test:    make test                             # all seven modules
Integration:     make test-integration                 # expect 63/0, unchanged
Dockerization:   GOWORK=off go build ./...             # per module, must stay green
Services:        make docker-up && make run-{auth,market-data,trading-engine,backtesting,ai-insights,gateway}
```

## Boundaries

- **Always:** run `npm test`, `npm run build` and `make test` before a commit; branch on section `state` before reading a figure; format percentages through `format.ts` and nowhere else; keep the branch as `step22-insights-frontend`, squashed to one `feat(step22)` commit and merged `--no-ff`.
- **Ask first:** any change to `services/ai-insights` beyond §2.3's wrapper; any new npm dependency; any change to a threshold, band, or the prompt; adding a router.
- **Never:** compute a figure client-side that the report does not carry; render a figure from a section whose `state` is not `ok`; parse `Finding.detail`; show prose whose `report_hash` does not match the report on screen; auto-trigger a billed generation on anything but the first load.

## 8. Success criteria

1. A sixth tab renders all three report sections with every figure the report carries, and no figure it does not.
2. A degraded section shows its `reason` and zero figures.
3. The prose appears beneath its section after the figures, and its absence — in all nine backend reasons — leaves the figures untouched and explained per §2.6.
4. Every percent on the card is character-identical to the same percent in the sentence beside it, verified by eye across a real report and pinned by the §2.4 parity table.
5. Prose whose `report_hash` disagrees with the displayed report is never shown; the regenerate control is.
6. `npm test`, `npm run build`, `npm run lint` clean; `make vet`/`make test` green across all seven modules; `make test-integration` 63/0; `GOWORK=off go build ./...` green per module.
7. `docs/PHASE4_CHECKLIST.md`, `docs/NEXT_SESSION.md` and `agents.md` updated; spec, plan and todo archived to `docs/archive/phase4-step22-insights-frontend/`; root `SPEC.md` and `tasks/` not carried onto `main`.

## 9. Open questions for review

**9.1 — Is §2.3's backend field in scope?** It breaks the "frontend only" framing for ten lines of Go. **Recommendation: yes.** The alternative leaves a silent wrong-number path in the one step whose entire premise was closing wrong-number paths, and §2.9 makes it reachable in ordinary use rather than only in a race.

**9.2 — Should the first narrative load be automatic at all?** It spends money without a click. **Recommendation: yes, automatic on first tab open.** The 24-hour content-hash cache means opening the tab repeatedly on an unchanged account costs one generation per day, the cap bounds the worst case at roughly a dollar, and a tab that shows nothing until you press a button is a feature most users would never find. Every *subsequent* generation is a click (§2.9).

> **AMENDED AT T11, by measurement.** The cost argument above rests on the content hash being
> stable for an unchanged account. **It is not.** Twelve recomputes of one untouched account
> produced **six distinct hashes**. The displayed figures were identical; the drift is in the last
> floating-point digits of `portfolio_sharpe`, `annualized_volatility_pct` and
> `concentration_hhi`. Ordering is not the cause — positions and benchmarks return in a stable
> order and `concentrationHHI` is a sequential loop — and `ReportHash` correctly zeroes
> `ComputedAt`. The drift is upstream in the reconstruction, which fetches price histories
> concurrently through an `errgroup` in `histories.go`; float addition is not associative, so a
> different completion order moves the last digit.
>
> Since the narrative cache key is `narrative:{user_id}:{report_hash}`, a new hash is a cache miss
> and a cache miss is a billable generation. **"One generation per day for an unchanged account"
> is wrong** — roughly half of recomputes mint a new key. The five-minute report cache is the only
> brake, so sustained viewing can cost about one generation per five minutes, bounded only by the
> 50/day cap. The recommendation to load automatically still stands; the cost bound quoted for it
> does not.
>
> The same instability makes §2.3's check fire in the false-positive direction: two independent
> computations of identical data can disagree, so correct prose is occasionally replaced by
> "These figures have changed since this summary was written". `describesReport` failing closed on
> two disagreeing hashes remains correct. The fix belongs in `services/ai-insights` — make the
> reconstruction deterministic, or round each figure to its published precision before hashing so
> the hash reflects what is actually shown.

**9.3 — Does the benchmarking section render each benchmark's Sharpe?** It is a 2-decimal figure and therefore in §2.4's unexercised gap. **Recommendation: yes, render it.** The gap is theoretical for values from real arithmetic (0 mismatches in 20,000 random doubles) and suppressing a real measurement to avoid a rounding disagreement that no observed value produces would be the wrong trade. It is recorded here so a future change knows what it is disturbing.

**9.4 — Where does `as_of_date` go?** Both responses carry it and it is the one piece of context that makes every figure interpretable ("as of when?"). **Recommendation: once, in the panel header, from the report — not per-section, and not from the narrative.** The report is the source; the narrative's copy is only there to identify what it described.
