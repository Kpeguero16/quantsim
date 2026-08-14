# SPEC — QuantSim Identity Lookup Consistency (Step 10)

Status: **Approved 2026-07-31.** Khalil resolved the open decisions as recommended, and reordered the work: Step 10 lands **before** Step 9 is merged, since it fixes Step 9's own review findings. §9 records the resolutions.
Scope: one query in `services/auth/internal/store`, one migration, one unit test, one documentation note. Small — deliberately so. Not a whole-project spec; see `agents.md` and `docs/intent/quantsim-resume.md`.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `phase1-step9-auth-validation/` — all complete. **Phase 1 is closed.**

---

## 1. Objective

Close the findings from the pre-merge review of Step 9 (2026-07-31). Step 9 made identity case-insensitive in the service layer and added unique indexes on `lower(email)` and `lower(username)`. The review found that the **lookup** was never brought along: it still matches exactly.

Four findings, in the review's own severity order:

| # | Finding | Severity |
|---|---|---|
| 1 | `GetUserByEmail` matches `email = $1`, depending on an invariant the database does not enforce | Important |
| 2 | Four unique indexes on `users` where two suffice | Suggestion |
| 3 | `CREATE UNIQUE INDEX` takes an `ACCESS EXCLUSIVE` lock; irrelevant now, not later | Suggestion |
| 4 | `InvalidInputMessage` has no direct test | Suggestion |

**Finding 1 is the only one with a plausible failure behind it**, and it is worth stating precisely, because it is *not* currently a live bug:

`Login` normalises the submitted email to lowercase, then the store matches it exactly. That works only because every stored email happens to be lowercase — migration `004` lowercased the existing rows and `service.Register` normalises every new one. **Verified: `service.Register` is the only write path** (no seed scripts, no other `INSERT INTO users`).

What migration `004` did *not* do is make that true structurally. A unique index on `lower(email)` prevents a *second* row colliding with `Foo@x.test`; it does not prevent `Foo@x.test` existing. Should one ever appear — a manual fix-up, a future import, a bug in a new write path — that user could never log in, and the failure would be a silent `401` indistinguishable from a wrong password.

This is the argument `004` itself makes, applied to only half the problem. From Step 9 §2.10:

> App-level normalisation alone is one forgotten `strings.ToLower` from breaking.

That reasoning justified constraining *uniqueness*. It applies just as well to *lookup*.

**Out of scope:** everything in `docs/security-backlog.md` — rate limiting above all, which remains the largest gap in the auth surface and is the right way to open Phase 2. This step is the tail of Step 9, not a substitute for that.

---

## 2. Decisions

### 2.1 Look users up by `lower(email)`

`services/auth/internal/store/user_store.go:63`:

```sql
-- from
SELECT ... FROM users WHERE email = $1
-- to
SELECT ... FROM users WHERE lower(email) = $1
```

The lookup then matches the constraint that actually exists, and stops depending on the stored form being canonical.

**Verified, not assumed** — `EXPLAIN` against the dev database:

```
Index Scan using idx_users_email_lower on users  (cost=0.14..8.15 rows=1 width=16)
  Index Cond: (lower(email) = '...'::text)
```

Identical cost to the current plan (`0.14..8.15` using `users_email_key`). This is free.

`Login` keeps calling `NormalizeEmail` first. That is not redundant: it is what makes the bound parameter canonical, and lowercasing the *needle* is required for `lower(haystack) = needle` to mean anything. Two halves of one rule, not two copies of it.

### 2.2 Drop the redundant `UNIQUE` constraints from migration 001

Migration `005` drops `users_email_key` and `users_username_key`.

Both are now fully implied: two rows with identical `email` necessarily share `lower(email)`, so `UNIQUE (lower(email))` already rejects them. They are pure write-path overhead — four unique index maintenances per insert where two would do.

**Coupled to §2.1, and the coupling is the risk.** Dropping `users_email_key` while an exact-match query is still running turns that lookup into a sequential scan. Correct, but slower. So §2.1 ships **before or with** §2.2, never after. On 15 rows this is unobservable either way; the ordering discipline is for the habit, not for this database.

### 2.3 **No** `CHECK (email = lower(email))` — the stricter option, considered and rejected

The obvious alternative to §2.1 is to force canonical storage instead of tolerating non-canonical storage. Rejected, for three reasons:

1. **It stops being load-bearing.** Once lookup is case-insensitive (§2.1), a non-canonical row is found correctly anyway. The `CHECK` would guarantee something nothing depends on.
2. **It adds an unmapped failure mode.** A check violation is SQLSTATE `23514`. The store maps only `23505` → `ErrDuplicateUser` (`user_store.go:42`), so `23514` would surface as a **500**. That is a worse outcome than the condition it prevents.
3. **It constrains a decision that is not ours to freeze.** Emails are lowercased today because §2.7 of Step 9 argued they should be. A `CHECK` would make revisiting that a migration rather than a code change.

