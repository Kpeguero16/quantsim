# Next session — state of play

Last updated **2026-08-18**, at the close of Step 17 (backtesting frontend).

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Step 17 is code-complete and verified, but not yet committed or merged

| | |
|---|---|
| Branch | `step17-backtesting-frontend` — checked out, working tree has all of Step 17's changes **uncommitted**. Nothing has been pushed. |
| Commit | **Not done yet, deliberately.** This session built and manually verified all 14 tasks in a real browser but did not commit or merge — needs Khalil's explicit go-ahead per this project's git workflow (branch per step, review before merge). |
| Tests | `npm run lint`, `npm run build`, and `npm run test` (39 tests: 17 from Step 15 plus 22 new) all green. Backend `make test`, `make vet`, and `make test-integration` (all three services' harnesses) also re-run clean — this branch ended up touching one backend file, see below. |
| Dev database | `users=20`, `accounts=20`, `backtests=0`, `backtest_trades=0` — three throwaway accounts across two verification rounds (`step17review`, `step17stranger`, `step17verify`) were deleted afterward, backtests first (`backtests.user_id` has no `ON DELETE CASCADE`, unlike `backtest_trades`). |
| Local processes | The `gateway` and `backtesting` processes started for this session's browser verification were killed at the end. `auth`, `market-data`, and `trading-engine` were already running from earlier in the day and were left as they were — check with `lsof -i :8080-8084` before assuming any port's state. The frontend dev server (`vite`, port 5173) was already running and picked up every change live via HMR — it was left running too. |

`docs/archive/phase3-step17-backtesting-frontend/{SPEC.md,plan.md,todo.md}` hold this step's spec, plan and todo — moved there (plain `mv`, not `git mv`, since none of the three were ever committed on this branch to begin with) the same way Step 16's were.

### Before committing: review the diff

Nothing in `git status` has been committed. The next session (or Khalil, right now) should:

1. `git status` / `git diff` to see everything this session touched — the new `frontend/src/backtesting/` directory in full, edits to `frontend/src/{api/{client.ts,types.ts},format.ts,format.test.ts,market/Dashboard.tsx}`, one backend fix (`services/backtesting/internal/service/{simulate.go,simulate_test.go}` — a real bug this step's own adversarial testing found, not scope creep; see below), the archived spec/plan/todo, `PHASE3_CHECKLIST.md`'s Step 17 entry, and this file.
2. Decide on commit granularity (one commit per task, matching Steps 14–16's convention, or fewer) and write the commits.
3. Merge `step17-backtesting-frontend` to `main`, delete the branch locally and on the remote, matching Steps 14–16's close-out.

---

## What Step 17 shipped

The backtesting engine's frontend — Step 16 shipped the API, deliberately with no UI (`docs/archive/phase2-step16-backtesting-engine/SPEC.md` §1 non-goals). A fifth `Dashboard.tsx` tab (`'backtest'`), holding a strategy-config form (symbol, MA windows, date range, starting capital) with client-side validation mirroring the backend's exact bounds, a synchronous result view built directly from `POST /backtests`'s own response (no extra round trip, since the backend already returns the full trade log), and a persistent run-history sidebar that reopens any past run via `GET /backtests/{id}`. No new frontend dependencies — `vitest` was already in place from Step 15.

**This closes the Step 14→15 / Step 16→17 pattern for the second time** — both of Phase 2 and Phase 3's backend systems now have a working UI, not just a tested API.

### Verified live in a browser, not just against curl

Every rejection path (`symbol_unavailable`, `date_range_unavailable`), the `profit_factor: null` rendering rule, history reopening, and cross-user isolation were driven through the actual dashboard with `claude-in-chrome`, against real ingested AAPL history — not asserted from API responses alone. Full writeup in `PHASE3_CHECKLIST.md`'s Step 17 entry.

### Two real bugs the browser pass found, one of them backend

1. **A backend crash bug**: `Simulate` built its trade log as a nil slice (`var trades []TradeRecord`), which every existing `len()`-based test missed but `encoding/json` did not — a nil slice marshals as `null`, not `[]`. Any zero-trade run sent `"trades": null`, and `TradeLogTable.tsx`'s unconditional `.length` access crashed the whole dashboard to a blank screen. This is the one backend file this "frontend-only" step ended up touching (`services/backtesting/internal/service/simulate.go`) — fixed at the source (`trades := []TradeRecord{}`), not worked around in the frontend, matching the "list responses are never null" rule this project already enforces everywhere else.
2. **A timezone rendering bug**: `start_date`/`end_date`/`bar_timestamp` are calendar dates with no meaningful time-of-day, but were rendered with a bare `toLocaleDateString()`, which converts through the *viewer's* local timezone — `2024-08-01T00:00:00Z` read as `7/31/2024` on this US-Eastern dev machine. Fixed with a shared `formatDate` in `frontend/src/format.ts` that renders with `{timeZone: 'UTC'}`.

Both were caught by actually looking at the rendered page during manual verification, not by the unit test suite — full writeup with the exact regression tests added for each in `PHASE3_CHECKLIST.md`'s Step 17 entry.

---

## What to do next

**1. Commit and merge Step 17** (see above) — this is the immediate next action, not a new step.

**2. RSI/MACD strategies** are now the natural next extension — Step 16's SPEC.md and Step 17's own non-goals both deferred them until a frontend existed to drive a strategy picker, and that frontend now exists.

**3. Multi-symbol / portfolio-level backtests** remain a materially bigger lift (correlation, cross-symbol position sizing) than a small extension — lower priority than RSI/MACD.

**4. The two long-standing small items**, both still open and both still lower priority:

- `market-data`'s store has no tests (`historical_price_store.go`). The integration harness exists in **three** copies (auth, trading-engine, backtesting); a fourth use is the point to actually extract to `pkg/testutil/` — see `docs/deferred-tuning.md` §11 and `docs/TESTING_STRUCTURE.md` §6a.
- Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`, untouched since Step 11. Worth a one-line cleanup commit before any `fmt` check lands in CI.

**5. Security backlog:** items 1, 2 and 4 are closed. Item **8** (Unicode-normalise passwords) is the cheap one left from the Phase 2 set and gets more expensive as real accounts accumulate. Item **3** (Argon2id) is the next substantive one and wants its own step, since it carries a migration strategy.

---

## Restarting the environment

```bash
make docker-up            # Postgres + Redis
make run-auth             # :8081
make run-market-data      # :8082
make run-trading-engine   # :8083
make run-backtesting      # :8084
make run-gateway          # :8080
make run-frontend         # :5173
```

Each `run-*` target runs in the foreground, so they need separate terminals. `make help` lists the test targets too.

Auth rate limiting is **on by default** (100 requests / 15 min per IP; backoff after 5 consecutive failed logins). `RATE_LIMIT_ENABLED=false` turns it off if it gets in the way during development.

`services/auth` requires `REDIS_URL` to boot (`log.Fatal` if unset). **Do not put `PORT=8083` or `PORT=8084` in `.env`**: the Makefile exports that file to every target, so it would move every service onto the same port. Override per process instead.

**Register a fresh password with something that isn't your username, email, or "quantsim."** Auth's password validator rejects any password containing the username, the email, or the service name as a substring, case-insensitively. A generic throwaway phrase with no connection to the account's own name/email sidesteps it.

---

## Things that will trip you up

**`DATABASE_URL` points at the `postgres` database, not `quantsim`.** An empty database named `quantsim` also exists. `psql -d quantsim` connects successfully and shows no `users` table, which reads like data loss and is not. The user is **`quantsim`** and the database is **`postgres`**:

```bash
docker compose exec -T postgres psql -U quantsim -d postgres -tAc \
  "SELECT count(*) FROM users"     # 20, as of this session
```

**`backtests.user_id` has no `ON DELETE CASCADE`, unlike `backtest_trades.backtest_id`.** Deleting a throwaway user who has run any backtests fails with a foreign-key violation unless their `backtests` rows are deleted first. `backtest_trades` cascades fine from `backtests` — it's only the `users → backtests` edge that needs the extra step.

**A `go run` service started before a code change keeps serving the old binary.** Kill the whole `make run-*` / `go run` process tree and restart rather than assuming a long-running dev process reflects the code currently on disk. The frontend's `vite` dev server is the exception — it has HMR and picks up changes live, so it doesn't need restarting between edits the way the Go services do.

**A gateway wildcard route (`/prefix/*`) does not match the bare prefix with no trailing segment.** This was Step 16's routing bug. If a new backend service has a collection endpoint at its own root (no sub-path, unlike `trading-engine`'s `/trading/orders` etc.), the gateway needs both `r.Handle("/prefix", proxy)` and `r.Handle("/prefix/*", proxy)`.

**The integration harness now exists in three copies** (`services/{auth,trading-engine,backtesting}/integration/`), not extracted to `pkg/testutil/` yet — see `docs/deferred-tuning.md` §11 for why, and what should trigger doing it for real.

**A nil Go slice and an empty one are `len()`-identical but `encoding/json`-different.** `var s []T` marshals as `null`; `s := []T{}` marshals as `[]`. Every list-shaped response field needs the latter, deliberately, even when every existing test only ever checks `len(s)` — that check cannot tell the two apart. This is what Step 17's `Simulate` bug (above) was.

**`toLocaleDateString()` with no `timeZone` option uses the *browser's* local zone, not UTC.** Any value that's a calendar date rather than a real instant (a form's `start_date`/`end_date`, a daily bar's `bar_timestamp`) needs `{timeZone: 'UTC'}` passed explicitly, or it can render a day off depending on where the browser sits relative to UTC. `frontend/src/format.ts`'s `formatDate` is the one place in this app that does this correctly — reuse it rather than calling `toLocaleDateString()` directly on a calendar-date field.

---

## Where things are written down

| Topic | File |
|---|---|
| Phase 1 (auth + market data) | `docs/archive/phase1-step4-auth/` through Step 7's archive |
| Phase 2 (trading engine) — complete | `PHASE2_CHECKLIST.md`, archived specs `docs/archive/phase2-step*` |
| Phase 3 (backtesting engine) — in progress | `PHASE3_CHECKLIST.md`, archived specs `docs/archive/phase3-step*` |
| Deferred tuning / known trade-offs | `docs/deferred-tuning.md` |
| Testing conventions | `docs/TESTING_STRUCTURE.md` |
| Security backlog | `docs/security-backlog.md` |
| Roadmap / phase definitions | `agents.md` |
