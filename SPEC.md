# SPEC — QuantSim Auth Input Validation (Phase 1, Step 9)

Status: **Approved 2026-07-30** — open decisions delegated to the implementer with the instruction to decide against cybersecurity standards. §9 records the resolutions, including **three reversals of my own draft** after checking the draft's claims against the current text of NIST SP 800-63B rather than recalling it.
Scope: `services/auth/` input validation, one migration, and a small frontend follow-up. Not a whole-project spec — see `agents.md` and `docs/intent/quantsim-resume.md`. Prior specs archived at `docs/archive/phase1-step4-auth/` through `phase1-step8-frontend/` — all complete.

---

## 1. Objective

Per `PHASE1_CHECKLIST.md` Step 9, close the input-validation gap in the auth service. Found while drafting the Step 8 frontend spec (§2.12 there) and scheduled to land after Phase 1's UI, before Phase 2.

**Every row below was reproduced against the running stack on 2026-07-30, not inferred from reading code.** Registration validates only that `email`, `username`, and `password` are non-empty (`services/auth/internal/handler/auth.go:28`):

| Request | Today | Should be |
|---|---|---|
| `password: "a"` | **201 Created** | 400 |
| `email: "x"` | **201 Created** | 400 |
| `username` of 500 chars | **201 Created** | 400 |
| 80-byte password | **500 internal_error** | 400 |
| Register `a@x.test`, then `A@X.TEST` | **two separate accounts** | 409 duplicate |
| Register `a@x.test`, log in as `A@x.test` | **401 invalid_credentials** | 200 |

The last two are the real motivation. They are not hypotheses — the dev database holds a live case-collision pair created during this investigation.

**Out of scope:** rate limiting and account lockout, password reset / email verification, MFA, changes to token issuance or lifetimes, and anything in Phase 2. Two security items are deliberately deferred with reasoning recorded in §7 rather than silently omitted: migrating off bcrypt to Argon2id, and querying an online breach corpus.

---

## 2. Decisions

### 2.1 Validation moves into the service layer; the handler keeps only transport concerns

Today the non-empty checks sit in `handler.Register`/`handler.Login`. "Is this a well-formed email" and "is this password acceptable" are **domain rules**, not HTTP concerns — a future gRPC entry point, CLI, or seed script must not be able to bypass them by not going through the chi handler.

`service.Register` validates and returns a typed error; the handler maps it to `400` exactly as it already maps `ErrDuplicateUser` to `409`. The handler's non-empty checks are **removed**, not kept alongside — two places enforcing one rule is how they drift out of sync.

Security framing: the goal is a single authoritative choke point that cannot be routed around, not validation sprinkled at every layer.

### 2.2 One new error type, mapped to `400 invalid_request`

`service.ErrInvalidInput` (new, alongside the existing four sentinels), wrapped with a specific message per rule.

**No new error code.** The API keeps returning `{"code": "invalid_request", ...}` with a precise `message`, which the frontend already renders verbatim. A structured per-field body is the better shape for a form with inline per-field errors, but the Step 8 form renders a single error region — it would be built and immediately discarded.

Not an information-disclosure concern: these messages describe the caller's *own* submitted input. The place where response detail *would* leak something is login, which §2.8 deliberately leaves uniform.

### 2.3 Password: minimum 15 characters — **reversed from the draft's 8**

The draft said 8, citing NIST SP 800-63B. Checking the actual text rather than recalling it, that was wrong:

> "Verifiers and CSPs **SHALL** require passwords that are used as a single-factor authentication mechanism to be a minimum of 15 characters in length." — SP 800-63B §3.1.1.2

The 8-character figure is the floor for passwords used **as part of multi-factor authentication**. QuantSim has no second factor, so its password is a single-factor authenticator and 15 is the `SHALL`, not a nice-to-have.

- **Minimum: 15, counted in runes** (`utf8.RuneCountInString`). A user typing 15 emoji has typed 15 characters; counting bytes would accept one 15-character password and reject another, which is indefensible in the UI.
- **No composition rules.** Also a `SHALL NOT`: *"Verifiers and CSPs SHALL NOT impose other composition rules (e.g., requiring mixtures of different character types)"*. No required uppercase, digit, or symbol.

