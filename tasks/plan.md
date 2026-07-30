# Implementation Plan — QuantSim Auth Input Validation (Phase 1, Step 9)

## Overview

Close the auth-service input-validation gap. Today a one-character password, the literal string `x` as an email, and a 500-character username all register successfully; an 80-byte password returns `500` instead of `400`; and — the two that actually matter — the same email in different capitalisation creates **two separate accounts**, while a user who registered as `Khalil@x.test` cannot log in as `khalil@x.test`.

`SPEC.md` is **approved**, decided against cybersecurity standards. Checking NIST SP 800-63B §3.1.1.2 directly reversed three of the draft's own decisions, which widened the scope from the original draft:

- Password minimum is **15**, not 8 (`SHALL` for single-factor auth; 8 applies only with MFA)
- A **blocklist check** is required (`SHALL`) and was missing entirely
- **Case-insensitive usernames** move from non-goal to in scope
- Plus a **64 KiB request body cap**, since length checks run after decoding and so cannot bound memory

## Architecture decisions

Restated from `SPEC.md` §2 for reference while implementing:

- **Validation lives in `internal/service`**, not the handler — §2.1
- **`ErrInvalidInput` → `400 invalid_request`**; no new code, no per-field body — §2.2
- **Password: min 15 runes, max 72 bytes.** Asymmetry is deliberate; bcrypt's limit is bytes — §2.3, §2.4
- **Blocklist**: embedded 15+ char entries, context terms (username, email local part), trivial patterns — §2.5
- **Email**: `net/mail.ParseAddress`, reject display-name forms, 254-byte cap, no dot requirement — §2.6
- **Email + username lowercased** for storage and lookup — §2.7, §2.8
- **Username 3–30, `[A-Za-z0-9_-]`** — homograph impersonation — §2.9
- **Unique indexes on `lower(email)` and `lower(username)`**, failing loudly — §2.10
- **64 KiB body cap** in the handler before decode — §2.11
- **Login is NOT tightened** — availability, and preserving the uniform failure response — §2.12
- **Tests ship with this step** — §6

## Dependency graph

```
Task 1 (validate.go + blocklist + tests)   ← pure functions, no DB, no HTTP
    │
    └── Task 2 (wire into service + handler, body cap, fixture updates)
            │
            └── Task 3 (migration 004 — the only irreversible step)
                    │
                    └── Task 4 (frontend hint + close out)
```

Task 1 is standalone and first: the rules are pure functions, provable before anything mutable is touched. Task 3 is last of the backend work — it is the only irreversible piece (§7), and by then the code depending on normalisation is already tested.

---

# Phase 1: The rules

## Task 1: `validate.go`, the blocklist, and their tests

**Description:** The rules as pure functions — no HTTP, database, or context. Fully provable before anything calls them.

**Acceptance criteria:**
- [ ] `NormalizeEmail` / `NormalizeUsername` trim and lowercase; both idempotent
- [ ] `ValidateRegistration(email, username, password)` returns `ErrInvalidInput`-wrapped errors with user-readable messages
- [ ] Password minimum in **runes** (15), maximum in **bytes** (72) — §2.3, §2.4
- [ ] Blocklist rejects: an embedded entry, a password containing the username or email local part, a single repeated character, a simple sequence — §2.5

**Verification:**
- [ ] `cd services/auth && go test -count=1 ./internal/service/...`
- [ ] Boundaries both sides: 14/15 runes, 72/73 bytes, usernames 2/3 and 30/31
- [ ] **A 15-rune password that exceeds 15 bytes** — proves the rune/byte split is real, not incidental
- [ ] **A negative blocklist case**: an ordinary long passphrase passes. Guards against checks so broad they reject good passwords
- [ ] Idempotence of both normalisers asserted, not assumed

**Dependencies:** None

**Files likely touched:**
- `services/auth/internal/service/validate.go`, `validate_test.go`
- `services/auth/internal/service/blocklist.go`, `blocklist.txt`
- `services/auth/internal/service/errors.go`

**Estimated scope:** Medium

---

# Phase 2: Wiring

## Task 2: Enforce in `Register`/`Login`, cap bodies, map in the handler

**Description:** Call the validator, normalise, cap request bodies, and surface rejections as `400`. Remove the handler's duplicate non-empty checks so the rules live in one place.

**Acceptance criteria:**
- [ ] `Register` validates and normalises **before** hashing or any store call
- [ ] `Login` normalises email only — **no** other rules (§2.12)
- [ ] Handler maps `ErrInvalidInput` → `400 invalid_request`; its non-empty checks are gone
- [ ] `http.MaxBytesReader(w, r.Body, 64<<10)` on every auth route (§2.11)
- [ ] `bcrypt.ErrPasswordTooLong` maps to `400`, not `500` (§2.13)
- [ ] **All existing test fixtures updated** from 10–14 chars to 15+ (§2.3)

