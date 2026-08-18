# Next session — state of play

Last updated **2026-08-18**, at the close of Step 15 (trading frontend).

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Step 15 is code-complete and verified, but not yet committed or merged

| | |
|---|---|
| Branch | `step15-trading-frontend` — checked out, working tree has all of Step 15's changes **uncommitted**. Nothing has been pushed. |
| Commit | **Not done yet, deliberately.** This session built and adversarially verified all 13 tasks but did not commit or merge — that needs Khalil's explicit go-ahead per this project's git workflow (branch per step, review before merge). |
| Tests | `frontend`: `npm run test` **17/17 passing** (new), `npm run build` (`tsc -b` + `vite build`) clean, `npm run lint` clean (three pre-existing, non-blocking `react-hooks/exhaustive-deps` warnings — see below) |
| Dev database | `users=20`, `accounts=20`, trading tables empty again — the one throwaway account (`step15manual`) used for manual verification was deleted afterward |
| Local processes | `docker-up` (Postgres, Redis), all four Go services, and the Vite dev server were all running at the end of this session for manual verification. They may still be up — check with `lsof -i :8080-8083,5173` before starting new copies. |

`docs/archive/phase2-step15-trading-frontend/{SPEC.md,plan.md,todo.md}` hold this step's spec, plan and todo — moved there (staged with `git mv`, not yet committed) the same way Step 14's were.

### Before committing: review the diff

Nothing in `git status` has been committed. The next session (or Khalil, right now) should:

