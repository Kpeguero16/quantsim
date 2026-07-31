# Implementation Plan — QuantSim Identity Lookup Consistency (Step 10)

## Overview

Close the four findings from Step 9's pre-merge review. `SPEC.md` is **approved**; §9 records the resolutions, all taken as recommended.

The step is small — one query, one migration, one test, one documentation note — and the plan is sized to match. The reason it exists as its own step rather than a patch commit is §9's sequencing decision: **Step 10 lands before Step 9 is merged**, so `main` never receives the latent gap.

Only Finding 1 has a plausible failure behind it, and it is not a live bug today. `Login` normalises the email to lowercase; the store then matches it exactly. That works solely because every stored email happens to be lowercase — migration `004` lowercased the existing rows, and `service.Register` (verified: the only write path) normalises every new one. Migration `004` constrained *uniqueness* under `lower(email)` but never made canonical storage structural: the index stops a second row colliding with `Foo@x.test`, it does not stop `Foo@x.test` existing. Such a user could never log in, and the failure would be a silent `401`.

## Architecture decisions

Restated from `SPEC.md` §2 for reference while implementing:

- **Look users up by `lower(email)`** — the lookup matches the constraint that exists — §2.1
- **Drop `users_email_key` and `users_username_key`** in migration `005`; both are implied by the `lower()` unique indexes — §2.2
- **No `CHECK (email = lower(email))`.** The stricter option, considered and rejected: once lookup is case-insensitive it guarantees nothing anything depends on, and SQLSTATE `23514` is unmapped by the store, so it would surface as a **500** — §2.3
- **Usernames need no lookup change.** Verified: nothing anywhere looks a user up by username — §2.4
- **`InvalidInputMessage` gets a direct table test**, and stays narrow — §2.5, §9
- **The `CONCURRENTLY` trade is documented, not taken** — §2.6
- **`005.down` is a complete rollback**, unlike `004.down` — §2.7

## Dependency graph

```
Task 1 (GetUserByEmail -> lower(email))   ← the fix; unblocks the drop
    │
    └── Task 2 (migration 005, drops both redundant constraints)
            │
            └── Task 3 (InvalidInputMessage test, tuning note, close out)
```

Task 1 is first because Task 2 is unsafe before it: dropping `users_email_key` while an exact-match query is live turns every login into a sequential scan. On 15 rows that is unobservable, which is precisely why the ordering has to be structural rather than remembered.

Task 3 is genuinely independent of both and is last only because it is close-out work.

---

# Phase 1: The fix

## Task 1: `GetUserByEmail` matches on `lower(email)`

**Description:** One query. The lookup stops depending on the stored form being canonical and starts matching the constraint the database actually has.

**Acceptance criteria:**
- [ ] `user_store.go` `GetUserByEmail` uses `WHERE lower(email) = $1`
- [ ] `Login` still calls `NormalizeEmail` first — that is what makes the bound parameter canonical, not a duplicate of the rule (§2.1)
- [ ] No other query changed; `GetUserByID` untouched

**Verification:**
- [ ] `cd services/auth && go test -count=1 ./...` — proves no regression, and **cannot** prove the fix (§6: the mock store is a Go map)
- [ ] **The check that actually matters, and it must be done by hand.** Insert a non-canonical row directly, with a *real* bcrypt hash of a known password — generated with a throwaway Go program against the existing `golang.org/x/crypto` dependency, not a placeholder string:
      - Before the change: `SELECT ... WHERE email = 'noncanon@quantsim.test'` returns **0 rows**, and login returns **401**
      - After: login as `noncanon@quantsim.test` returns **200**
      - This is stronger than `SPEC.md` §3's sketch, which used a bogus hash and so could only prove the row was *found*, not that authentication completes
- [ ] `EXPLAIN` confirms `idx_users_email_lower` is used
- [ ] `khalil-ui-check@quantsim.test` still logs in with its 10-character password, in both capitalisations
- [ ] The seeded row is deleted afterwards; user count back to 15

**Dependencies:** None

**Files likely touched:**
- `services/auth/internal/store/user_store.go`

**Estimated scope:** Extra small (one line, and a verification that is larger than the change)

---

## ✅ Checkpoint: Lookup no longer depends on stored form (after Task 1)

- [ ] A deliberately non-canonical row can be authenticated against
- [ ] Every existing user still logs in; `khalil-ui-check` verified directly
- [ ] `go test -count=1 ./...` green in `services/auth` and `pkg`
- [ ] Test row removed; database back to 15 users
- [ ] **Stop for architect review before the migration**

---

# Phase 2: The schema

## Task 2: Migration `005` — drop the redundant `UNIQUE` constraints

**Description:** Remove `users_email_key` and `users_username_key`, both fully implied by the `lower()` unique indexes since `004`. Four unique index maintenances per insert become two.

