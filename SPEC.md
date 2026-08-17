# SPEC — QuantSim Store-Layer Integration Harness (Step 12)

Status: **Approved 2026-08-14.** Khalil resolved the four design decisions before drafting; §9 records them. Implementation is unblocked.
Scope: one new test-only package in `services/auth`, two compile-time assertions in production files, Makefile targets. **No change to any query, no migration, no new module dependency.**

Prior specs archived at `docs/archive/phase1-step4-auth/` through `phase2-step11-auth-rate-limiting/`.

---

## 1. Objective

`services/auth/internal/store/` has **no tests at all**. Every auth suite runs against `mock.UserStore` (`services/auth/internal/service/mock/mock.go`), which is a Go map. All 18 existing test files would stay green against a completely wrong SQL query.

This is not a theoretical gap. Step 10's central fix — changing `GetUserByEmail` to match `WHERE lower(email) = $1` — lives in exactly that layer, and `PHASE1_CHECKLIST.md` records the caveat in its own words:

> *"the verification was manual, which proves the fix today and protects nothing tomorrow."*

**Why now, before the trading engine.** Phase 2 will add far more SQL than auth ever had — orders, trades, positions, P/L. Building the harness first means that SQL gets written against a working safety net. Building it afterwards means retrofitting one against a much larger surface, at which point the cheap version is no longer available. `docs/NEXT_SESSION.md` has carried this as item 1 across two sessions.

**Objective:** a real-Postgres test suite that **fails when the store's SQL is wrong**, plus the `make test` / `make test-integration` targets the repo has never had.

**Non-goals.** Not market-data's store. Not CI. Not testcontainers. Not a rewrite of any query — if a test finds a bug, that is a separate decision, not silent scope.

---

## 2. Design decisions

### 2.1 Reuse the docker-compose Postgres, not testcontainers

The harness connects to the Postgres that `make docker-up` already runs.

Testcontainers was the obvious alternative and is **rejected for now**: there is **no CI anywhere in this repo** (verified — no `.github/`, no `.gitlab-ci.yml`, nothing), so its main advantage, a self-contained ephemeral database that CI can start, is currently hypothetical. It also costs a heavyweight dependency in `services/auth` and container startup on every run.

**Recorded as the upgrade path with its trigger — CI arriving** — in `docs/deferred-tuning.md`, the same convention Steps 10 and 11 used.

### 2.2 A dedicated `quantsim_test` database, and three guards around it

The dev database holds **15 real users**, and this harness runs `TRUNCATE`. "Which database am I connected to" is therefore not a detail; it is the one assumption whose violation is unrecoverable.

The environment makes this sharper than it looks:

| | |
|---|---|
| `POSTGRES_USER` | `quantsim` |
| `POSTGRES_DB` | `quantsim` — **empty**, the known decoy |
| `DATABASE_URL` database | `postgres` — **this is where the 15 users actually live** |

So the two names a careless harness would reach for, `postgres` and `quantsim`, are respectively the real data and the decoy. The target is `quantsim_test`, which is neither.

**An absolute denylist, plus a check on every path that can write:**

`assertTestDB` fails closed twice over — first against a hardcoded
`protectedDatabases` list (`postgres`, `quantsim`, `template0`, `template1`),
then against an exact match on `quantsim_test`. It is called:

1. When the DSN is derived.
2. Immediately before `DROP DATABASE`.
3. After the pool connects — asking the server `SELECT current_database()`.
4. Immediately before **every** `TRUNCATE` — asking the server again.

Checks 3 and 4 consult the server rather than a string parsed at startup or a
boolean cached from it, because those are the statements that destroy data.

**The denylist was added in pre-merge review, and the reason matters** *(2026-08-17)*. The first version compared every target against `testDBName` alone. That defended against a wrong **DSN** and against nothing else: editing the constant to `postgres` would have satisfied every check while the harness dropped and truncated the database holding real users. The call that looked most protective — `assertTestDB(testDBName)` just before the `DROP` — was the emptiest of all, being a constant compared with itself, and a check that reads as protective but can never fire is worse than no check because it gets believed.

Checking an absolute list first makes the constant itself subject to the guard rather than the yardstick for it. Verified end to end by poisoning `testDBName` to `quantsim` — the **empty decoy**, chosen deliberately so a guard failure would cost nothing recoverable — and confirming the run aborts with a non-zero exit. The failure being guarded against is unrecoverable, so it is never reproduced against the real database in order to be tested.

### 2.3 Skip, never fail, when Postgres is unavailable

Plain `go test ./...` must stay green on a laptop with Docker stopped. Two mechanisms, and both are wanted:

- The package is behind `//go:build integration`, so a default run does not even compile it.
- With the tag on but the server unreachable, `TestMain` records a skip reason and every test calls `t.Skip` with it.

The skip reason is printed to stderr naming `make docker-up`. **A permanently-skipping suite otherwise looks identical to a passing one**, which is the failure mode that makes integration suites worthless over time.