**Consequence, stated plainly:** every existing password fixture in the test suite is 10–14 characters and will need updating, and the `pw12345678` account used throughout Step 8's verification can no longer be *registered*. It can still **log in**, because §2.8 deliberately does not apply these rules to login — which is exactly the property that keeps this change from locking anyone out.

### 2.4 Password maximum: 72 bytes, a documented deviation from the 64-character `SHOULD`

> "Verifiers and CSPs **SHOULD** permit a maximum password length of at least 64 characters." — §3.1.1.2

bcrypt hard-caps at 72 **bytes** — `golang.org/x/crypto v0.49.0` returns `ErrPasswordTooLong` above it (verified in the module source, `bcrypt.go:96`). It does **not** silently truncate, which is the good outcome; silent truncation would mean two different passwords hashing identically.

For ASCII that gives 72 characters, comfortably over the 64 `SHOULD`. For multi-byte scripts it does not: a 64-character password in Cyrillic or CJK exceeds 72 bytes and is rejected. **This is a real, if narrow, deviation and is recorded rather than papered over.** The fix is to stop using bcrypt directly — see §7.

Rejecting is the only acceptable behaviour at the boundary. Truncating to fit is explicitly forbidden (§8).

### 2.5 A blocklist check — **added; the draft omitted a `SHALL` entirely**

> "Verifiers **SHALL** compare the prospective secret against a blocklist that contains known commonly used, expected, or compromised passwords." — §3.1.1.2

The draft had no blocklist at all. That is a mandatory requirement, so it is in scope.

Implementation, sized for Phase 1:
- An embedded list (`//go:embed`) of common and breach-corpus passwords **that are 15+ characters** — anything shorter is already excluded by §2.3, so a generic top-10k list would be almost entirely dead weight. A few hundred entries earns its place; a megabyte does not.
- **Context-specific terms**, which the guidance calls out explicitly: `quantsim`, `trading`, and similar, plus the submitted **username** and the **email local part**. A password containing your own username is exactly the "expected" case the requirement names.
- **Trivial patterns**: a single repeated character, and simple ascending/descending sequences. These defeat a naive length minimum (`aaaaaaaaaaaaaaaa` is 16 characters).
- Compared case-insensitively against the normalised password.

Deliberately **not** an online lookup against Have I Been Pwned. Its k-anonymity API is the stronger control and the right eventual answer, but it puts a third-party network call on the registration path — new latency, a new failure mode, and a decision about whether registration fails open or closed when the service is unreachable. That belongs in its own spec (§7).

### 2.6 Email: `net/mail.ParseAddress`, rejecting display-name forms, capped at 254 bytes

Hand-rolled email regexes are a known trap — they reject valid addresses (plus-tags, new TLDs, quoted local parts) while still admitting nonsense. The stdlib implements RFC 5322 already.

