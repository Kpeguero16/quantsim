# Refresh-Token Revocation and Logout — Task Checklist (Step 13)

> **`SPEC.md` is APPROVED** — all six decisions resolved 2026-08-17, all as
> recommended (§9). **Implementation is unblocked.**
>
> Closes `docs/security-backlog.md` item 2, the highest-priority open security
> item. Sequenced ahead of the trading engine per `docs/NEXT_SESSION.md`.

Full detail (acceptance criteria, verification, dependency graph, risks) in `tasks/plan.md`.

Prior steps archived under `docs/archive/` — Auth Service (Step 4) through Store-Layer Integration Harness (Step 12).

Each checkpoint is a stop-for-review point per `agents.md`: implement, verify, **stop**.

---

## ⚠️ The one thing this step can get quietly wrong

A revocation check that compiles, looks right, and never actually rejects
anything — the same failure *shape* Step 12's pre-merge review found once
already, in a different guard (`dfe6ba3`). Task 2's mutation check exists
specifically to rule this out: comment out the `IsRevoked` call in `Refresh`,
confirm the "revoked token still refreshes" test fails, then restore.

**Do not mark Task 2 done without running that mutation check.**

---

### Phase 1: The store layer
- [x] **Task 1** — `pkg/auth/jwt.go`: `GenerateToken` sets a `jti` on every
      token. `services/auth/internal/service/interfaces.go`: `RevocationStore`
      interface. `services/auth/internal/store/redis_token_store.go`: real
      implementation, `revoked:` key prefix. `services/auth/internal/service/mock/revocation_store.go`:
      in-memory double with `RevokeErr`/`IsRevokedErr` injection fields.
      `services/auth/go.mod`: `+ github.com/redis/go-redis/v9`.
      `services/auth/integration/`: extend `main_test.go` with an independent
      Redis skip check, add `revocation_store_test.go` against **logical DB
      15** — round-trip, real TTL expiry (2s sleep after a 1s TTL — the mock
      cannot prove this), no key collision, skips cleanly with Redis down

- [x] ⏸️ **Checkpoint: the store layer exists and is proven against real
      Redis** — nothing in the running service behaves differently yet

### Phase 2: Wiring the service
- [x] **Task 2** — `services/auth/internal/service/auth.go`: `Refresh` checks
      `IsRevoked` before the user lookup, fails open + logs on a store error;
      `+ Logout` (validates like `Refresh`, then `Revoke`s the `jti` with its
      remaining TTL, fails open + logs on a store error). `NewService` gains a
      `revocations RevocationStore` parameter — **one find-and-replace**
      across `auth_test.go`'s 20 call sites and `handler/auth_test.go`'s 1
      (identical literal string, verified). `main.go`: reads the *existing*
      `REDIS_URL` (not new — market-data already requires it), wires
      `store.NewRedisTokenStore`

- [x] Mutation check run and reverted — see the warning above

- [x] ⏸️ **Checkpoint: Refresh actually rejects a revoked token; Logout
      actually revokes one** — proven, not assumed

### Phase 3: The endpoint
- [ ] **Task 3** — `services/auth/internal/handler/auth.go`: `+ Logout`
      handler, reuses `service.RefreshTokenRequest`, success is
      `WriteJSON(w, 200, struct{}{})` → `{}` (§2.5 — not `204`, or
      `client.ts`'s generic response handling breaks on the empty body).
      `router.go`: `+ POST /auth/logout`, same unauthenticated group as
      `/login` and `/refresh`. End-to-end handler test: register → logout →
      refresh with the same token → `401`

- [ ] ⏸️ **Checkpoint: the endpoint is reachable and a logged-out token
      demonstrably cannot refresh**

### Phase 4: Frontend
- [ ] **Task 4** — `frontend/src/api/client.ts`: `+ api.logout(refreshToken)`,
      unauthenticated, same shape as `login`/`register`/`refresh`.
      `frontend/src/auth/AuthProvider.tsx`: `logout` captures the refresh
      token **before** `clearSession`, clears immediately, then calls
      `api.logout` best-effort with errors swallowed — sign-out must never
      wait on or fail from the network call. `AuthContextValue`'s shape is
      unchanged

- [ ] Manual browser check (`agents.md` — UI changes need a real run):
      `make docker-up && make run-auth && make run-gateway && make run-frontend`,
      log in, sign out, confirm `POST /auth/logout` → `200` in the network
      tab and the UI returns to login instantly; then replay the old refresh
      token with `curl` against the gateway and confirm `401`

- [ ] ⏸️ **Checkpoint: sign-out is real, verified against a running system**

### Phase 5: Close-out
- [ ] **Task 5** — `.env.example`: one line on `REDIS_URL` now being read by
      `services/auth` too. `docs/security-backlog.md`: item 2 → **CLOSED**,
      same write-up style as item 1. `docs/deferred-tuning.md`: rotation-with-
      reuse-detection recorded with its trigger (theft *detection* becomes a
      requirement, not just logout). Step 13 in `PHASE2_CHECKLIST.md`,
      mutation-check result included. `docs/NEXT_SESSION.md` rewritten — the
      trading engine becomes the sole next item

- [ ] ⏸️ **Checkpoint: Step 13 complete** — `make test` green with Docker
      down, `make test-integration` green with it up (Redis tests included),
      `make vet` clean

---

## Reminders that have cost time before

**`DATABASE_URL` points at `postgres`, not `quantsim`.** Not this step's
concern directly, but `make test-integration` still spins up the same dev
stack — don't let a Redis-focused session make this any less true.

**Restart a service after changing its code.** `make run-auth` runs under
`go run`; a live instance keeps serving the old binary. This has burned an
entire step before and this step changes `main.go`.

**A green unit suite still proves nothing about the revocation check** until
Task 2's mutation check has actually been run and has actually failed before
being reverted. That is the whole point of the task.

**`services/auth/go.mod` is expected to change this time** — unlike Step 12,
which held itself to zero new dependencies. Don't second-guess the `go-redis`
line in the diff; it's §2.2, not scope creep.
