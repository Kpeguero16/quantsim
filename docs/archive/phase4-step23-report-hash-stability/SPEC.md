# SPEC — ReportHash stability (Step 23)

Status: **Implemented, verified against the running stack, ready for docs and merge.** §6.2 carries the live evidence. §3 is the root cause with numbers behind it. §4.1 is the one decision worth arguing about, and it goes against what `docs/NEXT_SESSION.md` proposed.

Scope: `services/ai-insights/internal/service/` only. Two accumulation loops, one new helper, and the tests that hold them in place. No migration, no new table, no API change, no gateway change, no frontend change. **No figure changes value** at any precision a reader can see.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase4-step22-insights-frontend/`.

---

## 1. Objective

Step 22 left three defects. This closes the one it called "the one that matters most": `ReportHash` is not stable for unchanged data. Twelve recomputes of one untouched account produced six distinct hashes.

The hash is not decoration. `narrative:{user_id}:{report_hash}` is the narrative cache key, so an unstable hash means a cache that never hits, and every miss is a billed Claude generation for a report nothing changed. It is also the input to the frontend check added in Step 22 §2.3, which compares the hash on the report against the hash the narrative was generated from and tells the reader their prose may be describing different figures. An unstable hash makes that warning fire against itself.

So the defect costs money on every view and cries wolf on the page while doing it.

**Objective:** make two reports computed from identical data identical, byte for byte, and prove it with a test that fails against today's code.

**Non-goals:**

- **Changing any published figure.** The fix moves values by at most one unit in the last place of a float64. Nothing renders differently at 1, 2, 3 or 4 decimals. If a figure reads badly on the page, that is still a separate finding for a later step.
- **The report cache invalidation defect.** Step 22's defect 2, where a fill's refetch is defeated by the five minute report cache. It is real, it is next, and it is a different service boundary with its own design question about who invalidates. §7.
- **Rounding the report before hashing.** §4.1 rejects it, and that is a reversal of what `NEXT_SESSION.md` recommended.
- **Any change to thresholds, bands, finding rules, or the prompt.**
- **Making the trade fold order-insensitive at the bit level.** §4.3.

---

## 2. What the hash is, and why bit equality is the bar

`ReportHash` is `sha256(json.Marshal(report))[:16]` with `ComputedAt` zeroed (`internal/service/hash.go:36`). It hashes the serialized struct, so it is defined on the exact decimal expansion of every float64 in the report. `12.340000000000001` and `12.34` are the same figure to every reader and different reports to the hash.

That is the correct design. `hash.go`'s comment argues it well: excluding one field by zeroing it, rather than listing fields to include, means a measurement added in a later step participates by default, and forgetting one cannot quietly serve stale prose. Nothing here changes that.

It does mean the hash inherits every last-bit difference the computation produces. Bit equality for identical input is therefore a precondition the computation owes the hash, and until now it did not pay it.

---

## 3. Root cause

Two float64 accumulations run in Go map iteration order.

**`reconstruct.go`, the equity loop.** For each date, `equity := cash` and then `equity += qty * px` over `range holdings`.

**`risk.go`, the invested loop.** `invested += value` over `range r.Holdings`, which sets every position weight and `concentration_hhi`.

Go randomizes map iteration order per pass. Float64 addition is not associative. Summing the same holdings at the same closes in a different order gives a result that differs in the last bits.

Three measurements, all against the unfixed code:

| Measurement | Result |
|---|---|
| Map iteration orders for a 7 key map, 500 passes | **7 distinct**, not 5040. Go picks a random start offset and walks one bucket, so the orders are rotations of a fixed sequence, not permutations. |
| Dates whose equity sum changes under those rotations, realistic values | **52% at 3 positions, 77% at 5, 89% at 7.** Prices rounded to cents, quantities to NUMERIC(20,4), cash near 90k. |
| `Reconstruct` over identical input, 200 runs | **199 distinct equity curves.** |
| `PortfolioInsights` over identical input, 12 recomputes | **11 distinct hashes.** |

The last line is the reported defect reproduced, slightly worse than the six-of-twelve seen against the dev account because the fixture holds five positions over twenty-five days.

Two things are worth recording about how this hid for three steps.

**The existing order test asserts a tolerance the hash does not have.** `TestReconstruct_SameTimestampTradesAreOrderInsensitive` compares through `assertFloats`, which passes at `eps = 1e-9`. The drift is around 1e-11 of a value near 1e5. The test is not wrong about what it checks. It checks the wrong thing for this fault.

**The codebase already had the instinct, aimed one step short.** `calendar.go:76` says dates are "sorted explicitly, never left to map iteration order, which Go randomizes per run". `risk.go:99` says the same about position order. Both are about ordering a result someone reads. Neither anticipated that an accumulation nobody reads needs the same discipline for a different reason.

The rotation detail also explains why the first fixture I tried showed zero drift across 200 runs. Seven rotations of seven values agreed by luck. Realistic cent-rounded prices do not.

---

## 4. Design decisions

### 4.1 Fix the computation, do not round the hash

`NEXT_SESSION.md` proposed rounding each figure to its published precision before hashing, and called it the smaller change that closes both symptoms. Having measured the thing, I disagree, and the argument is not close.

Published precision is per `Kind`, set in `narrative/render.go`: percent 1 decimal, ratio 2, HHI 3, money 2, quantity 4, count 0. Rounding before hashing needs every float field in `PortfolioInsights` mapped to one of those. That map is an include-list, and `hash.go` spends a paragraph explaining why this struct is hashed by exclusion instead. Rebuilding the include-list one layer up reintroduces the failure the comment describes, and it puts a third copy of the precision rule in the tree beside `narrative/render.go` and `frontend/src/format.ts`, which Step 22 already had to keep in character-for-character agreement.

Sorting the accumulation is four lines and one helper. It removes the cause rather than the symptom, and it fixes the figures too. Rounding the hash would leave `GET /insights/portfolio` returning a `portfolio_sharpe` that differs between two calls on unchanged data. Any future consumer that compares two reports, and the §2.3 check is already one, would meet the same defect again with the hash no longer able to report it.

What rounding would buy, and what this gives up: two reports differing only below display precision would share cached prose instead of billing a new generation. After this fix that requires the underlying data to genuinely change by a sub-display amount, which a fill or a new bar does not do. If §2.3 false alarms ever show up in practice, rounding becomes the follow-up, and it will be a smaller change then because the noise floor will be gone.

### 4.2 Sort the symbols, not the values

Sorting keys is not the only way to make a float sum deterministic. Sorting by magnitude, or Kahan summation, would also reduce the error. Both are wrong here.

The requirement is reproducibility, not accuracy. Two runs must agree with each other. Neither is obliged to agree with the exact decimal sum, and no figure in this report is sensitive enough at display precision for the difference to matter. Symbol order is already the ordering the rest of the package reaches for, it costs one `sort.Strings`, and it is obvious at the call site. Magnitude sorting would change values relative to today for no visible gain, and Kahan summation adds a compensated accumulator to code whose problem was never precision.

### 4.3 The trade fold stays as it is

`Reconstruct` folds trades in `sort.SliceStable` order by day, so trades sharing a day are folded in the order the caller supplied. Cash is accumulated across that fold, and float addition is no more associative there than in the equity loop, so a different within-day order gives a different last bit.

`reconstruct.go`'s comment claims the fold is insensitive to this: "cash and holding deltas are additive, so any permutation of one day's trades reaches the same state". At the bit level that is false, and the comment should say what is actually true.

It is not a drift source, which is why it is not being changed. The order comes from `GET /trading/trades`, whose `ORDER BY` breaks ties on the row's UUID. Those UUIDs are fixed per row, so the order is arbitrary but stable, and the same set of trades comes back the same way every time. Sorting the fold by trade ID would make the guarantee intrinsic instead of borrowed. That is a real improvement and it is not this step, because it changes every existing hash for no defect anyone has hit.

The comment gets corrected. The behavior does not.

---

## 5. The change

`internal/service/reconstruct.go`

- New `sortedSymbols(m map[string]float64) []string`, returning the keys sorted. The comment carries the measurement and states why this differs from the sorts in `Calendar` and `ComputeRisk`.
- The equity loop iterates `sortedSymbols(holdings)` and reads `holdings[symbol]`.
- The trade fold comment is corrected per §4.3.

`internal/service/risk.go`

- The invested loop iterates `sortedSymbols(r.Holdings)` and reads `r.Holdings[symbol]`.

The first draft threaded a reused scratch slice through `sortedSymbols` to avoid allocating per date. Benchmarked over the §6 fixture, that saved 23 allocations and 1.8 KB per reconstruction and cost nothing measurable in time: 24765 ns/op with it against 24727 ns/op without, which is noise. It also produced an equivalent mutant, because the scratch was created at length zero and never reassigned, so `scratch[:0]` and `scratch` were always the same slice. Removed. A per-date allocation of at most ten strings is not worth the subtlety.

Neither loop changes which symbols it visits or which errors it can return. `applyTrade` deletes dust positions, so the key set is exactly the held symbols either way.

---

## 6. Tests

Three, and each owns a fault the others miss.

1. **`TestReconstruct_IsBitStableAcrossRuns`** in `reconstruct_test.go`. Identical input, many runs, equity compared through `math.Float64bits`. The fixture must use cent-rounded prices and four-decimal quantities, because round fixture numbers are rotation-stable and the test would pass against the unfixed code. Verified to fail without the fix: 199 distinct curves over 200 runs.

2. **`TestReportHash_IsStableAcrossRecomputes`** in `hash_test.go`, driving `PortfolioInsights` through the mock clients with a fresh cache per run so each call recomputes. This is the defect as reported. Verified to fail without the fix: 11 distinct hashes over 12 recomputes.

3. **`TestComputeRisk_WeightsAreBitStableAcrossRuns`** in `risk_test.go`, covering the `invested` loop on its own. Without it, `risk.go` is held only through the end-to-end hash test, and a mutation that unsorts it would be caught only indirectly.

`TestReconstruct_SameTimestampTradesAreOrderInsensitive` keeps its `eps` comparison, and gains a comment saying why the tolerance is deliberate and which test owns bit equality. Tightening it would assert §4.3's fold insensitivity, which is not true and is not being made true.

`TestReportHash_IsStableAcrossCalls` already existed and passed against the unfixed code throughout. It hashes one struct value twice, so it is a purity check and its name overpromises. It keeps the assertion and gains a comment saying which test owns the property it sounds like it covers.

Both new stability tests must be confirmed to fail against the unfixed code by restoring from a copy of the pre-fix file, never `git checkout --`, which discards uncommitted work in the same file. That happened twice in Step 22.

### 6.1 Mutation results

Five mutants, run against the three stability tests.

| Mutant | Result |
|---|---|
| Drop `sort.Strings` in `sortedSymbols` | killed |
| `risk.go` back to `range r.Holdings` | killed |
| Sort by string length instead of value | killed, because equal-length symbols keep map order under a stable sort |
| Reverse the sort order | **survived, by design.** §4.2 asks for reproducibility, not a particular order, and a reverse sort is equally reproducible. A test that killed this would be asserting something the spec does not require. |
| `equity += px`, dropping the quantity | survived the three stability tests, killed by eight existing tests in the same package |

The last two are the useful ones. They confirm the division of labor: the new tests own determinism and nothing else, and the existing suite still owns whether the arithmetic is right. Neither set catches the other's faults, which is the property Step 22 wrote down for `format.test.ts` and `parity.live.test.ts`.

### 6.2 Live evidence

Everything above is in-process. Against the running stack, on a seeded three-position account over a 79 trading day window, requesting `GET /insights/portfolio` with `insights:{user_id}` deleted between each call so every one recomputes:

| Binary | Recomputes | Distinct hashes |
|---|---|---|
| Unfixed | 10 | **9** |
| Fixed | 12 | **1** |

The drift is visible in the response without computing anything: `portfolio_sharpe` came back as `1.9858623576388137`, `...146`, `...132`, `...093` across the unfixed runs, and as `1.9858623576388097` on all twelve fixed ones.

One detail worth keeping. `weight_pct` and `concentration_hhi` were identical across all ten unfixed runs. Three positions happened to give a rotation-stable `invested` sum, so this account would have hidden the `risk.go` fault completely while the equity loop drifted in plain sight. That is the argument for `TestComputeRisk_WeightsAreBitStableAcrossRuns` existing separately, made by the data rather than by reasoning.

The narrative endpoint was never called, so this cost nothing. No `narrative:*` key existed at any point.

---

## 7. Open

**Report cache invalidation on a fill.** Step 22's defect 2, unchanged by this step and now the most consequential thing left in Phase 4. A fill's report refetch is defeated by the five minute `insights:{user_id}` cache, so for up to five minutes the reader sees figures that predate their own trade, unmarked. It wants its own step because the design question is a service boundary: `trading-engine` invalidating a key it does not own, or `ai-insights` learning that a trade happened. Neither is obviously right.

Worth noting the interaction. Until it is fixed, a fill produces exactly the disagreement §2.3 was built to catch, and after this step that warning finally means something.

**Whether `report_hash` should be stable across process restarts and machines.** Restarts are now answered by accident: two separately started `ai-insights` processes produced `7173ed8958abb848` for the same account, which is what `sha256` over `encoding/json` with no per-process seed should do. Across machines it is untested, and nothing depends on it in writing. If the service is ever sharded or the cache shared between builds, that becomes a real requirement and wants writing down rather than rediscovering.