1. `git status` / `git diff` to see everything this session touched — new files under `frontend/src/trading/`, `frontend/src/format.ts`, `frontend/vitest.config.ts`, edits to `Dashboard.tsx`/`PriceList.tsx`/`api/client.ts`/`api/types.ts`/`tsconfig.app.json`/`package.json`, the archived spec/plan/todo, and this file plus `PHASE2_CHECKLIST.md`.
2. Decide on commit granularity (one commit per task, matching Step 14's convention, or fewer) and write the commits.
3. Merge `step15-trading-frontend` to `main`, delete the branch locally and on the remote, matching Step 14's close-out.

---

## What Step 15 shipped

Wired all four `/trading/*` endpoints into the dashboard: a typed wire layer (`api/types.ts`, `api/client.ts`), two hooks (`usePortfolio` polling every 15s, `useOrders` fetching once, both with `refetch()` and a request-id race guard), and five new components — `OrderTicket` (the only one that writes), `PositionsTable`, `PortfolioSummary`, `OrdersTable`, and `position-display.ts` as the one tested place the null-price/null-P&L rule (§2.5) lives. `Dashboard.tsx` gained a three-column layout (watchlist | tabbed Chart/Positions/Orders/Portfolio | order ticket pinned across every tab) and a header balance figure, with the Chart tab's existing behavior untouched.

First frontend step with automated tests: `vitest` + `@testing-library/react`, 17 tests scoped to the units holding real logic (`format.ts`, `rejection-reason.ts`, `position-display.ts`) rather than full component coverage.

**Frontend only.** No backend, migration, or gateway change — `SPEC.md` §1's non-goals.

### A real bug the adversarial pass found and fixed

`OrderTicket`'s number input had `min`/`max` HTML attributes matching the quantity bounds. Submitting an out-of-range value let the *browser's own* constraint validation silently block the form submit before React's `handleSubmit` ever ran — no crash, no console error, `npm run build`/`lint`/`test` all still green, but the error banner on screen was now stale, showing whatever the *previous* rejection said. Found only by driving the real form in a real browser, not by any automated check. Fixed with `noValidate` on the `<form>` — `validateQuantity` already enforced the same bounds correctly, so this makes it the single place quantity errors get decided and rendered, instead of two paths that could disagree. Full writeup in `PHASE2_CHECKLIST.md`'s Step 15 entry.

---

## What to do next

**1. Commit and merge Step 15** (see above) — this is the immediate next action, not a new step.

**2. Step 16: Phase 3, the backtesting engine.** `agents.md`'s roadmap puts this next — historical ingestion, a strategy simulator, metrics dashboards — and it's the bigger resume-relevant milestone (`agents.md`'s priority order ranks systems depth and backend complexity above UI polish, and this project has now shipped two full UI steps in a row). Recommended over the two small items below unless something about them becomes urgent.

**3. The two long-standing small items**, both still open and both still lower priority:

- `market-data`'s store has no tests (`historical_price_store.go`). The harness now exists in **two** modules; a third use is the recorded trigger for extracting it to `pkg/testutil/` — see `docs/TESTING_STRUCTURE.md` §6a.
- Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`, untouched since Step 11. Worth a one-line cleanup commit before any `fmt` check lands in CI.

**4. Security backlog:** items 1, 2 and 4 are closed. Item **8** (Unicode-normalise passwords) is the cheap one left from the Phase 2 set and gets more expensive as real accounts accumulate. Item **3** (Argon2id) is the next substantive one and wants its own step, since it carries a migration strategy.

---

## Restarting the environment

```bash
make docker-up            # Postgres + Redis
make run-auth             # :8081
make run-market-data      # :8082
make run-trading-engine   # :8083
make run-gateway          # :8080
make run-frontend         # :5173
```

Each `run-*` target runs in the foreground, so they need separate terminals. Four backend services plus the frontend dev server need to be up together for any manual trading-UI check — a stopped `market-data` turns every order into a rejected `upstream_unavailable` (correctly, but confusingly if you forgot to start it) and the watchlist into `error`/em-dash rows.

`make help` lists the test targets too.

Auth rate limiting is **on by default** (100 requests / 15 min per IP; backoff after 5 consecutive failed logins). `RATE_LIMIT_ENABLED=false` turns it off if it gets in the way during development.

`services/auth` requires `REDIS_URL` to boot (`log.Fatal` if unset). **Do not put `PORT=8083` in `.env`**: the Makefile exports that file to every target, so it would move all four services onto the same port. Override per process instead.

**Register a fresh password with something that isn't your username, email, or "quantsim."** Auth's password validator rejects any password containing the username, the email, or the service name as a substring — case-insensitively enough to catch `Step15Manual` if the username is `step15manual`. A generic throwaway phrase with no connection to the account's own name/email sidesteps it.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty database named `quantsim` also exists. `psql -d quantsim` connects successfully and shows no `users` table, which reads like data loss and is not. The user is **`quantsim`** and the database is **`postgres`**:

```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT count(*) FROM users"     # 20, as of this session
```

**A `go run` gateway process started before a code change keeps serving the old binary.** This session found a gateway process left running from an earlier point in the day that predated the trading-engine proxy wiring and was answering `/trading/*` with a stale `501` — indistinguishable from a real regression until `grep`ing the current source turned up nothing that could produce a `501`. Kill the whole `make run-*` / `go run` process tree and restart rather than assuming a long-running dev process reflects the code currently on disk.

**Money is `float64` in Go and `NUMERIC(20,4)` in Postgres, and Postgres is the authority.** Read money as `::text` in tests — scanning straight into a `float64` lets a value that lost precision on the way in come back looking exactly like the number you expected. `total_equity` can legitimately come back as `99999.99999999999`; `docs/deferred-tuning.md` §10 has the measured numbers and the trigger for fixing it properly.

**Order quantities have a floor of `0.0001`, and it is load-bearing.** It is the ledger's own tick. Anything smaller was charged for in full and then rounded to zero shares, which minted money on the sell side — the bug Step 14's adversarial review found. Do not relax that check without reading `PHASE2_CHECKLIST.md` Step 14 first. The frontend now enforces the same floor client-side (`OrderTicket.tsx`), commented with where the backend's literal lives, but the two are independent constants, not read from one shared source.

**A `<form>` with native `min`/`max` on its inputs can silently swallow a submit.** See Step 15's bug above. If a form's error state ever looks "stuck," check whether the browser's own constraint validation blocked the handler before assuming the state logic is wrong.

**The write path fails closed; the read path fails open.** No price means no fill (`502`, order persisted as rejected). No price on a read means `latest_price: null` and still a `200`. Reversing these is the single easiest way to violate the spec's intent, and there are tests on both — plus, as of Step 15, a frontend that renders the null case as an em-dash rather than `$0.00`.

**`migrate` lives at `~/go/bin/migrate` and is not on a non-interactive shell's PATH.** Use `make migrate-up` from an interactive shell, or the full path. The integration harness execs the `.up.sql` files directly instead — `docs/deferred-tuning.md` §7.

**A failed migration leaves the schema dirty.** Recovery is `make migrate-force VERSION=<n>` at the last good version, then fix the cause and re-run. Dev database only — the test database is recreated from scratch every run.

**Restart a service after changing its code.** Everything runs under `go run`, so a live instance keeps serving the old binary. Killing the `go run` wrapper alone may not release the port — check `lsof -i :<port>` and kill the actual server binary too if it's still held.

**A green `go test ./...` says nothing about Redis or Postgres.** `make test-integration` covers both, on independent skip paths. `make vet` includes a `-tags=integration` pass so a tagged suite cannot rot invisibly.

**The integration harness now exists in two copies** (`services/auth/integration/`, `services/trading-engine/integration/`). The guard machinery is byte-identical on purpose. **Change one, change both, and `diff` them** — `docs/TESTING_STRUCTURE.md` §6a explains why it was copied rather than shared, and what triggers extracting it.

**Rate-limit counters are per-process.** Correct while one gateway runs; a second instance doubles the effective limit — `docs/deferred-tuning.md` §4–§5.

**`gofmt` reports drift in `services/auth/internal/service/interfaces.go` and `types.go`.** Pre-existing, deliberately left alone since Step 11.

**Two non-blocking `react-hooks/exhaustive-deps` warnings in the new trading hooks.** `use-portfolio.ts` and `use-orders.ts` each write to a plain instance-counter ref inside their effect's cleanup function; oxlint's rule is aimed at DOM node refs and flags this as a false positive. `npm run lint` still exits 0. A third, older warning of a different shape exists in `use-prices.ts` (missing `symbols`/`symbols.length` deps) — pre-existing since Step 5, same non-blocking status.

---

## Where things are written down

| | |
|---|---|
| `agents.md` | master context, working agreement, architecture |
| `PHASE1_CHECKLIST.md` | Phase 1, all 9 steps + Step 10 — **closed** |
| `PHASE2_CHECKLIST.md` | Phase 2 — Steps 11–15 written up, including Steps 14 and 15's review findings |
| `SPEC.md` | the current step's spec — **Step 15's is archived; there is no active spec until Step 16 is drafted** |
| `tasks/plan.md`, `tasks/todo.md` | archived with Step 15; recreated when the next step is planned |
| `docs/TESTING_STRUCTURE.md` | test layout; §6a is the integration-test guide |
| `docs/security-backlog.md` | 8 known gaps — items 1, 2 and 4 **closed**; item 8 cheapest next, item 3 the next substantive one |
| `docs/deferred-tuning.md` | deferred decisions with triggers; §9 and §10 are Step 14's |
| `docs/archive/phase*/` | every completed step's spec, plan, and todo |
| `docs/intent/quantsim-resume.md` | why the workflow changed in July 2026 |
