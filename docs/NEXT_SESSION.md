# Next session — state of play

Last updated **2026-08-07**, at the end of the session that shipped Steps 9 and 10.

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Nothing is in flight

Everything is committed and pushed. There is no work to recover.

| | |
|---|---|
| Branch | `main`, clean, in sync with `origin/main` |
| HEAD | `4300b92` — *test(auth): cover InvalidInputMessage; record the index-lock trade* |
| Migrations | schema at version **5**, **not dirty** |
| Local branches | only `main` (two merged branches deleted) |
| Tests | green: `services/auth`, `pkg` |

**Phase 1 is complete**, including Step 9 (auth input validation) and Step 10 (the fixes from Step 9's review). See `PHASE1_CHECKLIST.md`.

`tasks/plan.md` and `tasks/todo.md` still describe **Step 10, fully checked off**. That is deliberate — by convention they are archived to `docs/archive/phase1-step10-identity-lookup/` when the *next* spec is drafted, not before. `SPEC.md` is likewise still Step 10's.

---

## What to do next, in order

### 1. Rate limiting on the auth routes — the largest remaining gap

`docs/security-backlog.md` item 1. Nothing throttles `/auth/login` today. Every control Step 9 added raises the cost **per guess**; none bounds the **number** of guesses, which is what actually defeats credential stuffing against reused passwords.

It belongs at the gateway, where Step 7 deferred it. The backlog entry has the shape, including a detail worth not rediscovering: the gateway already calls `r.SetXForwarded()`, which *replaces* any inbound `X-Forwarded-For`, so a per-IP limiter can trust that header — the usual reason naive limiters are bypassable does not apply.

Watch out for the trap recorded there: a per-account lockout is itself a denial-of-service vector, and the response must never distinguish "locked" from "wrong password", which would undo the uniform-failure property Step 9 §2.12 protects.

### 2. A store-layer integration harness

`internal/store/` has **no tests at all**. Both the service and handler suites run against `mock.UserStore`, which is a Go map — they would stay green with a completely wrong SQL query. Step 10's central fix lives in exactly that layer and was verified by hand, which proves it today and protects nothing tomorrow.

`docs/TESTING_STRUCTURE.md` §4 sketches the shape (`services/auth/integration/`, `-tags=integration`, a real Postgres). The open decision is the harness: testcontainers vs. the existing docker-compose, and how CI gets a database.

**Do this before Phase 2**, which will add far more SQL than auth ever had.

### 3. Phase 2 — Trading Engine

Order execution, trade history, P/L tracking. Note `docs/security-backlog.md`'s argument for doing items 1, 2, 4 and 8 *alongside* it rather than after: Phase 2 is what makes account takeover consequential, since `/trading/*` moves a $100k simulated balance.

Per `agents.md`, start with a spec, get it reviewed, then build to checkpoints.

---

## Restarting the environment

```bash
make docker-up        # Postgres + Redis
make run-auth         # :8081
make run-gateway      # :8080
make run-frontend     # :5173
```

Each `run-*` target runs in the foreground, so they need separate terminals.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty database named `quantsim` also exists. Running `psql -d quantsim` connects successfully and shows no `users` table, which reads like data loss and is not. Use `-d postgres`, or better, `"$DATABASE_URL"`. This cost real confusion once — a manual `DELETE` appeared to do nothing because it was aimed at the wrong database. Renaming is filed in `SPEC.md` §7 as worth doing at a natural break.

**`migrate` lives at `~/go/bin/migrate` and is not on a non-interactive shell's PATH.** Use `make migrate-up` from an interactive shell, or the full path.

**A failed migration leaves the schema dirty.** Recovery is `make migrate-force VERSION=<n>` at the last good version, then fix the cause and re-run. Step 9's `004` does this deliberately when case-collisions exist.

**Restart the auth service after changing its code.** It runs under `go run`, so a running instance keeps serving the old binary. This silently happened for an entire step: `:8081` was still accepting one-character passwords while three commits of validation sat on disk. If behaviour does not match the code, check this first.

**The unit suites cannot see store-layer changes.** See item 2 above. Do not read a green suite as coverage of anything in `internal/store/`.

---

## Where things are written down

| | |
|---|---|
| `agents.md` | master context, working agreement, architecture |
| `PHASE1_CHECKLIST.md` | Phase 1 status, all 9 steps + Step 10 |
| `SPEC.md` | the current step's spec (Step 10) |
| `tasks/plan.md`, `tasks/todo.md` | the current step's breakdown and checkpoints |
| `docs/security-backlog.md` | 8 known gaps, deliberately deferred, with a suggested order |
| `docs/deferred-tuning.md` | performance defaults to revisit under real traffic |
| `docs/TESTING_STRUCTURE.md` | how tests are meant to be laid out |
| `docs/archive/phase1-step*/` | every completed step's spec, plan, and todo |
| `docs/intent/quantsim-resume.md` | why the workflow changed in July 2026 |
