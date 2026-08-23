# Todo — ReportHash stability (Step 23)

Tracks `tasks/plan.md`'s 4 tasks and 1 checkpoint. **All done. What remains is documentation and the merge.**

Branch `step23-report-hash-stability`, cut from `main` at `2e3cf12`. **One commit, `4a5623b`, nothing pushed.** Root `SPEC.md` and `tasks/` stay untracked as always.

---

## State of the machine

**Everything is put back.** Services stopped, database at baseline and verified by query: `users=20 accounts=20 trades=0 orders=0 positions=0`, `historical_prices=3525`. Postgres and Redis containers up. No `insights:*` or `narrative:*` keys.

`historical_prices` is **3525, not the 3507** the previous session recorded. market-data ingested more bars since. Nothing to do with this step, but the next person comparing against the old number should not read it as damage.

**This step cost nothing.** The narrative endpoint was never called and no `narrative:*` key ever existed.

---

## T1 — Locate the drift and measure it. Done.

Two nondeterministic float accumulations, both running in Go map iteration order:

- `reconstruct.go`, the equity loop. `equity += qty * px` over `range holdings`, once per date, so a 72-day window compounds it.
- `risk.go`, the invested loop. `invested += value` over `range r.Holdings`, which sets every weight and `concentration_hhi`.

Ruled out, each for a stated reason rather than by inspection alone:

- `FetchHistories` is concurrent but lands results in index slots, never completion order. Deterministic by construction, and its comment already says so.
- `Calendar` and `sortedUnion` both sort before returning.
- `closesBySymbolAndDay` writes each symbol into its own submap, so the outer map order cannot change what wins.
- The trade fold is order-sensitive at the bit level, but not a drift source. Its order comes from `GET /trading/trades`, whose `ORDER BY` breaks ties on the row UUID, and those are fixed per row. Arbitrary, but the same every time. SPEC §4.3.

Measurements, all against unfixed code:

| | |
|---|---|
| Map iteration orders, 7 key map, 500 passes | **7 distinct.** Go picks a random start offset and walks one bucket, so these are rotations, not permutations. |
| Dates whose equity sum moves under those rotations | **52% at 3 positions, 77% at 5, 89% at 7** |
| `Reconstruct`, 200 runs on identical input | **199 distinct equity curves** |
| `PortfolioInsights`, 12 recomputes | **11 distinct hashes** |

**The first fixture I built showed zero drift over 200 runs and nearly sent me down the wrong path.** Whole-dollar closes are exact in binary, and seven rotations of seven exact values agreed. Rounding the closes to cents took it from 1 distinct curve to 199. This is now written into `driftBars`' comment, because the next person to write a stability test here will reach for `bars()` and get a green test against broken code.

## T2 — Fix it. Done.

`sortedSymbols(m map[string]float64) []string` in `reconstruct.go`; both loops iterate it. Four lines of behavior change.

The trade fold comment was rewritten. It claimed "any permutation of one day's trades reaches the same state", which is true of the figures and false of the bits. It now says which, and says the stability is borrowed from trading-engine's query rather than intrinsic here.

## T3 — Tests. Done.

| Test | Owns | Unfixed |
|---|---|---|
| `TestReconstruct_IsBitStableAcrossRuns` | the equity loop | 199 distinct curves over 200 runs |
| `TestComputeRisk_WeightsAreBitStableAcrossRuns` | the invested loop | fails |
| `TestReportHash_IsStableAcrossRecomputes` | the defect as reported | 11 distinct hashes over 12 |

Reverting only `risk.go` leaves the reconstruction test passing and the other two failing, which is the check that they are not all riding one fault.

`TestReportHash_IsStableAcrossCalls` already existed, passed throughout, and hashes one struct value twice. It is a purity check whose name oversells it. Left alone apart from a comment pointing at the test that owns the real property.

`TestReconstruct_SameTimestampTradesAreOrderInsensitive` keeps `eps = 1e-9`. Tightening it to bit equality would assert §4.3's fold insensitivity, which is not true.

## T4 — Adversarial pass. Done.

Five mutants. Three killed by the new tests, one killed by eight existing tests, one intentional survivor. Full table in SPEC §6.1.

The survivor worth stating: reversing the sort order survives, and should. §4.2 asks for reproducibility, not a specific order.

**The pass also removed something.** The first draft threaded a reused scratch slice through `sortedSymbols` to avoid a per-date allocation. Benchmarked, it saved 23 allocations and 1.8 KB per reconstruction and cost nothing in time (24765 vs 24727 ns/op, noise). It also produced an equivalent mutant, since the scratch was length zero and never reassigned, making `scratch[:0]` and `scratch` the same slice. Removed.

## Checkpoint A — Runtime evidence. Done.

Seeded a throwaway user rather than touching any of the 20 existing ones: three buys (AAPL, MSFT, GOOGL) in April 2026, orders, trades, positions and balance inserted in one transaction so the account reconciles. 79 trading day window, full `ok` report.

`GET /insights/portfolio` with `insights:{user_id}` deleted between every call:

| Binary | Recomputes | Distinct hashes |
|---|---|---|
| Unfixed | 10 | **9** |
| Fixed | 12 | **1** |

Full detail in SPEC §6.2, including the part where a three-position account hid the `risk.go` fault entirely while the equity loop drifted in the open.

**Two traps hit, both already written down in `NEXT_SESSION.md`, both worth re-reading before the next live check.**

1. **A `go run` service keeps serving the old binary.** Restoring the fix, killing, and restarting produced 11 distinct hashes out of 12, which reads as the fix not working. It was not the fix. `pkill -f "ai-insights/cmd/server"` matched nothing, because `go run` compiles to a temp binary whose process is just `server`. The old process kept 8085, the new one died with `bind: address already in use`, and the health check happily returned 200 from the wrong build. **Check the log says `listening` and that the PID on the port is new. A passing health check proves a server is there, not that it is yours.**
2. **`pkill -f` patterns reach further than intended.** An earlier `pkill -f "exe/server"` could have taken the three sibling services with it. It did not, but only luck decided that. Killing by PID from `lsof -t` is the version that does what it says.

**First seed attempt was wrong and the database said so.** Quantities sized at roughly 133k against a 100k account produced a balance of -33349.13. A negative cash position is not an error the report refuses, it just makes weights exceed 100% and tests something nobody ships. Re-seeded at about 66% invested.

---

## Verification so far

| | |
|---|---|
| `make vet` | clean |
| `make test` | green, all seven modules, 0 failures |
| `make test-integration` | **63/0**, unchanged from Step 22 |
| `GOWORK=off go build ./...` | passes for all seven modules |
| Mutations | 5 run, 4 killed, 1 intentional survivor |
| Live stack | **9 distinct hashes over 10 unfixed recomputes, 1 over 12 fixed.** SPEC §6.2 |
| Cost | **$0.00.** The narrative endpoint was never called. |