Three checks, in order:
1. `mail.ParseAddress(input)` must succeed.
2. The parsed `addr.Address` must equal the trimmed input. `ParseAddress` accepts `Khalil <a@b.test>`; without this, that whole string would be stored as an email.
3. Length ≤ 254 bytes (RFC 5321's practical maximum).

**Not** requiring a dot in the domain. `user@localhost` is a valid address, and the check is security theatre: `a@b.co` passes it while being equally disposable. Blocking throwaway registrations is rate limiting's job, not format validation's.

### 2.7 Email is normalised to lowercase — the actual bug fix

Trim surrounding whitespace and lowercase the whole address, before both storage and lookup.

The domain part is case-insensitive by RFC. The local part is *technically* case-sensitive, but no mail provider treats `Khalil@` and `khalil@` as different mailboxes, and every product a user has met treats login email as case-insensitive.

The security argument, beyond usability: case-sensitive uniqueness permits **account pre-registration and confusion**. An attacker who sees that `victim@example.com` exists can register `Victim@example.com` — a distinct row for the same real mailbox. Any future email-driven flow (password reset, notifications, support lookup) then has two candidate accounts for one address. Closing it now, before those flows exist, is far cheaper than after.

### 2.8 Case-insensitive uniqueness for **usernames** too — **reversed from the draft's non-goal**

The draft listed this as an explicit non-goal on the grounds that `Khalil` and `khalil` being distinct is "merely unusual, not broken." Under a security lens that reasoning does not hold: it is the **same impersonation class** as §2.9's homograph argument, and dismissing one while fixing the other is inconsistent.

A username is the identity string shown in the dashboard header. If `Admin` and `admin` can coexist, the display is ambiguous about who is who. The cost is one more unique index in the same migration, and **verified: the current database has zero username case-collisions**, so it applies cleanly with no cleanup.

### 2.9 Username: 3–30 characters, `[A-Za-z0-9_-]`

Beyond what the checklist asks for, and kept because the security justification is stronger than the draft credited:

- **Homograph impersonation.** Unrestricted Unicode lets `раypal` (Cyrillic а, р) render indistinguishably from `paypal`. Restricting to ASCII alphanumerics plus `_` and `-` eliminates the entire class rather than trying to detect it.
- **Unbounded input reaching storage and the UI.** 500 characters registers successfully today. Not XSS — React escapes it — but there is no reason to accept it.

The trade, stated honestly: this excludes users who would legitimately want a non-Latin username. For a paper-trading simulator that is the right side of the trade; for a consumer product serving non-Latin scripts it would not be, and the answer there is Unicode normalisation plus a confusable-detection library, not a wider charset.

### 2.10 A unique index on `lower(email)` and `lower(username)`, failing loudly on collisions

App-level normalisation alone is one forgotten `strings.ToLower` from breaking. Migration `004` adds both indexes.

**Creating the email index will fail on any database containing a case-collision.** That is intended: the database refusing to pretend duplicates are fine. The current dev database *does* contain one, so the migration fails there until it is cleared — by hand, using the query in §3. A migration in this project does not silently delete user rows.

The `up` migration lowercases existing emails *before* creating the index, so data and constraint agree. That update is safe precisely because the index creation immediately after would catch any collision it created.

### 2.11 Request bodies are capped at 64 KiB — **added**

Nothing currently bounds a request body. `json.NewDecoder(r.Body).Decode(...)` on the auth routes will read whatever is sent, so a multi-megabyte body is buffered before any validation runs — the length checks in §2.3 cannot help, because they only run after decoding.

`http.MaxBytesReader(w, r.Body, 64<<10)` at the top of each auth handler. 64 KiB is far above any legitimate credential payload and far below anything that matters for memory.

This is a transport concern and stays in the handler, which is consistent with §2.1 rather than an exception to it: the handler owns request shape, the service owns domain rules. Step 7's spec deferred body limits at the gateway; this decision covers only the auth service's own routes and does not pre-empt that.

### 2.12 Login is deliberately **not** tightened

Login validates non-empty and normalises the email for lookup. It does **not** apply the length, blocklist, or format rules.

Two reasons, both load-bearing:

1. **Availability.** Applying a 15-character minimum to login would lock out every account whose password predates this change — including, in the current dev database, accounts with 1-character passwords. A validation change must never revoke existing access.
2. **Information disclosure.** A validation error would tell an attacker their submitted password failed a *policy* check, distinguishable from the uniform "invalid email or password" that `Login` is deliberately written to return for both unknown-email and wrong-password (`auth.go:62-66`). That uniformity, plus the existing dummy-hash timing defence (`auth.go:52`), is what keeps login from being a user-enumeration oracle. Do not undermine it.

Registration enforces policy. Login authenticates whoever already exists.

### 2.13 `bcrypt.ErrPasswordTooLong` is mapped explicitly, even though validation should prevent it

`Register` rejects >72 bytes before hashing, so this should be unreachable. It is mapped to `ErrInvalidInput` anyway: if a future path ever reaches `GenerateFromPassword` without validating, the user should see a `400`, not today's `500`. Cheap defence in depth against exactly the class of bug this step fixes.

### 2.14 The frontend hint states the real rule

`LoginPage.tsx` currently reads *"Use 8 or more characters for a stronger password."* — softened in Step 8 precisely because the server enforced nothing. It becomes *"At least 15 characters."*

No client-side enforcement is added; the server's `{code, message}` remains what gets displayed (Step 8 §2.12). The client is a hint, never the boundary.

---

## 3. Commands

**Before migrating**, find case-collisions the new index will reject:

```bash
psql "$DATABASE_URL" -c \
  "SELECT lower(email) AS normalized, count(*), array_agg(email) FROM users GROUP BY 1 HAVING count(*) > 1;"
```

If rows come back, decide which to keep and delete the others by `id`. The dev database currently returns one collision, created while investigating; either row may go.

Verification — each currently gives the wrong answer:

```bash
# 400, not 201  (14 chars, one under the minimum)
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"a@b.test","username":"alice","password":"fourteen-chars"}'

# 201  (15 chars, exactly at the minimum)
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"ok@b.test","username":"alice2","password":"fifteen-chars-x"}'

# 400 blocklist: contains the username
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"c@b.test","username":"alice","password":"alice-alice-alice"}'

# 400 blocklist: single repeated character
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"d@b.test","username":"bob","password":"aaaaaaaaaaaaaaaa"}'

# 400, not 201  (malformed email)
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"x","username":"carol","password":"a-valid-long-password"}'

# 400, not 500  (80-byte password)
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d "{\"email\":\"e@b.test\",\"username\":\"dave\",\"password\":\"$(python3 -c 'print("a"*80)')\"}"

# 400, not 201  (500-char username)
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d "{\"email\":\"f@b.test\",\"username\":\"$(python3 -c 'print("u"*500)')\",\"password\":\"a-valid-long-password\"}"

# 413/400, not a buffered 10 MB  (body cap, §2.11)
python3 -c 'print("{\"email\":\"g@b.test\",\"username\":\"x\",\"password\":\"" + "a"*10_000_000 + "\"}")' \
  | curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' --data-binary @-

# 409, not a second account
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"case@b.test","username":"c1","password":"a-valid-long-password"}'
curl -i -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"CASE@B.TEST","username":"c2","password":"a-valid-long-password"}'

# 200, not 401  (different capitalisation than registration)
curl -i -X POST localhost:8080/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"Case@B.test","password":"a-valid-long-password"}'

# 200 — an EXISTING short-password account still logs in (§2.12). The check
# that proves this change locks nobody out.
curl -i -X POST localhost:8080/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"khalil-ui-check@quantsim.test","password":"pw12345678"}'
```

---

## 4. Project structure

```
services/auth/internal/service/
  validate.go        # NEW — NormalizeEmail, ValidateRegistration; the only
                     #   place the rules live (§2.1)
  validate_test.go   # NEW — table-driven; the bulk of this step's tests
  blocklist.go       # NEW — embedded list + pattern checks (§2.5)
  blocklist.txt      # NEW — 15+ char common/breach entries, //go:embed
  errors.go          # + ErrInvalidInput (§2.2)
  auth.go            # Register: validate + normalise before hashing.
                     #   Login: normalise email only (§2.12).
                     #   Map bcrypt.ErrPasswordTooLong (§2.13)
  auth_test.go       # + rejection cases; ALL existing password fixtures
                     #   updated to 15+ chars (§2.3)

services/auth/internal/handler/
  auth.go            # MaxBytesReader (§2.11); remove non-empty checks;
                     #   map ErrInvalidInput -> 400
  auth_test.go       # + status coverage; fixtures updated

infra/migrations/
  004_case_insensitive_identity.up.sql    # lowercase emails, then unique
                                          #   indexes on lower(email) and
                                          #   lower(username) (§2.10)
  004_case_insensitive_identity.down.sql  # drop both indexes (§7)

frontend/src/auth/
  LoginPage.tsx      # hint -> "At least 15 characters." (§2.14)
```

No changes to `pkg/`, the gateway, market-data, or token issuance.

---

## 5. Code style / conventions

- **Layering:** validation is a pure function in `internal/service` — no HTTP, no database, no context. Trivially testable, which is the point of putting it there.
- **Errors:** `fmt.Errorf("%w: password must be at least 15 characters", ErrInvalidInput)` so the handler matches with `errors.Is` while the message stays specific. Matches how the existing four sentinels are used.
- **Messages are user-facing** — rendered verbatim by the frontend, so they read as instructions, not diagnostics.
- **Normalisation happens once**, at the top of `Register` and `Login`, before anything else touches the value. Never scattered into the store.
- **Never log** a password, its length, a blocklist hit, or any rejected value. A log line saying "password rejected: too short" for a known email is itself a small disclosure.
- Migrations follow the existing `NNN_name.up.sql` / `.down.sql` pair convention.

---

## 6. Testing strategy

Unlike Step 8, this step **ships tests** — it is exactly the logic-with-invariants that Steps 4–7 covered. Table-driven, hand-written fakes, matching `docs/TESTING_STRUCTURE.md`.

- **`validate_test.go`** — the core. Boundaries on both sides: password at 14/15 runes and 72/73 bytes; **a 15-rune password that exceeds 15 bytes** (proves the rune/byte split of §2.3–2.4 is real, not incidental); emails valid, malformed, display-name form, and over 254 bytes; usernames at 2/3 and 30/31 and containing a disallowed character.
- **Blocklist** — an exact entry, a password containing the username, one containing the email local part, a single repeated character, and a simple sequence. Plus a **negative** case: a long, ordinary passphrase must pass, so the checks are not so broad they reject good passwords.
- **`NormalizeEmail`** — mixed case, surrounding whitespace, already-normalised, and idempotence asserted rather than assumed.
- **Service** — `Register` rejects each invalid input **before touching the store** (assert the mock recorded no write; this is what proves validation runs first rather than the database happening to reject it). `Register` stores the normalised email. `Login` finds a user registered in a different case.
- **Regression, the most important test in the step:** a user whose stored password is 10 characters — below the new minimum — can still log in (§2.12).
- **Handler** — each rejection is `400` with `code: "invalid_request"`; the happy path still returns `201`; an oversized body is rejected without being buffered.
- **Not unit-tested:** the migration. Verified manually per §3 against a throwaway database first, the same approach used for Phase 1's handoff.

---

## 7. Deferred with reasoning, not omitted

Four items a security review would reasonably raise, each deliberately out of this step:

1. **bcrypt → Argon2id.** OWASP's Password Storage guidance prefers Argon2id for new systems, and it would remove the 72-byte cap that forces the §2.4 deviation. Out of scope because it changes the stored hash format and needs a rehash-on-login migration path for existing users — a separate spec, not a rider on input validation. bcrypt at cost 10 meets OWASP's stated minimum in the meantime.
2. **Online breach-corpus lookup (HIBP k-anonymity).** Strictly stronger than the embedded list in §2.5 and the right eventual answer. Deferred because it adds a third-party network call to registration, with its own latency, failure mode, and a fail-open/fail-closed decision that deserves deciding on purpose.
3. **Rate limiting and account lockout.** The single largest remaining gap in the auth surface: nothing throttles credential stuffing against `/auth/login` today. Genuinely out of scope here — it belongs at the gateway, where Step 7 explicitly deferred it — but it is the item I would put next after this step, ahead of Phase 2 features.

4. **Unicode normalisation (NFC/NFKC) of passwords.** Added during the Task 1 review, having been missed in the original §9 pass. SP 800-63B §3.1.1.2 — the same section this spec cites throughout — says verifiers *SHOULD* apply NFKC or NFC normalisation before hashing. We do not. Verified rather than assumed: the same visually identical passphrase entered precomposed vs decomposed is **22 vs 25 bytes**, and both are accepted today, so they hash differently and the user is locked out of their own account depending on how they typed it. Out of scope here because it changes what gets hashed, which is §7's item 1 territory. **Worth doing early for a specific reason:** it is free while no non-ASCII password exists, and becomes a lockout event once one does — normalising later changes the hash of every password already stored in a non-normalised form.

Also accepted: `004.down.sql` drops both indexes but **cannot** restore the original capitalisation of emails the `up` lowercased. That information is gone. A backup column to preserve it is real complexity to protect data whose only distinguishing feature is capitalisation nobody wants. Noted in the migration file itself so the next reader is not surprised.

---

## 8. Boundaries

**Always do:**
- Validate in the service layer, before hashing and before any store call (§2.1)
- Count the password minimum in runes and the maximum in bytes (§2.3, §2.4)
- Normalise email before both storage and lookup (§2.7)
- Keep `Login`'s failure response uniform — never let a validation message distinguish "bad format" from "wrong password" (§2.12)
- Cap request bodies before decoding (§2.11)
- Run `go test -count=1 ./...` in `services/auth` and `pkg` before flagging a checkpoint done

**Ask first:**
- Adding password composition rules (§2.3 — forbidden by `SHALL NOT`)
- Adding a new error `code` or a structured per-field body (§2.2)
- Any change to token lifetimes, issuance, or the login response shape
- Anything that would make an existing user unable to log in (§2.12)
- Deleting user rows in a migration (§2.10 — cleanup is manual and documented)
- Adding an external network dependency to registration (§2.5, §7)

**Never do:**
- Log a password, its length, a blocklist hit, or a rejected value
- Apply the registration rules to `Login` (§2.12)
- Hand-roll an email regex in place of `net/mail` (§2.6)
- **Truncate a password to fit bcrypt's 72 bytes** — reject it (§2.4). Truncation makes distinct passwords hash identically
- Let validation live in two places at once (§2.1)

---

## 9. Resolutions

Khalil delegated the open decisions on 2026-07-30 with the instruction to decide against cybersecurity standards. Resolved by checking the draft's claims against the current text of **NIST SP 800-63B §3.1.1.2** rather than recalling it — which reversed three of my own draft decisions:

- [x] **Reversed (§2.3):** minimum raised from **8 to 15 characters**. The draft cited 8 as NIST's requirement. The actual text makes 15 a `SHALL` for single-factor authentication; 8 applies only when the password is one factor of *several*. QuantSim has no MFA. Cost: every existing test fixture (10–14 chars) needs updating.
- [x] **Added (§2.5):** a **blocklist check**, which the draft omitted altogether. *"Verifiers SHALL compare the prospective secret against a blocklist of known commonly used, expected, or compromised passwords."* Embedded list scoped to 15+ char entries, plus context-specific terms and trivial patterns.
- [x] **Reversed (§2.8):** **case-insensitive usernames** move from explicit non-goal to in scope. The draft dismissed it as cosmetic while simultaneously arguing homograph impersonation to justify §2.9 — inconsistent. Same attack class, and the database has zero username collisions, so it applies cleanly.
- [x] **Added (§2.11):** **64 KiB request body cap.** Nothing bounded body size; length validation runs *after* decoding, so it cannot help. Multi-megabyte bodies were buffered unconditionally.
- [x] **Added (§2.4):** the 72-byte bcrypt cap is recorded as a **documented deviation** from the `SHOULD permit at least 64 characters`, since a 64-character multi-byte password exceeds it. Was previously unexamined.
- [x] §2.1 — validation in the service layer; handler's duplicate checks removed — as drafted
- [x] §2.2 — reuse `invalid_request` / `400`; no new code, no per-field body — as drafted
- [x] §2.3 — **no composition rules** (`SHALL NOT`) — as drafted, now with the citation
- [x] §2.6 — `net/mail.ParseAddress`, reject display-name forms, 254-byte cap; **no** dot-in-domain requirement (security theatre — `a@b.co` passes it and is equally disposable) — as drafted
- [x] §2.7 — email lowercased and trimmed; the fix for both §1 bugs — as drafted, with the account-confusion argument made explicit
- [x] §2.9 — username 3–30, `[A-Za-z0-9_-]`; **kept**, on a stronger justification than the draft gave (homograph impersonation, not just tidiness)
- [x] §2.10 — migration adds both indexes and **fails loudly** until the existing collision is cleared by hand — as drafted
- [x] §2.12 — login **not** tightened; availability plus the uniform-failure property — as drafted, now with the enumeration-oracle reasoning spelled out
- [x] §2.13 — map `bcrypt.ErrPasswordTooLong` to `400` — as drafted
- [x] §2.14 — frontend hint states the real rule, now 15 — as drafted
- [x] §6 — this step ships tests, including a **regression test that an existing short-password account can still log in**
- [x] §7 — three items deferred *with reasoning on the record*: Argon2id, online breach lookup, and rate limiting. **Rate limiting is the largest remaining gap in the auth surface** and is what I would do next, ahead of Phase 2 features.

Checkpoint slicing lives in `tasks/plan.md`.
