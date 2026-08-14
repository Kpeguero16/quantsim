# QuantSim Identity Lookup Consistency — Task Checklist (Step 10)

> **`SPEC.md` is APPROVED** — the three open decisions were resolved 2026-07-31,
> all as recommended. **Implementation is unblocked.**
>
> **Sequencing:** Step 10 lands **before Step 9 is merged**, because it closes
> Step 9's own review findings. Merging first would put a known latent gap on
> `main`. Tasks commit onto `step9-task1-auth-validation`; both steps merge
> together at the end.

Full detail (acceptance criteria, verification steps, dependency graph, risks) in `tasks/plan.md`.

Prior steps archived under `docs/archive/` — Auth Service (Step 4) through Auth Input Validation (Step 9). **Phase 1 is complete;** this step is its tail.

Each task is a stop-for-review checkpoint per `agents.md`: implement, verify, **stop**.

### Phase 1: The fix
- [x] **Task 1** — `GetUserByEmail` matches `WHERE lower(email) = $1`. One line; the verification is larger than the change

- [x] ✅ **Checkpoint: Lookup no longer depends on stored form** — verified 2026-07-31 against a deliberately non-canonical row seeded with a real bcrypt hash. Before the change it was unfindable and login returned `401`; after, `200` in both capitalisations, while a wrong password still returns `401`. Every existing user still logs in

### Phase 2: The schema
- [x] **Task 2** — Migration `005`: drop `users_email_key` and `users_username_key`, both implied by `004`'s `lower()` indexes. Dry-run up **and** down completed on a throwaway DB, including `down` against a mixed-case username. Applied to the dev DB 2026-07-31 at version 5, not dirty

- [x] ✅ **Checkpoint: Schema carries only what it needs** — three indexes remain (`users_pkey`, `idx_users_email_lower`, `idx_users_username_lower`). Duplicate registration still returns `409` in both exact and case-differing forms; all logins unaffected; 15 users unchanged.
  **On query plans:** at 15 rows Postgres chooses a sequential scan for this lookup — and did so for the old exact-match query too (cost 1.23 vs 1.19), so nothing regressed. Forced with `enable_seqscan=off`, `idx_users_email_lower` is used at the same 8.15 cost as before. The index earns its place as a *constraint*; the planner will start choosing it for reads once the table is worth indexing

### Phase 3: Close out
- [x] **Task 3** — `InvalidInputMessage` table test (4 cases, both mutations caught); index-lock and `CONCURRENTLY` trade recorded as `docs/deferred-tuning.md` §3; Step 10 written up in `PHASE1_CHECKLIST.md`

- [x] ✅ **Checkpoint: Complete** — all four Step 9 review findings closed. `go test -count=1 ./...` green in `services/auth` and `pkg`. **Steps 9 and 10 are ready to merge together;** next is rate limiting, then the store integration harness, then Phase 2

---

## Why this step exists

Step 9's pre-merge review found four things. Only the first has a plausible failure behind it:

| # | Finding | Severity |
|---|---|---|
| 1 | `GetUserByEmail` matches `email = $1`, depending on an invariant the database does not enforce | **Important** |
| 2 | Four unique indexes on `users` where two suffice | Suggestion |
| 3 | `CREATE UNIQUE INDEX` takes an `ACCESS EXCLUSIVE` lock — irrelevant now, not later | Suggestion |
| 4 | `InvalidInputMessage` has no direct test | Suggestion |

**Finding 1 is not a live bug.** `Login` lowercases the email, the store matches exactly, and that works because every stored email *happens* to be lowercase — `004` rewrote the existing rows and `service.Register` (the only write path) normalises new ones.

What `004` never did was make that structural. A unique index on `lower(email)` stops a *second* row colliding with `Foo@x.test`; it does not stop `Foo@x.test` existing. If one ever appeared, that user could never log in and the failure would be a silent `401` — indistinguishable from a wrong password.

That is Step 9 §2.10's own argument — *"app-level normalisation alone is one forgotten `strings.ToLower` from breaking"* — applied to only half the problem. It justified constraining uniqueness; it applies equally to lookup.

The fix is one query, and it is free: `EXPLAIN` shows `WHERE lower(email) = $1` using `idx_users_email_lower` at **identical cost** to the current plan.

---

## The risk to watch

**The unit suite cannot see this change.** `services/auth/internal/store/` has no test files, and both the service and handler suites run against `mock.UserStore` — a Go map. They would stay green with a completely wrong query.

So the safety net is not CI, and pretending otherwise is the failure mode here. Task 1's manual check is a **hard acceptance criterion**: seed a non-canonical row with a real bcrypt hash of a known password, then log in as the lowercase form and require a `200`. Merely finding the row is not enough — authentication has to complete.

That check proves the fix today and protects nothing tomorrow, which is exactly the argument for the store integration harness deferred in `SPEC.md` §7 and scheduled ahead of Phase 2, behind rate limiting.

---

**Also decided, and recorded rather than left implicit:** no `CHECK (email = lower(email))`. It is the obvious stricter fix, and §2.3 rejects it — once lookup is case-insensitive the constraint guarantees nothing anything depends on, and a check violation is SQLSTATE `23514` while the store maps only `23505`, so it would convert a harmless condition into a `500`.

Every checkpoint runs `go test -count=1 ./...` in `services/auth` and `pkg`.

Step 10 docs move to `docs/archive/phase1-step10-identity-lookup/` when the next spec is drafted, per convention.
