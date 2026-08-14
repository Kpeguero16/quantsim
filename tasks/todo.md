# QuantSim Auth Rate Limiting — Task Checklist (Step 11)

> **`SPEC.md` is APPROVED** — the three design decisions were resolved 2026-08-14
> (§9), and both findings raised during drafting were approved as recommended
> (§10). **Implementation is unblocked.**
>
> **Closes `docs/security-backlog.md` item 1** — the largest remaining gap in the
> auth surface. First item of the Phase 2 security work.

Full detail (acceptance criteria, verification steps, dependency graph, risks) in `tasks/plan.md`.

Prior steps archived under `docs/archive/` — Auth Service (Step 4) through Identity Lookup Consistency (Step 10). **Phase 1 is complete;** this step opens Phase 2.

Each checkpoint is a stop-for-review point per `agents.md`: implement, verify, **stop**.

**Two tests are written before the code they cover, and neither is optional** — each pins a security property that a green suite would otherwise fail to prove:
- **Test #4** — a forged `X-Forwarded-For` must not buy a fresh budget (`SPEC.md` §2.5)
- **Test #8** — unknown and known emails must throttle identically (`SPEC.md` §2.6)

---

### Phase 1: The limiter core
- [x] **Task 1** — `limiter.Store` + sharded in-memory implementation with injected clock and eviction. Tests #1, #2, #3, #12 written RED first. No new `go.mod` entry; `-race` clean

- [x] **Task 4** — Exponential backoff schedule: failures 1–4 free, then 1/2/4/8 min capped at 15, keyed on failures *already recorded*. Tests #5, #6, #7 RED first. Pure `Delay` function, no I/O; windows and counts both decay

- [x] ⏸️ **Checkpoint: The core throttles correctly in isolation** — both suites green under `-race`, nothing wired into the request path yet

### Phase 2: Per-IP limiting
- [x] **Task 2** — `RateLimitByIP` middleware keyed on `r.RemoteAddr`, port stripped. **Test #4 RED first** — confirm it fails while keying on the header and passes after. Tests #4, #10

- [x] **Task 3** — Wire into the router (`StripUserID -> CORS -> RateLimitByIP -> [routes]`), construct the store in `main.go`, document the five `RATE_LIMIT_*` knobs in `.env.example`. Test #11 proves a `429` still carries CORS headers

- [x] ⏸️ **Checkpoint: Per-IP limiting is live end to end** — restart the gateway, loop `/auth/login` past the threshold, confirm `429` with the standard body, recovery after the window, and `/healthz` + `/market-data/*` unaffected

### Phase 3: Per-account limiting
- [x] **Task 5** — `RateLimitLoginByAccount`: 64KB capped body read, replay via `io.NopCloser`, `ResponseWriter` wrapper counting only `401`s. **Test #8 RED first.** Tests #8, #9. Oversized/malformed bodies pass through untouched

- [x] ⏸️ **Checkpoint: Both dimensions live** — failures throttle after 5; a *nonexistent* email throttles identically in status, body, and timing; a correct password from another IP still succeeds mid-backoff

### Phase 4: Close out
- [ ] **Task 6** — **Correct `docs/security-backlog.md` item 1** (its `X-Forwarded-For` premise is wrong) and mark it closed; add two `docs/deferred-tuning.md` entries with named triggers (multiple gateway instances; ALB deployment); write up Step 11; rewrite `docs/NEXT_SESSION.md`

- [ ] ⏸️ **Checkpoint: Step 11 complete** — `go test ./...` green across the workspace, `go build ./...` clean, docs reflect reality

---

## Reminders that have cost time before

**Restart the gateway after every code change.** It runs under `go run`, so a live instance keeps serving the old binary. This silently burned an entire step once — `:8081` kept accepting one-character passwords while three commits of validation sat on disk. If behaviour does not match the code, check this first.

**A green unit suite proves nothing about `internal/store/`.** Not this step's concern (nothing here touches the store), but the store-layer integration harness is still unbuilt and is the item queued behind this one.
