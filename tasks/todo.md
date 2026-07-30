# QuantSim Auth Input Validation — Task Checklist (Phase 1, Step 9)

> **BLOCKED — `SPEC.md` §9 is awaiting review.** Twelve proposed decisions plus one
> explicit non-goal. Nothing starts until they are accepted or reversed.
>
> The two most worth your attention: **§2.7** (username validation goes beyond
> what the checklist asks for — cut it if you want this step kept tight) and
> **§2.6** (the migration will deliberately fail until a case-collision already
> sitting in the dev database is cleared by hand).

Full detail (acceptance criteria, verification steps, dependency graph, risks) in `tasks/plan.md`.

Prior steps archived: Auth Service (Step 4) at `docs/archive/phase1-step4-auth/`; Market Data historical ingestion (Step 5) at `docs/archive/phase1-step5-market-data/`; Market Data live polling (Step 6) at `docs/archive/phase1-step6-market-data-live/`; API Gateway (Step 7) at `docs/archive/phase1-step7-gateway/`; Minimal Frontend (Step 8) at `docs/archive/phase1-step8-frontend/` — all complete.

Each task is a stop-for-review checkpoint per `agents.md`: implement, verify, **stop**.

### Phase 1: The rules
- [ ] **Task 1** — `validate.go` + `validate_test.go`: `NormalizeEmail`, `ValidateRegistration`, `ErrInvalidInput`. Pure functions, no DB or HTTP

### Phase 2: Wiring
- [ ] **Task 2** — Enforce in `Register`/`Login`, map `ErrInvalidInput` → `400` in the handler, remove the handler's duplicate non-empty checks

- [ ] ✅ **Checkpoint: Rules enforced** — short/long password, bad email, long username all `400`; valid registration still `201`. Case-duplicates *not* fixed yet — that is Task 3

### Phase 3: The database
- [ ] **Task 3** — Migration `004`: lowercase existing emails, then unique index on `lower(email)`. Highest-risk task; dry-run up **and** down on a throwaway DB first

- [ ] ✅ **Checkpoint: Database aligned** — no duplicate accounts, no case lockout, **every pre-existing user can still log in**

### Phase 4: Close out
- [ ] **Task 4** — Frontend hint back to "At least 8 characters."; check off Step 9

- [ ] ✅ **Checkpoint: Complete** — Phase 1 fully closed; next is Phase 2 (Trading Engine)

---

## Why this step exists

Every row below was reproduced against the running stack on 2026-07-30, not inferred from reading code:

| Request | Today |
|---|---|
| `password: "a"` | 201 Created |
| `email: "x"` | 201 Created |
| 500-char username | 201 Created |
| 80-byte password | **500** (should be 400) |
| Same email, different case | **two separate accounts** |
| Login with different case | **401 lockout** |

The last two are the real motivation. They are live in the dev database right now.

---

Every checkpoint runs `go test -count=1 ./...` in `services/auth` and `pkg`. Unlike Step 8, **this step ships tests** — it is exactly the logic-with-invariants that Steps 4–7 tested (`SPEC.md` §6).

Step 9 docs (`SPEC.md`, `tasks/plan.md`, `tasks/todo.md`) stay at the repo root until Phase 2's spec is drafted, then move to `docs/archive/phase1-step9-auth-validation/` — matching how Steps 4→5 through 8→9 were archived.
