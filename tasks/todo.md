# QuantSim Auth Input Validation — Task Checklist (Phase 1, Step 9)

> **`SPEC.md` is APPROVED** — decisions delegated 2026-07-30 with the instruction
> to decide against cybersecurity standards, and resolved in §9. **Implementation
> is unblocked.**
>
> Checking NIST SP 800-63B §3.1.1.2 directly **reversed three of the draft's own
> decisions**, which widened scope from the original draft:
> password minimum is **15, not 8** (`SHALL` for single-factor auth; the 8-char
> figure applies only alongside MFA); a **blocklist check** is required and was
> missing entirely; **case-insensitive usernames** move from non-goal to in
> scope. A **64 KiB body cap** was added since length checks run after decoding.

Full detail (acceptance criteria, verification steps, dependency graph, risks) in `tasks/plan.md`.

Prior steps archived: Auth Service (Step 4) at `docs/archive/phase1-step4-auth/`; Market Data ingestion (Step 5) and live polling (Step 6); API Gateway (Step 7); Minimal Frontend (Step 8) at `docs/archive/phase1-step8-frontend/` — all complete.

Each task is a stop-for-review checkpoint per `agents.md`: implement, verify, **stop**.

### Phase 1: The rules
- [x] **Task 1** — `validate.go`, `blocklist.go` + embedded list, `ErrInvalidInput`, and their tests. Pure functions, no DB or HTTP

### Phase 2: Wiring
- [x] **Task 2** — Enforce in `Register`/`Login`, 64 KiB body cap, map `ErrInvalidInput` → `400`, remove the handler's duplicate checks, **update all existing 10–14 char test fixtures**

- [x] ✅ **Checkpoint: Rules enforced** — verified 2026-07-31 against the running stack on a second instance (port 8099, dev DB), then the test rows removed. All §3 rejections return `400`; the 80-byte password returns `400` instead of `500`; a 10 MB body returns `413`; valid registration still `201`; **`khalil-ui-check@quantsim.test` still logs in with its 10-character password**. Case-duplicates not fixed yet — that is Task 3

### Phase 3: The database
- [x] **Task 3** — Migration `004`: lowercase emails, then unique indexes on `lower(email)` and `lower(username)`. Dry-run up **and** down on a throwaway DB first, including a seeded collision to confirm it fails loudly and rolls back clean. Applied to the dev DB 2026-07-31 at version 4, not dirty

- [x] ✅ **Checkpoint: Database aligned** — both §1 bugs verified fixed against the running stack: a second registration of the same address in different capitalisation returns **409**, and login with different capitalisation returns **200**. Username case-duplicates also return `409`. The collision pair was cleared by hand first (the uppercase row, per §3's "either row may go"), taking the table from 16 users to 15.
  **On "every pre-existing user can still log in":** `khalil-ui-check@quantsim.test` was verified directly with its 10-character password, in both original and upper case. The other 14 could not be — their passwords aren't known to anyone but their owners. The checkable property was verified instead: a pre/post snapshot of all rows shows the deleted row as the **only** difference, so no surviving user's `email`, `username`, or `password_hash` was touched by the migration

### Phase 4: Close out
- [ ] **Task 4** — Frontend hint to "At least 15 characters."; check off Step 9

- [ ] ✅ **Checkpoint: Complete** — Phase 1 fully closed; next is rate limiting, then Phase 2

---

## Why this step exists

Every row reproduced against the running stack on 2026-07-30, not inferred from reading code:

| Request | Today |
|---|---|
| `password: "a"` | 201 Created |
| `email: "x"` | 201 Created |
| 500-char username | 201 Created |
| 80-byte password | **500** (should be 400) |
| Same email, different case | **two separate accounts** |
| Login with different case | **401 lockout** |

The last two are the real motivation, and are live in the dev database right now.

---

## The risk to watch

Raising the minimum to 15 makes **every existing password in the database non-compliant**, including the `pw12345678` account used throughout Step 8's verification. Nobody is locked out because **`Login` is deliberately not tightened** (`SPEC.md` §2.12) — registration enforces policy, login authenticates whoever already exists.

That property is load-bearing, so it is asserted, not assumed: Task 2 carries an explicit regression test that a 10-character stored password still authenticates.

---

Every checkpoint runs `go test -count=1 ./...` in `services/auth` and `pkg`. Unlike Step 8, **this step ships tests** — it is exactly the logic-with-invariants that Steps 4–7 covered (`SPEC.md` §6).

**Flagged forward, not scheduled:** rate limiting on `/auth/login` is the largest remaining gap in the auth surface (`SPEC.md` §7). Nothing throttles credential stuffing today. It belongs at the gateway, where Step 7 deferred it, and is worth doing ahead of Phase 2 features.

Step 9 docs move to `docs/archive/phase1-step9-auth-validation/` when the next spec is drafted, per convention.
