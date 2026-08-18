# Next session — state of play

Last updated **2026-08-18**, at the close of Step 16 (backtesting engine MVP).

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Step 16 is code-complete and verified, but not yet committed or merged

| | |
|---|---|
| Branch | `step16-backtesting-engine` — checked out, working tree has all of Step 16's changes **uncommitted**. Nothing has been pushed. |
| Commit | **Not done yet, deliberately.** This session built and adversarially verified all 14 tasks but did not commit or merge — needs Khalil's explicit go-ahead per this project's git workflow (branch per step, review before merge). |
| Tests | `make test`, `make vet`, and `make test-integration` (all three services' harnesses, including 12 new backtesting-store tests) all green. |
| Dev database | `users=20`, `accounts=20`, every trading and backtesting table empty again — the two throwaway accounts (`step16review`, `step16stranger`) used for manual verification were deleted afterward. Migration `007_backtests` is applied. |
| Local processes | The gateway and backtesting service that were started for manual verification were killed at the end of this session. `auth`, `market-data`, and `trading-engine` were already running from earlier in the day and were left as they were — check with `lsof -i :8080-8084` before assuming any port's state. |

`docs/archive/phase2-step16-backtesting-engine/{SPEC.md,plan.md,todo.md}` hold this step's spec, plan and todo — moved there (plain `mv`, not `git mv`, since `SPEC.md` was never committed on this branch to begin with; staged as new files either way) the same way Step 14/15's were.

### Before committing: review the diff

Nothing in `git status` has been committed. The next session (or Khalil, right now) should:

1. `git status` / `git diff` to see everything this session touched — the new `services/backtesting/` module in full, migration `007_backtests.{up,down}.sql`, edits to `go.work`, `services/gateway/{internal/handler/router.go,cmd/server/main.go,internal/handler/router_test.go}`, `Makefile`, `.env.example`, the new `PHASE3_CHECKLIST.md`, the `PHASE2_CHECKLIST.md` Phase-2-complete note, `docs/deferred-tuning.md` §11, the archived spec/plan/todo, and this file.
2. Decide on commit granularity (one commit per task, matching Steps 14–15's convention, or fewer) and write the commits.
3. Merge `step16-backtesting-engine` to `main`, delete the branch locally and on the remote, matching Steps 14–15's close-out.

---

## What Step 16 shipped

A fifth Go service, `services/backtesting`, the first system in Phase 3. `POST /backtests` runs a moving-average-crossover strategy against `market-data`'s existing historical daily bars: `GenerateSignals` finds crossings, `Simulate` fills them at the *next* bar's open (avoiding lookahead bias) with all-in long-only sizing, and `ComputeMetrics` derives the five `agents.md` §3 metrics (total return, Sharpe, max drawdown, win rate, profit factor). The run and its simulated trade log persist to two new tables (`backtests`, `backtest_trades`, migration 007) and are readable via `GET /backtests` and `GET /backtests/{id}`, both scoped to the caller. Gateway got a fourth proxied prefix.

**Backend only**, mirroring the Step 14 → 15 split — the frontend is a later step.

`services/backtesting/integration/` is the third copy of the auth/trading-engine Postgres integration harness — `docs/TESTING_STRUCTURE.md` §6a's extraction trigger has now fired, recorded with its own reasoning in `docs/deferred-tuning.md` §11. The extraction itself was deliberately deferred to its own change rather than bundled in here.

### A real routing bug the build caught before it shipped

`trading-engine` has no bare `/trading` endpoint, so the gateway's original `/trading/*`-only wildcard was always sufficient. `backtesting` *does* have one — `POST`/`GET /backtests` is the collection route itself — and chi's `/backtests/*` wildcard alone does not match a request with no trailing segment. Caught by both the gateway's own routing test and a live `curl` (a 401 instead of reaching the backend) before the fix — adding `r.Handle("/backtests", backtestingProxy)` alongside the wildcard — went in. Full writeup in `PHASE3_CHECKLIST.md`'s Step 16 entry.

### A real test gap the mutation-testing pass found

Removing `GenerateSignals`' `haveState` guard (so the very first eligible bar could fire a false signal) was **not caught** by the original crossover test, because that series happened to start with the short and long MAs exactly tied. A new test using a monotonically increasing series — already above on bar one, nothing to "cross" from — closed the gap; the same mutation is caught by it. Full writeup in `PHASE3_CHECKLIST.md`.

---

## What to do next

**1. Commit and merge Step 16** (see above) — this is the immediate next action, not a new step.

**2. Step 17: the backtesting frontend**, mirroring Step 14 → 15's split — a strategy-config form, a results view (metrics + trade log), and a run history list, all against the four `/backtests/*` endpoints Step 16 just shipped. Recommended next, since it's the same shape of work Step 15 already proved out against `/trading/*`.

**3. RSI/MACD strategies** are the next natural extension of the backtesting engine itself once the frontend exists to exercise them, but are lower priority than the frontend — a second and third strategy behind a UI nobody can drive yet doesn't add resume-visible value.

**4. The two long-standing small items**, both still open and both still lower priority:

- `market-data`'s store has no tests (`historical_price_store.go`). The integration harness now exists in **three** copies (not two); a fourth use is the point to actually extract to `pkg/testutil/` — see `docs/deferred-tuning.md` §11 and `docs/TESTING_STRUCTURE.md` §6a.
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

**A `go run` service started before a code change keeps serving the old binary.** This session had to kill and restart a gateway process left running from earlier in the day, predating the `/backtests` proxy wiring. Kill the whole `make run-*` / `go run` process tree and restart rather than assuming a long-running dev process reflects the code currently on disk.

**A gateway wildcard route (`/prefix/*`) does not match the bare prefix with no trailing segment.** This is what the Step 16 routing bug above was. If a new backend service has a collection endpoint at its own root (no sub-path, unlike `trading-engine`'s `/trading/orders` etc.), the gateway needs both `r.Handle("/prefix", proxy)` and `r.Handle("/prefix/*", proxy)`.

**Money is `float64` in Go and `NUMERIC(20,4)` in Postgres, and Postgres is the authority.** Read money as `::text` in tests — scanning straight into a `float64` lets a value that lost precision on the way in come back looking exactly like the number you expected. `docs/deferred-tuning.md` §10 has the measured numbers and the trigger for fixing it properly. Backtesting's `POST /backtests` response echoes the raw Go-computed float (not yet round-tripped through Postgres), while `GET /backtests`/`GET /backtests/{id}` show what Postgres actually stored — the two can differ in trailing digits, and that's expected, not a bug.

**Order quantities have a floor of `0.0001` in `trading-engine`, and it is load-bearing.** Do not relax that check without reading `PHASE2_CHECKLIST.md` Step 14 first. `backtesting`'s simulator has no equivalent floor — it computes `cash / price` directly for an all-in fill, since a hypothetical backtest quantity has no ledger tick to respect the way a real paper-traded position does.

**Backtesting's fills happen at the *next* bar's open, one bar after the signal.** This is deliberate (SPEC.md Step 16 §2.4, avoiding lookahead bias), not a bug — a signal on a bar's own close cannot be "traded" until the following bar opens. The very last bar in any range can never produce a fill for exactly this reason.

**The write path fails closed; the read path fails open** — true of `trading-engine` since Step 14, and now equally true of `backtesting`'s history fetch: an unreachable `market-data` fails the whole backtest request (`502 upstream_unavailable`), it never runs on partial or stale data.

**`migrate` lives at `~/go/bin/migrate` and is not on a non-interactive shell's PATH.** Use `make migrate-up` from an interactive shell, or the full path. The integration harness execs the `.up.sql` files directly instead — `docs/deferred-tuning.md` §7.

**A failed migration leaves the schema dirty.** Recovery is `make migrate-force VERSION=<n>` at the last good version, then fix the cause and re-run. Dev database only — the test database is recreated from scratch every run.

**Restart a service after changing its code.** Everything runs under `go run`, so a live instance keeps serving the old binary. Killing the `go run` wrapper alone may not release the port — check `lsof -i :<port>` and kill the actual server binary too if it's still held.

**A green `go test ./...` says nothing about Redis or Postgres.** `make test-integration` covers both, on independent skip paths. `make vet` includes a `-tags=integration` pass so a tagged suite cannot rot invisibly.

**The integration harness now exists in three copies** (`services/auth/integration/`, `services/trading-engine/integration/`, `services/backtesting/integration/`). The guard machinery is byte-identical on purpose. **Change one, change all three, and `diff` them** — `docs/TESTING_STRUCTURE.md` §6a and `docs/deferred-tuning.md` §11 explain why it was copied a third time rather than extracted, and what triggers actually doing that now.

**Rate-limit counters are per-process.** Correct while one gateway runs; a second instance doubles the effective limit — `docs/deferred-tuning.md` §4–§5.

**`gofmt` reports drift in `services/auth/internal/service/interfaces.go` and `types.go`.** Pre-existing, deliberately left alone since Step 11.

---

## Where things are written down

| | |
|---|---|
| `agents.md` | master context, working agreement, architecture |
| `PHASE1_CHECKLIST.md` | Phase 1, all 9 steps + Step 10 — **closed** |
| `PHASE2_CHECKLIST.md` | Phase 2, Steps 11–15 — **closed** |
| `PHASE3_CHECKLIST.md` | Phase 3 — Step 16 written up, including its review findings |
| `SPEC.md` | the current step's spec — **Step 16's is archived; there is no active spec until Step 17 is drafted** |
| `tasks/plan.md`, `tasks/todo.md` | archived with Step 16; recreated when the next step is planned |
| `docs/TESTING_STRUCTURE.md` | test layout; §6a is the integration-test guide |
| `docs/security-backlog.md` | 8 known gaps — items 1, 2 and 4 **closed**; item 8 cheapest next, item 3 the next substantive one |
| `docs/deferred-tuning.md` | deferred decisions with triggers; §11 is Step 16's |
| `docs/archive/phase*/` | every completed step's spec, plan, and todo |
| `docs/intent/quantsim-resume.md` | why the workflow changed in July 2026 |