Recorded here so this is a decision rather than an omission.

### 2.4 Usernames need no lookup change — only the index drop

**Verified: nothing anywhere looks a user up by username.** The store exposes `GetUserByEmail` and `GetUserByID` and nothing else; `grep` for `WHERE username` returns nothing.

So §2.1 has no username equivalent, and `idx_users_username_lower` exists purely to prevent `Admin` and `admin` coexisting — which is exactly what Step 9 §2.8 intended. Its plain counterpart `users_username_key` is the redundant one.

This also means usernames stay non-canonical on purpose: migration `004` deliberately did not rewrite them, so an existing `Khalil` keeps the capitalisation its owner chose. That remains correct and is not revisited here.

### 2.5 `InvalidInputMessage` gets a direct test

Currently covered only indirectly, by handler tests asserting the rendered message does not contain `"invalid input"`. Its two edge cases are untested:

- `nil` in → `""` out
- an error that does **not** wrap `ErrInvalidInput` → returned in full, because `TrimPrefix` is a no-op

Both are deliberate behaviours (`errors.go:29-42`) and neither is pinned. A table test of three cases.

### 2.6 The index-lock tradeoff is documented, not fixed

`CREATE UNIQUE INDEX` takes an `ACCESS EXCLUSIVE` lock for its duration. On 15 rows that is instant and irrelevant; against a real dataset it blocks writes.

The production answer is `CONCURRENTLY`, which **cannot run inside a transaction** — under golang-migrate it needs the `-- no-transaction` directive, and that forfeits the all-or-nothing rollback that `004`'s dry run demonstrated. That is a real trade, not an oversight, and the right moment to make it is when there is a dataset worth protecting.

It goes in `docs/deferred-tuning.md`, whose framing is exactly this: performance defaults that trigger when there is real traffic. **Not** `docs/security-backlog.md` — it is not a security gap.

### 2.7 The `down` migration restores what `up` dropped

`005.down.sql` re-adds both `UNIQUE` constraints. Unlike `004`, this rollback is **complete**: no data is transformed in either direction, and the constraints can be recreated exactly. Worth stating because `004.down.sql` says the opposite about itself, and the contrast is instructive rather than accidental.

---

## 3. Commands

Confirm the finding is real before fixing it — insert a deliberately non-canonical row and watch login fail:

```bash
# Seed a row the service layer could never produce
docker exec quantsim-postgres psql -U quantsim -d postgres -c \
  "INSERT INTO users (email, username, password_hash) VALUES
   ('NonCanon@quantsim.test','noncanon','\$2a\$10\$abcdefghijklmnopqrstuv');"

# BEFORE the fix: 401 -- the row exists and cannot be found
# AFTER  the fix: 401 as well, but for the right reason (that hash matches
#                 no password). Prove the lookup itself with the query:
docker exec quantsim-postgres psql -U quantsim -d postgres -c \
  "SELECT id FROM users WHERE email = 'noncanon@quantsim.test';"        -- 0 rows, today
docker exec quantsim-postgres psql -U quantsim -d postgres -c \
  "SELECT id FROM users WHERE lower(email) = 'noncanon@quantsim.test';" -- 1 row

# Clean up
docker exec quantsim-postgres psql -U quantsim -d postgres -c \
  "DELETE FROM users WHERE lower(email) = 'noncanon@quantsim.test';"
```

The end-to-end check that matters — a real account, registered normally, still logs in:

```bash
curl -i -X POST localhost:8080/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"khalil-ui-check@quantsim.test","password":"pw12345678"}'   # 200

curl -i -X POST localhost:8080/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"KHALIL-UI-CHECK@QUANTSIM.TEST","password":"pw12345678"}'   # 200
```

Index state, before and after `005`:

```bash
docker exec quantsim-postgres psql -U quantsim -d postgres -c \
  "SELECT indexname FROM pg_indexes WHERE tablename='users' ORDER BY indexname;"
# before: users_pkey, users_email_key, users_username_key,
#         idx_users_email_lower, idx_users_username_lower
# after:  users_pkey, idx_users_email_lower, idx_users_username_lower
```

---

## 4. Project structure

```
services/auth/internal/store/
  user_store.go        # GetUserByEmail -> WHERE lower(email) = $1 (§2.1)

services/auth/internal/service/
  errors_test.go       # NEW -- InvalidInputMessage table test (§2.5)

infra/migrations/
  005_drop_redundant_unique_constraints.up.sql    # drop users_email_key,
                                                  #   users_username_key (§2.2)
  005_drop_redundant_unique_constraints.down.sql  # re-add both (§2.7)

docs/
  deferred-tuning.md   # + the CONCURRENTLY / no-transaction trade (§2.6)
```

No changes to the handler, the service layer's logic, the frontend, the gateway, or token issuance.

---