**Verification:**
- [ ] `cd services/auth && go test -count=1 ./...`
- [ ] Service test asserts the **mock store recorded no write** on rejection — proves validation runs first, rather than the database happening to reject it
- [ ] **Regression test: an account with a 10-character stored password still logs in.** The single most important assertion in this step — it proves the change locks nobody out
- [ ] Handler tests cover each `400`, the still-`201` happy path, and an oversized body
- [ ] Manual: the §3 curls through the over-long-body case behave as documented

**Dependencies:** Task 1

**Files likely touched:**
- `services/auth/internal/service/auth.go`, `auth_test.go`
- `services/auth/internal/handler/auth.go`, `auth_test.go`

**Estimated scope:** Medium

---

## ✅ Checkpoint: Rules enforced (after Tasks 1–2)

- [ ] `go test -count=1 ./...` green in `services/auth` and `pkg`
- [ ] Short/long password, blocklisted password, malformed email, long username, oversized body all rejected
- [ ] A valid registration still returns `201`
- [ ] **An existing short-password account still logs in**
- [ ] **Not yet fixed:** case-duplicate accounts. New registrations normalise, but the database still permits a collision until Task 3 — expected, not a regression
- [ ] **Stop for architect review before the migration**

---

# Phase 3: The database

## Task 3: Migration 004 — lowercase emails, add both unique indexes

**Description:** Make the database enforce what the code now assumes. The only irreversible step, and the one needing a manual decision first.

**Acceptance criteria:**
- [ ] `up` lowercases existing emails, **then** creates unique indexes on `lower(email)` and `lower(username)`
- [ ] `down` drops both indexes, with a comment that original capitalisation is **not** restorable (§7)
- [ ] Pre-existing collisions cleared **by hand** via the §3 query — the migration must not delete user rows

**Verification:**
- [ ] Dry-run `up` **and** `down` against a throwaway database first — same approach used to verify Phase 1's handoff
- [ ] Collision query returns no rows, then `make migrate-up` on the real database
- [ ] Same address in two capitalisations now returns `409`, not two accounts
- [ ] Login with different capitalisation returns `200`
- [ ] **Every pre-existing user can still log in** — the check that matters most here

**Dependencies:** Task 2

**Files likely touched:**
- `infra/migrations/004_case_insensitive_identity.up.sql` / `.down.sql`

**Estimated scope:** Small (highest-risk task in the step)

**Notes:** The dev database holds a live collision created while investigating for the spec, so the index **will** fail until cleared. That is designed behaviour. `migrate` lives at `~/go/bin/migrate` and is not on the non-interactive shell PATH — invoke by full path, or run `make migrate-up` from an interactive shell.

---

## ✅ Checkpoint: Database aligned (after Task 3)

- [ ] Both §1 bugs demonstrably fixed: no duplicate account, no case lockout
- [ ] All pre-existing users can still log in
- [ ] `go test -count=1 ./...` still green
- [ ] **Stop for architect review**

---

# Phase 4: Close out

## Task 4: Frontend hint and step close-out

**Acceptance criteria:**
- [ ] `LoginPage.tsx` hint reads "At least 15 characters." (§2.14); no client-side enforcement added
- [ ] A 10-character password in the real form shows the **server's** message
- [ ] Step 9 checked off in `PHASE1_CHECKLIST.md`, including its handoff-criteria line

**Verification:**
- [ ] `cd frontend && npm run build && npm run lint`
- [ ] Manual: register with a short password in the browser, confirm the backend message renders in the error region
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
- [ ] Next: **rate limiting** (`SPEC.md` §7 — the largest remaining auth gap), then Phase 2

---

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| **The 15-char minimum locks out existing users** | Critical if it happened | §2.12 keeps login untightened, and Task 2 carries an explicit regression test that a 10-char account still authenticates. This is the risk to watch |
| **Migration locks out users** by changing the value they authenticate against | High | `Login` normalises (Task 2) *before* the data changes (Task 3), so both sides agree from the moment of the migration. Re-verified as a Task 3 criterion |
| **Blocklist over-rejects**, blocking legitimate strong passwords | Medium — silent user friction | Explicit negative test in Task 1: an ordinary long passphrase must pass |
| Index creation fails on existing collisions | Medium — blocks the migration | Intended (§2.10). Cleanup query in `SPEC.md` §3 |
| `down` cannot restore original capitalisation | Low, accepted | Documented in §7 and in the migration file itself |
| Validation applied to `Login` by reflex | High | §2.12, the "Never do" list, and the regression test |
| Scope creep into rate limiting, Argon2id, or HIBP | Medium | All three deferred **with reasoning** in §7 rather than left unexamined |

## Open questions

**None.** `SPEC.md` §9 is fully resolved.

The one item worth flagging forward rather than treating as settled: **rate limiting is the largest remaining gap in the auth surface** (§7). Nothing throttles credential stuffing against `/auth/login` today. It belongs at the gateway, where Step 7 explicitly deferred it, and is what I would schedule next — ahead of Phase 2 features.