### 2.4 Migrations by executing the `.up.sql` files in order — no new dependency

Verified: every migration is plain SQL with **no golang-migrate directives** (no `-- no-transaction`, no `CONCURRENTLY`), 1–5 statements each. The harness globs `infra/migrations/*.up.sql`, sorts by filename, and `Exec`s each in order with the pgx pool it already holds — roughly fifteen lines.

The test database is created once and truncated between tests, so migrate's version tracking and dirty-state recovery buy nothing here.

**`services/auth/go.mod` stays unchanged.** That matters: Step 7 §8 makes any new dependency an ask-first decision, and Step 11 held itself to zero new dependencies. Importing `golang-migrate` as a library would have added it plus its transitive tree to a production module's graph — Go does not mark test-only dependencies differently.

*Side benefit:* the test database is migrated **from zero** every time it is created, whereas the dev database was migrated incrementally over months. This harness is therefore also the first thing in the repo that proves `001`→`005` applies cleanly to an empty cluster.

**Trigger to revisit:** the first migration that needs a golang-migrate directive. `docs/deferred-tuning.md` §3 already flags `CONCURRENTLY` as a live Phase 2 consideration for the orders and trades tables.

### 2.5 `TRUNCATE` between tests, not transaction-per-test

`TRUNCATE TABLE users CASCADE` before each test — cascading through `accounts` → `positions`/`orders`/`trades` via the FK chain.

**Transaction-per-test is not available here, and the reason is the point of the whole step.** `PostgresUserStore` takes a `*pgxpool.Pool` and calls `pool.Begin` itself inside `CreateUserWithAccount`. Wrapping each test in an outer transaction would require reshaping production code to suit tests, and it would make the single most valuable test in the suite — rollback atomicity — untestable, because the rollback under test would degrade into a savepoint release inside the test's own transaction.

Truncating **before** rather than after each test leaves a failing test's rows in place for inspection with `psql`.

**No `t.Parallel()` anywhere in this package.** Every test shares one database and truncates it. Stated in a comment at the top of the harness, because adding `t.Parallel()` is exactly the reflexive change that would make this suite flake mysteriously.

### 2.6 The rollback path is forced with a numeric overflow

Testing that a failed `accounts` insert leaves no orphan `users` row requires making that insert fail **deterministically, after the users insert has already succeeded.**

`accounts.balance` is `NUMERIC(20,4)`. Verified directly against the running server:

```
SELECT 1e15::NUMERIC(20,4)  ->  1000000000000000.0000
SELECT 1e16::NUMERIC(20,4)  ->  ERROR: numeric field overflow
                                DETAIL: ... must round to an absolute value less than 10^16.
```

So passing `startingBalance = 1e16` fails the accounts insert with SQLSTATE `22003` while the users insert succeeds. **No schema mutation, nothing to clean up, nothing that can leak into a later test.**

A temporary `CHECK (false)` constraint was considered and rejected: it works, but a failed cleanup would silently poison every subsequent test in the package.

The test asserts the error is a `*pgconn.PgError` with code `22003`, so that if the injection ever stops working the test fails loudly rather than passing because something *else* went wrong. It also asserts the error is **not** `ErrDuplicateUser` — the `23505` mapping lives only in the users branch and must not over-reach.

---

## 3. Project structure

```
services/auth/integration/
  harness_test.go          # env resolution, three guards, migrate, truncate, seed helper
  main_test.go             # TestMain: create DB, migrate, record skip reason
  user_store_get_test.go   # GetUserByEmail / GetUserByID
  user_store_create_test.go# CreateUserWithAccount
```

Every file carries `//go:build integration` and `package integration`. **The repo has no build tags anywhere today** — this is the first.

The directory is a sibling of `internal/`, so `internal/store` and `internal/service` are both importable from it.

Production files touched — two lines total, no behaviour change:
- `services/auth/internal/store/user_store.go` — `var _ service.UserStore = (*PostgresUserStore)(nil)`
- `services/auth/internal/service/mock/mock.go` — `var _ service.UserStore = (*UserStore)(nil)`

Neither assertion exists today, so the mock and the real store can drift apart with nothing failing. The mock's is the more valuable of the two.

---

## 4. Configuration

| Variable | Meaning |
|---|---|
| `TEST_DATABASE_URL` | Explicit override — for a different host or user. **Still guarded**: the override may not change the database *name*. |
| `DATABASE_URL` | Normal path. Taken, with the path component **replaced** by `/quantsim_test`. Never used as-is. |

If neither is set in the environment, the harness reads `DATABASE_URL` from the repo-root `.env` without exporting it into the process. This exists because `make migrate-up` only works thanks to the Makefile's `-include .env` + `export`; running `go test -tags=integration ./integration/...` by hand from a plain shell would otherwise always skip, and a silently-skipping harness is worse than none.

