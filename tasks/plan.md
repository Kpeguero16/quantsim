# Implementation Plan — QuantSim Auth Input Validation (Phase 1, Step 9)

## Overview

Close the auth-service input-validation gap found while drafting the Step 8 frontend spec: today a one-character password, the literal string `x` as an email, and a 500-character username all register successfully, an 80-byte password returns `500` instead of `400`, and — the two that actually matter — the same email address in different capitalisation creates **two separate accounts**, while a user who registered as `Khalil@x.test` cannot log in as `khalil@x.test`.

`SPEC.md` is **draft, awaiting review**. Nothing here starts until §9 is signed off.

Four tasks, ordered so the risky, hard-to-reverse piece (the migration) lands only after the code that depends on it is proven.

## Architecture decisions

Restated from `SPEC.md` §2 for reference while implementing:

- **Validation lives in `internal/service`**, not the handler; handler maps the typed error to HTTP — §2.1.
- **`ErrInvalidInput` → `400 invalid_request`**; no new error code, no per-field body — §2.2.
- **Password: min 8 runes, max 72 bytes.** The asymmetry is deliberate — bcrypt's limit is on bytes — §2.3.
- **Email: `net/mail.ParseAddress`**, reject display-name forms, cap 254 bytes, no dot-in-domain requirement — §2.4.
- **Email lowercased + trimmed** for storage and lookup — §2.5. *This is the actual bug fix.*
- **Unique index on `lower(email)`**, which fails loudly on existing collisions — §2.6.
- **Username 3–30, `[A-Za-z0-9_-]`** — §2.7, beyond the checklist, cut if rejected.
- **Login is not tightened** — non-empty plus normalisation only, so no existing user is locked out — §2.8.
- **Tests ship with this step**, unlike Step 8 — §6.

## Dependency graph

```
Task 1 (validate.go + tests)        ← pure functions, no DB, no HTTP
    │
    └── Task 2 (wire into service + handler)
            │
            └── Task 3 (migration 004 + the manual cleanup it forces)
                    │
                    └── Task 4 (frontend hint + close out)
```

Task 1 is deliberately first and standalone: the rules are pure functions, so they can be fully proven before anything mutable is touched. Task 3 is deliberately last of the backend work — it is the only irreversible piece (§7), and by then the code that depends on normalisation is already tested.

---

# Phase 1: The rules

## Task 1: `validate.go` and its tests

**Description:** The rules as pure functions, with no HTTP, database, or context involved. Nothing is wired up yet — this task is provably correct on its own before anything calls it.

**Acceptance criteria:**
- [ ] `NormalizeEmail(string) string` trims and lowercases; idempotent
- [ ] `ValidateRegistration(email, username, password string) error` returns `ErrInvalidInput`-wrapped errors with user-readable messages
- [ ] Password minimum counted in **runes**, maximum in **bytes** (§2.3)

**Verification:**
- [ ] `cd services/auth && go test -count=1 ./internal/service/...`
- [ ] Boundary cases both sides: password 7/8 runes and 72/73 bytes; **a multi-byte password that is 8 runes but more than 8 bytes** — this is the case that proves the rune/byte split is real and not incidental
- [ ] `NormalizeEmail` idempotence asserted, not assumed

**Dependencies:** None

**Files likely touched:**
- `services/auth/internal/service/validate.go`
- `services/auth/internal/service/validate_test.go`
- `services/auth/internal/service/errors.go`

**Estimated scope:** Small

---

# Phase 2: Wiring

## Task 2: Enforce in `Register`/`Login`, map in the handler

**Description:** Call the validator, normalise the email, and surface rejections as `400`. Remove the handler's now-duplicate non-empty checks so the rules live in exactly one place.

**Acceptance criteria:**
- [ ] `Register` validates and normalises **before** hashing or any store call
- [ ] `Login` normalises the email before lookup but applies **no** other rules (§2.8)
- [ ] Handler maps `ErrInvalidInput` → `400 invalid_request`; its non-empty checks are gone
- [ ] `bcrypt.ErrPasswordTooLong` also maps to `400`, not `500` (§2.9)

**Verification:**
- [ ] `cd services/auth && go test -count=1 ./...`
- [ ] Service test asserts the **mock store recorded no write** on a rejected registration — proves validation runs first rather than the database happening to reject it
- [ ] Handler tests cover each new `400` and confirm the happy path still returns `201`
- [ ] Manual: the first four `SPEC.md` §3 curls now return `400` (including the 80-byte password that returns `500` today)

**Dependencies:** Task 1

**Files likely touched:**
- `services/auth/internal/service/auth.go`
- `services/auth/internal/service/auth_test.go`
- `services/auth/internal/handler/auth.go`
- `services/auth/internal/handler/auth_test.go`