## 5. Code style / conventions

- Migrations keep the `NNN_name.up.sql` / `.down.sql` pair convention, and carry their reasoning in comments the way `004` does.
- The store stays a thin translation layer: SQL and error-code mapping, no domain rules.
- `errors_test.go` is an **external** test (`package service_test`), matching `validate_test.go`. `InvalidInputMessage` is exported, so there is no reason to reach inside.
- Constraint drops name the constraint explicitly (`ALTER TABLE users DROP CONSTRAINT users_email_key`), never a bare index drop — these were created by `CREATE TABLE ... UNIQUE` and are constraints, not free-standing indexes.

---

## 6. Testing strategy

**The honest problem with this step: the change in §2.1 lands in the one layer that has no automated tests.** `services/auth/internal/store/` contains exactly one file and no `_test.go`. The service and handler suites both run against `mock.UserStore`, which is a Go map — it cannot catch a SQL change, and it would keep passing if the query were wrong.

So the safety net here is *not* the unit suite, and pretending otherwise would be the mistake. Instead:

- **Manual verification is mandatory, not optional** — the §3 sequence, run against a deliberately non-canonical row. That is the only thing that actually exercises the changed line.
- **The full suite still runs** (`go test -count=1 ./...` in `services/auth` and `pkg`) to prove nothing regressed, while acknowledging it cannot prove the fix.
- **Migration `005` is dry-run** on a throwaway database, up **and** down, the same way `004` was — including confirming that re-adding the constraints succeeds against real data.
- **§2.5's test** is ordinary and covered by the suite.

A store-layer integration test is the real answer and is deferred with reasoning in §7.

---

## 7. Deferred with reasoning, not omitted

1. **Store-layer integration tests.** The gap §6 names. `docs/TESTING_STRUCTURE.md` §4 already sketches the shape — `services/auth/integration/`, `-tags=integration`, a real Postgres. Deferred because it needs a harness decision (testcontainers vs. the existing docker-compose, and how CI gets a database) that deserves deciding deliberately rather than as a rider on a two-line query change. **It should come before Phase 2's trading engine**, which will add far more SQL than auth has.
2. **`DATABASE_URL` points at the `postgres` database while an empty `quantsim` database sits beside it.** Not a bug, but it cost real confusion during Step 9 — a manual `DELETE` appeared to succeed against the wrong target. Renaming means a dump, a restore, and an `.env` change for a purely cosmetic gain, so it is worth doing at a natural break rather than mid-step.
3. **Everything in `docs/security-backlog.md`**, unchanged by this step: rate limiting (item 1, still the largest gap), refresh-token revocation, gateway-wide body caps, Unicode password normalisation, Argon2id, HIBP.

---

## 8. Boundaries

**Always do:**
- Ship §2.1 before or with §2.2 — never drop `users_email_key` while an exact-match query is live
- Run the §3 non-canonical-row check by hand; the unit suite cannot cover this change
- Dry-run `005` up **and** down on a throwaway database before the real one
- Confirm `khalil-ui-check@quantsim.test` still logs in, in both capitalisations, after the migration

**Ask first:**
- Adding a `CHECK` constraint on `email` (§2.3 decided against it)
- Rewriting stored usernames to lowercase (§2.4 — `004` deliberately preserved them)
- Changing what `/auth/me` returns
- Anything that alters `Login`'s uniform failure response
- Introducing an integration-test harness as part of this step (§7 — it is its own decision)

**Never do:**
- Reintroduce exact-match email lookup
- Drop an index without the corresponding query change landing first
- Let a migration in this project delete a user row

---

## 9. Resolutions

Resolved 2026-07-31. All three taken as recommended.

- [x] **§2.2 is in scope — migration `005` ships.** The redundancy is genuine and cheapest to remove now, while the schema is small and the reasoning is fresh. The deciding argument is not the wasted index maintenance, which on 15 rows is nothing: it is that the §2.2 coupling — never drop `users_email_key` while an exact-match query is live — is exactly the kind of constraint that becomes hazardous once nobody remembers it. Removing the redundancy removes the trap along with it.
- [x] **§2.5 stays narrow.** A table test for `InvalidInputMessage` only. It is the sole exported function in `errors.go`; sweeping in the four sentinels would be testing `errors.New`.
- [x] **§7 item 1 (store integration tests) is scheduled ahead of Phase 2, behind rate limiting.** Both belong before the trading engine. Rate limiting goes first because it has an attacker behind it, while this has a class of bug behind it. Neither is in this step.

### Sequencing decision

**Step 10 lands before Step 9 is merged.** Step 9's branch is review-clean but carries the findings this step exists to close; merging first would put a known latent gap on `main` and leave the fix as a follow-up that has to re-establish its own context. Both steps merge together, findings already closed.

The practical consequence: Step 10's tasks are committed onto `step9-task1-auth-validation` rather than a fresh branch.
