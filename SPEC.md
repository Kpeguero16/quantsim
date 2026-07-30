# SPEC — QuantSim Auth Input Validation (Phase 1, Step 9)

Status: **Draft 2026-07-30** — awaiting architect review. All decisions in §2 are proposed; §9 lists them for accept/reverse.
Scope: `services/auth/` input validation, plus one migration and a two-line frontend follow-up. Not a whole-project spec — see `agents.md` and `docs/intent/quantsim-resume.md` for that context. Prior specs archived at `docs/archive/phase1-step4-auth/`, `phase1-step5-market-data/`, `phase1-step6-market-data-live/`, `phase1-step7-gateway/`, `phase1-step8-frontend/` — all complete.

---

## 1. Objective

Per `PHASE1_CHECKLIST.md` Step 9, close the input-validation gap in the auth service. This was found while drafting the Step 8 frontend spec (that spec's §2.12) and scheduled to land after Phase 1's UI and before Phase 2's trading engine.

**Everything below was reproduced against the running stack on 2026-07-30, not inferred from reading code.** The registration path validates only that `email`, `username`, and `password` are non-empty (`services/auth/internal/handler/auth.go:28`), and nothing else:

| Request | Today | Should be |
|---|---|---|
| `password: "a"` | **201 Created** | 400 |
| `email: "x"` | **201 Created** | 400 |
| `username` of 500 chars | **201 Created** | 400 |
| 80-byte password | **500 internal_error** | 400 |
| Register `a@x.test`, then `A@X.TEST` | **two separate accounts** | 409 duplicate |
| Register `a@x.test`, log in as `A@x.test` | **401 invalid_credentials** | 200 |

The last two are the reason this step is worth doing properly rather than bolting a length check on. They are not hypotheses — the current dev database contains a live case-collision pair created during this investigation (`SELECT lower(email), count(*) FROM users GROUP BY 1 HAVING count(*) > 1` returns a row).

**Out of scope:** password composition rules (§2.3), rate limiting or lockout on repeated login failures, password reset / email verification flows, any change to token issuance or lifetimes, and anything in the trading engine (Phase 2).

---

## 2. Decisions

### 2.1 Validation moves into the service layer; the handler keeps only transport concerns

Today the non-empty checks sit in `handler.Register`/`handler.Login`. "Is this a well-formed email" and "is this password long enough" are **domain rules**, not HTTP concerns — a future gRPC entry point, CLI, or seed script should not be able to bypass them by not going through the chi handler.

So: `service.Register` validates and returns a typed error; the handler maps that error to `400` exactly as it already maps `ErrDuplicateUser` to `409`. This follows the established pattern rather than inventing one.

The handler's existing non-empty checks are **removed**, not kept alongside — two places enforcing the same rule is how they drift. The handler still rejects malformed JSON, which genuinely is a transport concern.

Consequence worth stating: the service layer becomes the single place to read to know what a valid registration is. That is the point.

### 2.2 One new error type, mapped to `400 invalid_request`

`service.ErrInvalidInput` (new, in `internal/service/errors.go` alongside the existing four), wrapping a specific message per rule.

**No new error code.** The API keeps returning `{"code": "invalid_request", ...}` with a precise `message`, which is what the existing malformed-JSON path already returns and what the frontend already renders verbatim (`LoginPage.tsx` displays `ApiError.message`). Introducing a `validation_failed` code would mean a frontend change for no user-visible gain.

Rejected alternative: a structured `{"code": "...", "fields": {"password": "too short"}}` body. It is the better shape for a form with per-field inline errors — but the Step 8 form renders a single error region (SPEC §2.12), so the extra structure would be built and immediately discarded. Revisit if the form ever grows per-field errors.

### 2.3 Password: minimum 8 characters, maximum 72 bytes — and the asymmetry is deliberate

- **Minimum: 8, counted in runes** (`utf8.RuneCountInString`). A user typing 8 emoji has typed 8 characters; counting bytes would tell them their 8-character password is fine while a different 8-character password is rejected, which is indefensible from the UI.
- **Maximum: 72, counted in bytes.** This is not a policy choice — it is bcrypt's hard limit. `golang.org/x/crypto v0.49.0` returns `ErrPasswordTooLong` above it (verified in the module source at `bcrypt.go:96`); it does **not** silently truncate, which is the good outcome. Since the limit is on the byte length bcrypt receives, the check must be in bytes.

**No composition rules** — no required uppercase, digit, or symbol. NIST SP 800-63B explicitly recommends against them: they push users toward predictable substitutions (`Password1!`) while reducing usability. Length is the control that matters, and 8 is the floor that guidance sets.

### 2.4 Email: `net/mail.ParseAddress`, rejecting display-name forms, capped at 254 bytes

Hand-rolled email regexes are a well-known trap — they reject valid addresses (plus-tags, new TLDs, quoted local parts) while still admitting nonsense. The stdlib already implements RFC 5322 parsing.

Three checks, in order:
1. `mail.ParseAddress(input)` must succeed.
2. The parsed `addr.Address` must equal the trimmed input. `ParseAddress` happily accepts `Khalil <a@b.test>`; without this, that string would be stored as an email.
3. Length ≤ 254 bytes (RFC 5321's practical maximum for a forward path).

**Not** requiring a dot in the domain: `user@localhost` is a valid address, and the check adds nothing an attacker cares about. Flagged in §9 in case you want the stricter behaviour for a consumer-facing product.

### 2.5 Email is normalised to lowercase, and this is the real fix

**This is the most consequential decision in the step.** Trim surrounding whitespace, lowercase the whole address, before both storage and lookup.

The domain part of an email is case-insensitive by RFC. The local part is *technically* case-sensitive, but no mail provider in practice treats `Khalil@` and `khalil@` as different mailboxes, and every product users have ever used treats login email as case-insensitive. Today QuantSim does not, which produces the two failures in §1: a user who signs up as `Khalil@X.test` cannot log in as `khalil@X.test`, and a second person can register what is, in reality, the same address.

Lowercasing the whole address is the pragmatic, universally-expected behaviour. The alternative — lowercasing only the domain — is more RFC-pure and strictly worse for users.

### 2.6 A unique index on `lower(email)` enforces it at the database, and will fail loudly on existing collisions

App-level normalisation alone is one forgotten `strings.ToLower` away from breaking. Migration `004` adds:

```sql
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
```

**Creating this index will fail on any database that already contains a case-collision.** That is intended, not a flaw: the failure is the database refusing to pretend the duplicates are fine. The current dev database *does* contain one (created during this investigation), so the migration will fail there until it is cleared.

The migration is therefore paired with a documented cleanup query (§3) rather than an automatic `DELETE` — silently destroying user rows in a migration is not something this project should do, even in dev.

Also folded in: the `up` migration lowercases existing emails (`UPDATE users SET email = lower(email)`) *before* creating the index, so the data and the constraint agree. That update is safe precisely because the index creation immediately after would catch any collision it created.

### 2.7 Username: 3–30 characters, `[A-Za-z0-9_-]` — beyond the checklist, flagged for approval

`PHASE1_CHECKLIST.md` Step 9 asks only for password and email rules. Username is proposed as an addition because it is the same validation pass, the same commit, and today a 500-character username registers successfully (§1) and is rendered in the dashboard header.

Not a security hole — React escapes it, so there is no injection — but it is unbounded input that reaches the UI, and "3–30, alphanumeric plus `_` and `-`" is the least surprising rule a user could meet. **Cut this from the step if you would rather keep it strictly to the checklist** (§9).

**Not** proposed: case-insensitive uniqueness for usernames. It would need a second index and its own collision cleanup, and unlike email there is no correctness argument — `Khalil` and `khalil` being distinct usernames is merely unusual, not broken. Listed in §9 as an explicit non-goal so it is a decision rather than an oversight.

### 2.8 Login is deliberately *not* tightened

Login validates non-empty and normalises the email for lookup. It does **not** apply the length or format rules.

This matters. Applying the new minimum to login would lock out every existing account whose password predates this change — including, in the current dev database, accounts with 1-character passwords. Worse, it would answer with a *validation* error, telling an attacker that the submitted password failed a policy check rather than the uniform "invalid email or password" that `Login` is carefully written to return for both unknown-email and wrong-password (`auth.go:62-66`).

Registration is where the policy is enforced. Login's job is to authenticate whoever already exists.

### 2.9 `bcrypt.ErrPasswordTooLong` is mapped explicitly, even though validation should prevent it

`service.Register` will reject >72 bytes before hashing, so the bcrypt error should be unreachable. It gets mapped to `ErrInvalidInput` anyway — if some future path reaches `GenerateFromPassword` without validating, the user should still see a `400`, not the `500` they get today.

Cheap defence in depth against exactly the class of bug this step is fixing.

### 2.10 The frontend hint becomes a stated rule again

`LoginPage.tsx` currently reads *"Use 8 or more characters for a stronger password."* — deliberately softened in Step 8 because the server enforced nothing (`docs/archive/phase1-step8-frontend/SPEC.md` §2.12). Once the server enforces it, the honest wording returns: *"At least 8 characters."*

No client-side enforcement is added. The server's `{code, message}` remains what gets displayed, per Step 8 §2.12.

---

## 3. Commands

Prerequisites — the stack running (`make docker-up`, then `make run-auth` / `run-market-data` / `run-gateway`).

**Before migrating**, check for case-collisions the new index would reject:

```bash
psql "$DATABASE_URL" -c \
  "SELECT lower(email) AS normalized, count(*), array_agg(email) FROM users GROUP BY 1 HAVING count(*) > 1;"
```

If that returns rows, decide which row to keep and remove the others by `id` before running `make migrate-up`. In the current dev database this returns one collision, created while investigating; deleting either row is fine.

Verification — each of these currently produces the wrong answer and must produce the right one:

```bash
# 400, not 201
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"a@b.test","username":"alice","password":"short"}'

# 400, not 201
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"x","username":"alice","password":"pw12345678"}'

# 400, not 500  (80-byte password)
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d "{\"email\":\"a@b.test\",\"username\":\"alice\",\"password\":\"$(python3 -c 'print("a"*80)')\"}"

# 400, not 201  (500-char username)
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d "{\"email\":\"a@b.test\",\"username\":\"$(python3 -c 'print("u"*500)')\",\"password\":\"pw12345678\"}"

# register, then the same address in a different case -> 409, not a second account
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"case@b.test","username":"c1","password":"pw12345678"}'
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"CASE@B.TEST","username":"c2","password":"pw12345678"}'

# log in with different capitalisation than registration -> 200, not 401
curl -i -X POST localhost:8080/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"Case@B.test","password":"pw12345678"}'

# still works: a valid registration
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"fine@b.test","username":"alice","password":"pw12345678"}'
```

Then the Step 8 UI still behaves: register with a 4-character password and confirm the form shows the **server's** message, not a client-invented one.

---

## 4. Project structure

Modified:

```
services/auth/internal/service/
  validate.go        # NEW — ValidateRegistration, NormalizeEmail; the only
                     #   place the rules live (§2.1)
  validate_test.go   # NEW — table-driven, the bulk of this step's tests
  errors.go          # + ErrInvalidInput (§2.2)
  auth.go            # Register: validate + normalise before hashing;
                     #   Login: normalise email before lookup (§2.8);
                     #   map bcrypt.ErrPasswordTooLong (§2.9)
  auth_test.go       # + cases for the new rejections

services/auth/internal/handler/
  auth.go            # remove the non-empty checks (§2.1); map
                     #   ErrInvalidInput -> 400 invalid_request
  auth_test.go       # + status-code coverage for the new 400s

infra/migrations/
  004_email_case_insensitive.up.sql    # lowercase existing, then unique
                                       #   index on lower(email) (§2.6)
  004_email_case_insensitive.down.sql  # drop the index (the lowercasing is
                                       #   not reversible — see §7)

frontend/src/auth/
  LoginPage.tsx      # hint reverts to a stated rule (§2.10)
```

No changes to `pkg/`, the gateway, market-data, or token issuance.

---

## 5. Code style / conventions

- **Layering:** validation is a pure function in `internal/service` with no HTTP, no database, and no context — trivially testable, which is the point of putting it there.
- **Errors:** `ErrInvalidInput` wrapped with `fmt.Errorf("%w: password must be at least 8 characters", ErrInvalidInput)` so the handler matches with `errors.Is` while the message stays specific. Matches how the existing four sentinel errors are used.
- **Messages are user-facing.** They are rendered verbatim by the frontend, so they read as instructions ("Password must be at least 8 characters"), not as internal diagnostics.
- **Normalisation happens once**, at the top of `Register` and `Login`, before anything else touches the value. Not scattered into the store.
- **Migrations** follow the existing pair convention (`NNN_name.up.sql` / `.down.sql`) and the numbering continues from `003`.
- **Never log** a password, a password length, or a validation failure containing the submitted value.

---

## 6. Testing strategy

Unlike Step 8, this step **does** ship tests — it is exactly the logic-with-invariants that Steps 4–7 tested and that `agents.md` calls for. Table-driven, hand-written fakes, matching `docs/TESTING_STRUCTURE.md` and the existing `services/auth/internal/service/auth_test.go` conventions.

- **`validate_test.go`** — the core. One table per rule, each with the boundary on both sides: password at 7/8 runes and 72/73 bytes; a multi-byte password that is 8 runes but >8 bytes (proves the rune/byte split of §2.3); emails that are valid, malformed, display-name form, and over 254 bytes; usernames at 2/3 and 30/31 characters and with a disallowed character.
- **`NormalizeEmail`** — mixed case, surrounding whitespace, already-normalised, and the idempotence property (normalising twice equals normalising once).
- **`auth_test.go` (service)** — `Register` rejects each invalid input *before* touching the store (assert the mock store recorded no write — this is what proves validation runs first rather than the database happening to reject it); `Register` stores the normalised email; `Login` finds a user registered in a different case.
- **`auth_test.go` (handler)** — each rejection surfaces as `400` with `code: "invalid_request"`, and the happy path still returns `201`.
- **Not covered by tests:** the migration. Verified manually per §3 against a scratch database, the same way Step 8's migrations were checked — `migrate up` then `down` on a throwaway DB before touching the real one.
- `go test ./...` passes in `services/auth` and `pkg` before any checkpoint is marked done.

---

## 7. Resolved: the `down` migration is not fully reversible, and that is accepted

`004.down.sql` drops the unique index. It **cannot** restore the original capitalisation of emails that `004.up.sql` lowercased — that information is gone.

The alternative (a backup column preserving the original casing) is real complexity for a dev-stage project, to protect data whose only distinguishing feature is capitalisation nobody wants. Documented in the migration file itself so the next reader is not surprised, and called out here rather than discovered later.

---

## 8. Boundaries

**Always do:**
- Validate in the service layer, before hashing and before any store call (§2.1)
- Count the password minimum in runes and the maximum in bytes (§2.3)
- Normalise email before both storage and lookup (§2.5)
- Return `400` with `code: "invalid_request"` and a user-readable message (§2.2)
- Keep `Login`'s failure response uniform — never let a validation message distinguish "bad password format" from "wrong password" (§2.8)
- Run `go test ./...` in `services/auth` before flagging a checkpoint done

**Ask first:**
- Adding password composition rules (§2.3 — deliberately excluded)
- Adding a new error `code` or a structured per-field error body (§2.2)
- Case-insensitive usernames (§2.7 — explicit non-goal)
- Any change to token lifetimes, issuance, or the login response shape
- Anything that would make an existing user unable to log in (§2.8)
- Deleting user rows as part of a migration (§2.6 — the cleanup is manual and documented)

**Never do:**
- Log a password, its length, or a rejected value
- Apply the new registration rules to `Login` (§2.8)
- Hand-roll an email regex in place of `net/mail` (§2.4)
- Silently truncate a password to fit bcrypt's 72 bytes — reject it instead (§2.3)
- Let validation live in two places at once (§2.1)

---

## 9. Confirm before I start

Proposed by me — please accept or reverse:

- [ ] **§2.1** — Validation moves to the service layer; the handler's non-empty checks are removed rather than kept alongside
- [ ] **§2.2** — Reuse `invalid_request` / `400` with a specific message; no new error code, no per-field error body
- [ ] **§2.3** — Password min 8 **runes**, max 72 **bytes**; no composition rules (NIST SP 800-63B)
- [ ] **§2.4** — `net/mail.ParseAddress` + reject display-name forms + 254-byte cap; **no** requirement that the domain contain a dot (so `user@localhost` stays valid — say if you want it stricter)
- [ ] **§2.5** — Email lowercased and trimmed for both storage and lookup. **This is the fix for the duplicate-account and case-lockout bugs in §1**
- [ ] **§2.6** — Migration `004` lowercases existing emails then adds a unique index on `lower(email)`; it **will fail** until the existing collision in the dev database is cleared by hand (query in §3). Failing loudly is the intent
- [ ] **§2.7** — Username 3–30 chars, `[A-Za-z0-9_-]`. **Beyond the checklist** — cut it if you want this step kept strictly to password and email
- [ ] **§2.8** — Login is *not* tightened: non-empty plus email normalisation only, so no existing account is locked out and the uniform failure message is preserved
- [ ] **§2.9** — Map `bcrypt.ErrPasswordTooLong` to `400` as unreachable-but-cheap defence in depth
- [ ] **§2.10** — Frontend hint reverts to "At least 8 characters." once the server enforces it
- [ ] **§6** — This step ships tests, unlike Step 8; `validate_test.go` is the bulk of them
- [ ] **§7** — The `down` migration does not restore original email capitalisation, and that is accepted

Explicit non-goal, listed so it is a decision rather than an omission:

- [ ] Case-insensitive **username** uniqueness (§2.7) — needs its own index and cleanup, and no correctness argument. Confirm you are happy leaving `Khalil` and `khalil` as distinct usernames.

Checkpoint slicing lives in `tasks/plan.md`, mirroring how Steps 5–8 were sliced.
