# QuantSim Store-Layer Integration Harness — Task Checklist (Step 12)

> **`SPEC.md` is APPROVED** — the four design decisions were resolved 2026-08-14,
> all as recommended (§9). **Implementation is unblocked.**
>
> Closes `docs/NEXT_SESSION.md` item 1, carried across two sessions. Builds the
> safety net **before** the trading engine adds far more SQL than auth ever had.

Full detail (acceptance criteria, verification, dependency graph, risks) in `tasks/plan.md`.

Prior steps archived under `docs/archive/` — Auth Service (Step 4) through Auth Rate Limiting (Step 11).

Each checkpoint is a stop-for-review point per `agents.md`: implement, verify, **stop**.

---

## ⚠️ The one dangerous thing in this step

The dev database holds **15 real users** and this harness runs `TRUNCATE`. The
environment actively misleads: `POSTGRES_DB=quantsim` is an **empty decoy**,
while `DATABASE_URL` points at `postgres`, where the real rows live. The target
is `quantsim_test`, which is neither.

**Baseline measured 2026-08-14 — `users=15`, `accounts=15`.** Re-check at every
checkpoint:

```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT 'users=' || count(*) FROM users"
```

**Task 2 exists to prove the guards before anything destructive runs. Do not skip it.**

---

### Phase 1: The harness
- [ ] **Task 1** — `harness_test.go` + `main_test.go`: repo-root discovery via `go.work`, `.env` reader that does not export, DSN resolution, **three guards**, `CREATE DATABASE`, migrations by glob-and-exec, `TRUNCATE`, raw seed helper. `services/auth/go.mod` must stay unchanged

- [ ] **Task 2** — 🔴 **SMOKE**: `SELECT 1` passes; `assertTestDB` rejects `postgres`, `quantsim`, `""`; `quantsim_test` has 5 tables; **dev DB still at 15 users**; forcing the DSN at `postgres` **refuses** instead of truncating

- [ ] ⏸️ **Checkpoint: The harness is proven safe** — nothing destructive has touched real data, and the guards are demonstrated rather than assumed

### Phase 2: The tests that justify the step
- [ ] **Task 3** — `GetUserByEmail` / `GetUserByID`: mixed-case row seeded by raw SQL (the store cannot make one), stored-form return value, argument not lowercased, nil `PasswordHash`, `ErrUserNotFound`, full round-trip

- [ ] ⏸️ **Checkpoint: Step 10's fix is protected** — **and verified by mutation**: revert `GetUserByEmail` to `WHERE email = $1` and confirm the mixed-case test fails. A harness that passes against the pre-Step-10 query proves nothing

- [ ] **Task 4** — `CreateUserWithAccount`: duplicate email and **username**, exact and case-differing; rollback via `1e16` overflowing `NUMERIC(20,4)` (asserting `22003` and *not* `ErrDuplicateUser`); balance precision read as `::text`; `currency` default; plus `var _ service.UserStore = ...` in both the store and the mock

### Phase 3: Wiring and close-out
- [ ] **Task 5** — Makefile: `test`, `test-integration` (`-count=1`), `test-all`, `test-db-drop`, `vet` **including a `-tags=integration` pass** — tagged files are otherwise never type-checked by anything

- [ ] **Task 6** — Docs: a real "Integration tests" section in `docs/TESTING_STRUCTURE.md`; two `docs/deferred-tuning.md` entries with named triggers (testcontainers ← CI arriving; golang-migrate ← first migration needing a directive); Step 12 in `PHASE2_CHECKLIST.md` with mutation results; rewrite `docs/NEXT_SESSION.md`

- [ ] ⏸️ **Checkpoint: Step 12 complete** — `make test` green with Docker down, `make test-integration` green with it up, `make vet` clean, dev DB still at 15 users

---

## Reminders that have cost time before

**`DATABASE_URL` points at `postgres`, not `quantsim`.** `psql -d quantsim`
connects fine and shows no `users` table, which reads like data loss and is not.
The user is `quantsim`; the database is `postgres`.

**Restart a service after changing its code.** Everything runs under `go run`.
Not this step's concern — nothing here runs a service — but it has burned an
entire step before.

**A green unit suite still proves nothing about `internal/store/`** until Task 3
lands. That is the whole point of the step.
