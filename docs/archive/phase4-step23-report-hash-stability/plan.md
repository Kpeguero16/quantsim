# Plan — ReportHash stability (Step 23)

Branch `step23-report-hash-stability`, cut from `main` at `2e3cf12`.

Step 23 is small enough that a twelve-task breakdown would cost more than it returns. The change is two loops and a helper. What actually carried risk here was the diagnosis, not the edit, so the plan is weighted that way: four tasks, one checkpoint.

---

## T1 — Locate the drift and measure it

Find every nondeterministic float accumulation reachable from `PortfolioInsights`, prove which ones move the hash, and reject the ones that only look like they do.

Acceptance: a reproduction that shows distinct hashes over recomputes of identical input, plus a stated cause for each candidate ruled in or out.

## T2 — Fix it

Sort both accumulations by symbol. Correct the trade fold comment, which claims a bit-level guarantee it does not have (§4.3).

Acceptance: the reproduction collapses to one hash. `go vet` clean, package tests green.

## T3 — Tests that fail against the unfixed code

Three, per SPEC §6: reconstruction bit stability, risk weight bit stability, end-to-end hash stability. Each confirmed to fail with the fix reverted, restoring from a file copy rather than `git checkout --`.

Acceptance: all three fail unfixed and pass fixed, and the reconstruction test passes while the risk test still fails when only `risk.go` is reverted. That last part is what shows each test owns its own fault instead of all three riding the same one.

## T4 — Adversarial pass

Mutate the fix. Confirm the intended survivors are intended. Check the simplification question: does anything in the change earn its complexity.

Acceptance: every survivor explained in SPEC §6.1, not merely listed.

## Checkpoint A — Runtime evidence

Everything above is unit level. Confirm two HTTP requests against the running service return the same `report_hash` for an unchanged account, with the report cache cleared between them so the second genuinely recomputes. Needs a seeded, reconciling trade history and a restore afterwards, verified by query.

---

## Not in this step

Report cache invalidation on a fill (Step 22 defect 2). It is next and it is a different service boundary. SPEC §7.