**Estimated scope:** Medium

---

## ✅ Checkpoint: Rules enforced (after Tasks 1–2)

- [ ] `go test -count=1 ./...` passes in `services/auth` and `pkg`
- [ ] Short password, malformed email, over-long password, and over-long username all rejected with `400`
- [ ] A valid registration still succeeds with `201`
- [ ] **Not yet fixed at this point:** case-duplicate accounts. New registrations normalise, but the database still permits a collision until Task 3 — expected, not a regression
- [ ] **Stop for architect review before the migration**

---

# Phase 3: The database

## Task 3: Migration 004 — lowercase existing emails, add the unique index

**Description:** Make the database enforce what the code now assumes. The only irreversible step in this plan, and the one that needs a manual decision first.

**Acceptance criteria:**
- [ ] `004_email_case_insensitive.up.sql` lowercases existing emails, **then** creates `UNIQUE INDEX users_email_lower_key ON users (lower(email))`
- [ ] `004_..._down.sql` drops the index, with a comment stating that original capitalisation is **not** restorable (§7)
- [ ] Pre-existing case-collisions are cleared **by hand** using the §3 query — the migration must not delete user rows

**Verification:**
- [ ] Dry-run `up` **and** `down` against a throwaway database first (same approach used to verify Phase 1's handoff), before touching the working one
- [ ] Confirm the collision query returns no rows, then `make migrate-up` on the real database
- [ ] Registering the same address in two capitalisations now returns `409`, not two accounts
- [ ] Logging in with different capitalisation than registration returns `200`
- [ ] Every pre-existing user can still log in — **the check that matters most here**

**Dependencies:** Task 2

**Files likely touched:**
- `infra/migrations/004_email_case_insensitive.up.sql`
- `infra/migrations/004_email_case_insensitive.down.sql`

**Estimated scope:** Small (but the highest-risk task in the step)

**Notes:** The current dev database contains a live collision created while investigating for the spec, so the index **will** fail until it is cleared. That is the designed behaviour, not a defect. `migrate` is installed at `~/go/bin/migrate` but is not on the non-interactive shell PATH — invoke it by full path or run `make migrate-up` from an interactive shell.

---

## ✅ Checkpoint: Database aligned (after Task 3)

- [ ] Both §1 bugs demonstrably fixed: no duplicate account, no case lockout
- [ ] All pre-existing users can still log in
- [ ] `go test -count=1 ./...` still green
- [ ] **Stop for architect review**

---

# Phase 4: Close out

## Task 4: Frontend hint and step close-out

**Description:** Restore the honest wording now that the server enforces the rule, and mark the step done.

**Acceptance criteria:**
- [ ] `LoginPage.tsx` hint reads "At least 8 characters." again (§2.10); no client-side enforcement added
- [ ] A 4-character password in the real form shows the **server's** message
- [ ] Step 9 checked off in `PHASE1_CHECKLIST.md`, including its handoff-criteria line

**Verification:**
- [ ] `cd frontend && npm run build && npm run lint`
- [ ] Manual: register with a short password in the browser and confirm the backend message renders in the error region
- [ ] Manual: register a valid new user end to end and reach the dashboard

**Dependencies:** Task 3

**Files likely touched:**
- `frontend/src/auth/LoginPage.tsx`
- `PHASE1_CHECKLIST.md`

**Estimated scope:** Extra small

---

## ✅ Checkpoint: Complete

- [ ] All acceptance criteria met across Tasks 1–4
- [ ] Phase 1 fully closed, auth hardening included
- [ ] Next: **Phase 2 — Trading Engine**

---

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| **Migration locks out real users.** Lowercasing emails changes the value users authenticate against | High — the worst outcome in this step | `Login` normalises its input (Task 2) *before* the migration runs (Task 3), so both sides agree from the moment the data changes. Explicitly re-verified as a Task 3 acceptance criterion |
| **Index creation fails on existing collisions** | Medium — blocks the migration | Intended (§2.6). Cleanup query is in `SPEC.md` §3; clear by hand before migrating |
| **`down` cannot restore original capitalisation** | Low, accepted | Documented in §7 and in the migration file itself, rather than discovered later |
| Validation applied to `Login` by reflex, locking out short-password accounts | High if it happened | Called out in §2.8 and in the "Never do" list; a test asserts login still works for an existing short-password user |
| Scope creep into password reset, lockout, or rate limiting | Medium | §1 out-of-scope list and §8 "Ask first" |

## Open questions

Everything in `SPEC.md` §9 is unresolved pending review. The two most worth a decision:

- **§2.7** — is username validation in scope, or should this step stay strictly to password and email as the checklist words it?
- **§2.4** — should `user@localhost` remain valid, or should the domain be required to contain a dot?