The repo root is located by walking up to `go.work`, not by a relative path that breaks if the package moves.

---

## 5. Testing strategy

The suite's whole purpose is to catch what the mock cannot. Each case names that property.

| # | Test | What only a real database proves |
|---|---|---|
| 1 | Mixed-case stored row found by lowercase lookup | **Step 10's fix.** Seeded via raw SQL — the store *cannot* create such a row, since the service lowercases first. Reverting to `email = $1` must fail this. |
| 2 | Returned `Email` is the **stored** form, not the query argument | Currently unasserted anywhere. |
| 3 | Lookup does not lowercase its own argument | Pins the documented contract, so a later "fix" to `lower($1)` is a visible decision rather than a silent widening. |
| 4 | Duplicate email differing only in case → `ErrDuplicateUser` | `idx_users_email_lower` from migration 004 + the `23505` mapping. |
| 5 | Duplicate username, exact and case-differing → `ErrDuplicateUser` | **The mock models no username uniqueness at all**, so every existing suite is blind to this path. |
| 6 | Failed accounts insert leaves no orphan user | The transaction the doc comment promises. See §2.6. |
| 7 | `GetUserByID` returns nil `PasswordHash` | The query omits the column; the mock returns the same fully-populated pointer for both lookups, so the asymmetry is invisible today. |
| 8 | Both lookups miss → `ErrUserNotFound` | `pgx.ErrNoRows` mapping against the real driver. |
| 9 | Round-trip of UUID, `timestamptz`, and the hash | No encode/decode ever happens in the mock. |
| 10 | Exactly one account, `currency` defaulting to `USD` | Nothing in Go sets `currency` — an untested column default. |
| 11 | `StartingBalance` persists exactly in `NUMERIC(20,4)` | Documents the `float64` → numeric conversion. **Observe the real behaviour first, then assert it** — do not guess an expected string and bend the test to it. |

**Mutation checks are mandatory, not optional** — the standard Step 11's review established. At minimum:

| Mutation | Must fail |
|---|---|
| `lower(email) = $1` → `email = $1` | Test 1 — **the headline check. If this does not fail, the harness is worthless.** |
| Delete the `23505` branch | Tests 4 and 5 |
| Move `tx.Commit` to just after the users insert | Test 6 |
| Comment out `idx_users_email_lower` in migration 004, drop the test DB, rerun | Test 4 — proves migrations are genuinely applied, not inherited from a stale database |

---

## 6. Code style

- Follows the existing suites: external test package conventions, failure messages that explain **why** a property matters rather than restating the assertion.
- Comments explain why, per the house standard set by `proxy.go` and `identity.go`.
- No new module dependency (§2.4).
- Errors compared with `errors.Is`, never string matching.

---

## 7. Commands

```bash
make docker-up          # Postgres + Redis
make test               # unit tests, all four modules, no Docker needed
make test-integration   # this suite; skips cleanly if Postgres is down
make test-db-drop       # drop quantsim_test after editing a migration
make vet                # includes a -tags=integration pass
```

`test-integration` uses `-count=1`: results depend on database state and must never be cached.

`vet` must include the tagged pass. Tagged files are otherwise **never type-checked by any default command**, which is precisely how a tagged test file rots unnoticed.

---

## 8. Boundaries

**Always do:**
- Target `quantsim_test`, verified by all three guards (§2.2)
- Ask the *server* which database it is on before any `TRUNCATE`
- Skip, never fail, when Postgres is unreachable — and print why
- Seed mixed-case rows with raw SQL, since the store cannot produce them
- Run the §5 mutation checks before flagging the step done

**Ask first:**
- Any new module dependency — §2.4 is specifically what keeps this at zero
- Any change to a query in `user_store.go`, including one a test appears to justify
- Extending the harness to market-data's store
- Adding CI

**Never do:**
- Point the harness at `postgres` or `quantsim`
- Use `DATABASE_URL` unmodified
- Add `t.Parallel()` in this package (§2.5)
- Reshape production code to make a test easier (§2.5)
- Commit `.env`

---

## 9. Decisions resolved before drafting

Resolved 2026-08-14, all as recommended:

| # | Decision | Resolution |
|---|---|---|
| 1 | Next step | **The store harness**, ahead of refresh-token revocation and the trading engine |
| 2 | Postgres source | **Reuse docker-compose** + a dedicated `quantsim_test` — §2.1, §2.2 |
| 3 | Scope | **auth store only** |
| 4 | Migrations | **Exec the `.up.sql` files in order**, no new dependency — §2.4 |

---

## 10. Implementation

`tasks/plan.md` holds the breakdown, acceptance criteria, and risks; `tasks/todo.md` is the checkpoint list. Four checkpoints: **(1)** harness connects, creates, and migrates, with the guards proven and the dev database demonstrably untouched; **(2)** Step 10's fix protected, verified by mutation; **(3)** the rest of the surface plus the interface assertions; **(4)** Makefile, docs, handoff.
