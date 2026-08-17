# Implementation Plan — QuantSim Store-Layer Integration Harness (Step 12)

## Overview

`SPEC.md` is **approved**; §9 records the four decisions. Build a real-Postgres test suite for `services/auth/internal/store/`, which today has no tests at all — every auth suite runs against a Go map and would stay green against a completely wrong query.

The step adds one test-only package, two compile-time assertions, and four Makefile targets. **No query changes, no migration, no new module dependency.**

**The single dangerous thing in this step is pointing the harness at the wrong database.** The dev database holds 15 real users, this harness runs `TRUNCATE`, and the environment is actively misleading: `POSTGRES_DB=quantsim` is an *empty decoy* while `DATABASE_URL` points at `postgres`, where the real rows live. Task ordering is built around proving the guards work **before** anything destructive runs.

Recorded baseline, measured 2026-08-14 against the dev database: **`users=15`, `accounts=15`.** Verification re-checks this number.

## Architecture decisions

Restated from `SPEC.md` §2:

- **Reuse the docker-compose Postgres**, not testcontainers — no CI exists, so its advantage is hypothetical — §2.1
- **Target `quantsim_test`, behind three independent guards**, the last of which asks the server itself immediately before every `TRUNCATE` — §2.2
- **Skip, never fail, when Postgres is down** — and print the reason, because a permanently-skipping suite looks exactly like a passing one — §2.3
- **Migrations by exec'ing `.up.sql` in filename order** — no new dependency; verified that no migration uses a golang-migrate directive — §2.4
- **`TRUNCATE` between tests, not transaction-per-test** — the store calls `pool.Begin` itself, so an outer transaction would make the rollback test meaningless — §2.5
- **Rollback forced with `startingBalance = 1e16`**, which overflows `NUMERIC(20,4)` after the users insert succeeds — no schema mutation, nothing to leak — §2.6

## Dependency order

```
Task 1 (harness + guards) ──> Task 2 (SMOKE + guard proof) ──┬─> Task 3 (Step 10 tests) ──┬─> Task 5 (Makefile)
                                                              └─> Task 4 (rest + asserts) ─┘        │
                                                                                                    v
                                                                                              Task 6 (docs)
```

**Task 2 gates everything.** No test that truncates may be written until the guards are proven.

---

## Phase 1 — The harness

### Task 1 — Connection, guards, migration, truncation

**Files:** `services/auth/integration/harness_test.go`, `main_test.go`

Every file starts `//go:build integration` + `package integration`.

Pieces:
- `repoRoot` — walk up to the directory containing `go.work`. Not a relative path, which breaks if the package moves.
- `dotenv` — read `DATABASE_URL` from the repo-root `.env` **without exporting** into the process environment, so other tests in the binary cannot observe a different environment depending on whether `.env` exists.
- `resolveDSNs` — `TEST_DATABASE_URL`, else `DATABASE_URL`, else `.env`. Replace the path with `/quantsim_test`. Return an admin DSN pointing at `postgres` (`CREATE DATABASE` cannot run from inside its target).
- `assertTestDB` — **guard 1.** Fail closed: anything not exactly `quantsim_test` is rejected, `postgres` and `quantsim` explicitly included.
- `ensureTestDatabase` — `SELECT EXISTS(... pg_database ...)`, then `CREATE DATABASE`. Short connect timeout (~3s) so "Docker is off" costs seconds, not a minute.
- `applyMigrations` — glob `infra/migrations/*.up.sql`, sort, `Exec` each, wrapping failures with the file's basename so a broken migration is *named*.
- `truncateAll` — **guard 3**: `SELECT current_database()` then `assertTestDB`, then `TRUNCATE TABLE users CASCADE`.
- `insertUserRaw` — seed a row bypassing the store. Required for the mixed-case case, which the store cannot produce.
- `TestMain` + `newStore(t)` — **guard 2** after the pool connects; records `skipReason`, prints it to stderr.

**Acceptance criteria:**
- `go test ./...` in `services/auth` does **not** compile this package and does not error
- Guards are three separate checks, not one shared helper called once
- No `t.Parallel()` anywhere; a comment at the top of the harness says why
- `services/auth/go.mod` **unchanged** — verify with `git diff --exit-code services/auth/go.mod`

---

### Task 2 — 🔴 SMOKE: prove the guards before anything truncates

**Files:** `services/auth/integration/harness_test.go` (temporary test)

**This task exists to be paranoid and is not optional.**

Write one trivial test that connects and asserts `SELECT 1`, plus a test that `assertTestDB` rejects `postgres`, `quantsim`, and `""`. Then, by hand:

1. `make test-integration` → passes, `quantsim_test` now exists
2. `\dt` in `quantsim_test` → 5 app tables present
3. **`SELECT count(*) FROM users` in `postgres` → still 15** ← the check that matters
4. Temporarily force the DSN to `postgres` and confirm the run **refuses** rather than truncating