**Acceptance criteria:**
- [ ] `up` drops both **by constraint name** (`ALTER TABLE users DROP CONSTRAINT ...`), not as bare indexes — they were created by `CREATE TABLE ... UNIQUE` (§5)
- [ ] `down` re-adds both; this rollback is complete, and the file says so in contrast to `004.down` (§2.7)
- [ ] A comment records that this migration **assumes `004`'s indexes exist** — dropping these constraints without them would leave `users` with no email uniqueness at all
- [ ] Exactly three indexes remain: `users_pkey`, `idx_users_email_lower`, `idx_users_username_lower`

**Verification:**
- [ ] Dry-run `up` **and** `down` on a throwaway database first, same as `004`
- [ ] On the dry-run database, confirm `down` succeeds **against real rows** — re-adding a `UNIQUE` constraint validates existing data, and this is the direction that could fail
- [ ] After `up` on the real database: a case-duplicate registration still returns `409`, and an exact-duplicate registration still returns `409` — the second is the one this task could plausibly break
- [ ] `khalil-ui-check@quantsim.test` still logs in
- [ ] `EXPLAIN` still shows an index scan, not a sequential scan

**Dependencies:** Task 1 — **hard**, not stylistic. See the dependency graph.

**Files likely touched:**
- `infra/migrations/005_drop_redundant_unique_constraints.up.sql` / `.down.sql`

**Estimated scope:** Small (highest-risk task in the step, which is a low bar here)

---

## ✅ Checkpoint: Schema carries only what it needs (after Task 2)

- [ ] Three indexes on `users`, each earning its place
- [ ] Duplicate registration rejected in both exact and case-differing forms
- [ ] All logins still work
- [ ] **Stop for architect review**

---

# Phase 3: Close out

## Task 3: `InvalidInputMessage` test, tuning note, and step close-out

**Description:** The two findings with no runtime risk, plus closing the step.

**Acceptance criteria:**
- [ ] `errors_test.go` (external, `package service_test`) covers all three behaviours: a wrapped `ErrInvalidInput` yields the message alone; `nil` yields `""`; an unrelated error is returned in full because `TrimPrefix` is a no-op (§2.5)
- [ ] `docs/deferred-tuning.md` records the `CREATE UNIQUE INDEX` lock and the `CONCURRENTLY` / `-- no-transaction` trade — including that taking it forfeits the all-or-nothing rollback `004`'s dry run relied on (§2.6)
- [ ] The note lands in `deferred-tuning.md`, **not** `security-backlog.md` — it is not a security gap
- [ ] Step 10 recorded in `PHASE1_CHECKLIST.md` alongside Step 9's close-out

**Verification:**
- [ ] `cd services/auth && go test -count=1 ./...`
- [ ] The new test fails if `InvalidInputMessage` is reduced to `err.Error()` — assert it by mutation, not by assumption

**Dependencies:** Task 2 (ordering only; no technical dependency)

**Files likely touched:**
- `services/auth/internal/service/errors_test.go`
- `docs/deferred-tuning.md`
- `PHASE1_CHECKLIST.md`

**Estimated scope:** Extra small

---

## ✅ Checkpoint: Complete

- [ ] All acceptance criteria met across Tasks 1–3
- [ ] Step 9's review findings all closed
- [ ] **Step 9 and Step 10 merge together** — per `SPEC.md` §9's sequencing decision, this is the point at which the merge happens
- [ ] Next: **rate limiting**, then a store integration harness, then Phase 2

---

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| **The unit suite cannot see this change.** The service and handler tests run against a Go map; a wrong query passes everything | **High — a broken login ships green** | Manual verification is a hard acceptance criterion on Task 1, using a real bcrypt hash so authentication must actually complete rather than merely finding a row. Named openly in `SPEC.md` §6 rather than papered over |
| Task 2 applied before Task 1 | Low — correct but sequential scans | Structural dependency, not a note. The whole reason §9 accepted the migration was to remove this trap permanently |
| `005` applied to a database where `004` never ran, leaving no email uniqueness | Critical if it happened | Impossible through golang-migrate, which applies in order; stated in the migration's own comments for anyone applying SQL by hand |
| `005.down` fails re-adding a `UNIQUE` constraint | Medium — a stuck rollback | Cannot happen (a `lower()` unique index implies exact uniqueness), but it is asserted on the dry-run database rather than reasoned about |
| Re-adding constraints takes an `ACCESS EXCLUSIVE` lock | Low now, real later | The same trade as §2.6, and the note written in Task 3 covers both directions |
| Scope creep into an integration-test harness | Medium — it is the obvious thing to reach for | Deferred explicitly in §7 and §9, scheduled behind rate limiting, and on the "Ask first" list |

## Open questions

**None.** `SPEC.md` §9 is fully resolved.

One thing flagged forward rather than treated as settled: Task 1's verification is manual, and manual verification does not survive into CI. It proves the fix today and protects nothing tomorrow. That is the argument for the store integration harness in §7, and the reason it is scheduled ahead of Phase 2 rather than left open-ended — Phase 2's trading engine will add far more SQL than auth has.