**Checkpoint — stop for review.** The harness is proven safe; nothing destructive has run against real data.

---

## Phase 2 — The tests that justify the step

### Task 3 — `GetUserByEmail` / `GetUserByID`

**Files:** `services/auth/integration/user_store_get_test.go`
**Depends on:** Task 2

Cases 1, 2, 3, 7, 8, 9 from `SPEC.md` §5. **Case 1 first** — the mixed-case row seeded via `insertUserRaw`, since the store cannot create one.

**Acceptance criteria:**
- Case 1 asserts the row is found **and** that the returned `Email` is the stored mixed-case form
- Case 3 pins that the store does *not* lowercase its own argument
- Case 7 asserts `PasswordHash == nil` from `GetUserByID`
- `errors.Is` throughout, never string matching

**Verification — the point of the entire step:** revert `GetUserByEmail` to `WHERE email = $1`, confirm case 1 fails with `ErrUserNotFound`, restore. **A harness that passes against the pre-Step-10 query proves nothing.**

**Checkpoint — stop for review.** Step 10's fix is protected by something other than a memory of having tested it once.

---

### Task 4 — `CreateUserWithAccount`, and the interface assertions

**Files:** `services/auth/integration/user_store_create_test.go`, plus two production one-liners
**Depends on:** Task 2

Cases 4, 5, 6, 10, 11 from `SPEC.md` §5.

**Acceptance criteria:**
- Duplicate email **and** duplicate username, each in exact and case-differing form → `ErrDuplicateUser`
- Rollback test uses `startingBalance = 1e16`; asserts `users` and `accounts` are both empty afterwards, that the error is a `*pgconn.PgError` with code `22003`, and that it is **not** `ErrDuplicateUser`
- Balance precision read back as `balance::text`, not as a float — **run it, observe, then assert what Postgres actually stores**, with the observed value recorded in a comment
- `currency` defaults to `USD` without Go setting it
- `var _ service.UserStore = ...` added to both `store` and `mock`, in the production files — not in the tagged package, where they would only be checked under the tag

**Verification:** mutate `tx.Commit` to run immediately after the users insert; the rollback test must fail. Delete the `23505` branch; the duplicate tests must fail.

---

## Phase 3 — Wiring and close-out

### Task 5 — Makefile

**Files:** `Makefile`
**Depends on:** Tasks 3, 4

`test`, `test-integration` (with `-count=1`), `test-all`, `test-db-drop`, `vet` (including a `-tags=integration` pass). Extend `.PHONY` and the `help` block, which lists every target.

**Acceptance criteria:**
- `make test` green across all four modules with Docker **stopped**
- `make test-integration` skips cleanly with Docker stopped, passes with it running
- `make vet` type-checks the tagged package — otherwise it rots unnoticed

---

### Task 6 — Documentation

**Files:** `docs/TESTING_STRUCTURE.md`, `docs/deferred-tuning.md`, `PHASE2_CHECKLIST.md`, `docs/NEXT_SESSION.md`
**Depends on:** Task 5

**Acceptance criteria:**
- `docs/TESTING_STRUCTURE.md` gains a real "Integration tests" section: the workflow, the `TEST_DATABASE_URL` override, `make test-db-drop`, and the never-the-dev-database rule. Its §4/§6 currently describe this as hypothetical
- `docs/deferred-tuning.md` gains **two entries with named triggers**: testcontainers ← *CI arriving*; golang-migrate library ← *the first migration needing a directive*
- Step 12 written up in `PHASE2_CHECKLIST.md`, including the mutation-check results
- `docs/NEXT_SESSION.md` rewritten per its own convention, with refresh-token revocation promoted to next

---

## Risks

| Risk | Mitigation |
|---|---|
| **Harness truncates the dev database — 15 real users gone** | Three independent guards; Task 2 proves them before any destructive statement; the baseline (`users=15`) is re-checked at every checkpoint |
| Misleading env: `POSTGRES_DB=quantsim` is empty, real data is in `postgres` | Target name is neither; `assertTestDB` names both as rejected |
| Suite silently skips forever and looks like it passes | Skip reason printed to stderr; `make test-integration` after `make docker-up` must show real `PASS` lines, and Task 2 checks this by hand |
| Tagged package never type-checked, rots | `make vet` includes `-tags=integration` |
| Tests pass without proving anything | Mutation checks in Tasks 3 and 4 are acceptance criteria, not suggestions |
| Repo path contains a space (`Personal projects`) | Paths built with `filepath`, never a `file://` URL — a real trap avoided for free by §2.4's glob approach |
| A future migration needs a golang-migrate directive | None today (verified). Trigger recorded in Task 6 |
| Accidental `t.Parallel()` makes the suite flake | Prohibition stated in a comment at the top of the harness |

## Out of scope

market-data's `historical_price_store.go`; CI; testcontainers; any change to a query in `user_store.go`, including one a failing test appears to justify — that would be a finding to raise, not a fix to fold in.
