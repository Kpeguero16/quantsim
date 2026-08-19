# Next session — state of play

Last updated **2026-08-18**, right after Step 18 (RSI & MACD strategies) was reviewed and merged to `main`.

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Step 18 is merged. Nothing is half-finished.

| | |
|---|---|
| Branch | `step18-rsi-macd-strategies` — merged to `main` and deleted, both locally and there was never a remote copy (feature branches in this project stay local; only `main` is pushed). |
| Commits on `main` | Two: `feat(step18): RSI and MACD strategies` (the branch's 20 commits squashed, matching Steps 16/17's precedent) then `Merge Step 18: RSI and MACD strategies` (`3b94d27`), followed by a separate `docs:` commit (`99ca840`) updating `agents.md`/`README.md`'s stale roadmap lines. All three are pushed to `origin/main`. |
| Pre-merge review | An independent adversarial code review (not just the mutation testing already described in `PHASE3_CHECKLIST.md`) found one real bug: integer overflow in `newRSIStrategy`/`newMACDStrategy` let an oversized period bypass the `maxWarmupBars` bound and panic the handler. Fixed, tested, and folded into the squashed commit before merge — see `PHASE3_CHECKLIST.md`'s Step 18 entry for the full writeup. |
| Tests | `make vet`/`test`/`test-integration` green across all five services, re-verified on `main` itself after the merge (not just on the branch). `npm run lint`/`build`/`test` (58 tests) green. |
| Dev database | `users=20`, `accounts=20`, `backtests=0` — unchanged. Migration `008_backtest_strategies` has been applied since Checkpoint B during Step 18's build (`strategy`/`params` columns present, `short_window`/`long_window` gone). |
| Local processes | Only `auth`, `market-data`, and `trading-engine` were left running at the end of the Step 18 session (pre-existing from earlier); `gateway`, `backtesting`, and the frontend dev server were started for verification and killed afterward. Check with `lsof -i :8080-8084` and `:5173` before assuming any port's state — this file doesn't track that across sessions. |

`docs/archive/phase3-step18-rsi-macd-strategies/{SPEC.md,plan.md,todo.md}` hold this step's spec, plan and todo.

---

## What Step 18 shipped

The backtesting engine's second and third strategies — `agents.md` §3 names three example strategies (moving-average crossover, RSI thresholds, MACD signals); Step 16 built the first and Step 17 shipped its UI, both deliberately deferring RSI/MACD until the pipeline and a strategy picker existed. Both preconditions were met, so this step made the engine genuinely multi-strategy rather than single-strategy-with-room-to-grow:

- A `Strategy` interface (`Kind`/`Params`/`WarmupBars`/`GenerateSignals`) with three implementations behind one `NewStrategy(kind, raw)` constructor — the single place an unknown kind, malformed params, or an out-of-bounds parameter all surface as `ErrInvalidRequest`.
- `wilderRSI` and `ema`, two new pure indicators, each verified against a hand-computed reference fixture *before* either strategy was built on top of them.
- A breaking change to the wire format and schema — `{strategy, params}` replaces `{short_window, long_window}` on `POST /backtests` and in the `Backtest` response; `strategy TEXT` + `params JSONB` replaces the two window columns in Postgres. **No compatibility shim** — the only client is this repo's own frontend, updated in the same step.
- `Simulate`, `ComputeMetrics`, `backtest_trades`, the next-bar-open fill rule, and all five metrics are **completely untouched**.
- A strategy `<select>` in `BacktestForm.tsx`, swapping visible field groups per strategy with every strategy's own conventional defaults pre-populated from mount, and one `describeStrategy` helper replacing the two places that used to format `{short}/{long}` inline.

Full writeup, including the mutation-testing results, the manual browser pass, and the overflow bug the pre-merge review caught, in `PHASE3_CHECKLIST.md`'s Step 18 entry.

### Three things worth knowing about

1. **`tsc --noEmit` silently no-ops against this project's `tsconfig` setup.** The root `tsconfig.json` is a bare `references`-only file; running `npx tsc --noEmit` from `frontend/` reports zero errors *regardless of what's actually broken*, because it isn't resolving the referenced project configs the way `tsc -b` does. **Always typecheck this frontend with `npm run build` or `npx tsc -b`, never a bare `tsc --noEmit`.**
2. **`Pick<UnionType, K>` does not distribute over the union in TypeScript.** It collapses a discriminated union's fields into one flat shape and loses the pairing a `switch` on the discriminant needs to narrow the other field. See `strategy-display.ts`'s `BacktestParamsByKind` for the fix (a direct union, not a `Pick`).
3. **A generic upper bound over a *sum* of user-controlled integers doesn't protect the addends from overflowing first.** `NewStrategy`'s single `WarmupBars() > maxWarmupBars` check was meant to be the one place every strategy's parameters get bounded — but a large enough individual period overflowed the sum to a negative number before that comparison ever ran, passing it silently. Each strategy constructor now also bounds its own period-like fields individually, before any arithmetic on them. Worth remembering for any future "one generic check covers every case" design over integer inputs: it only holds if the inputs feeding the check can't overflow the arithmetic that produces it.

---

## What to do next

**1. Multi-symbol / portfolio-level backtests** are now the last named item from `agents.md` §3's backtesting scope. Both Step 16 and Step 18 deferred it for the same reason: it's a materially different simulator (correlation, cross-symbol position sizing), not a small extension — the natural next *major* piece of work in this system.

**2. The two long-standing small items**, both still open and both still lower priority:

- `market-data`'s store has no tests (`historical_price_store.go`). The integration harness exists in **three** copies (auth, trading-engine, backtesting); a fourth use is the point to actually extract to `pkg/testutil/` — see `docs/deferred-tuning.md` §11 and `docs/TESTING_STRUCTURE.md` §6a.
- Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`, untouched since Step 11. Worth a one-line cleanup commit before any `fmt` check lands in CI.

**3. Security backlog:** items 1, 2 and 4 are closed. Item **8** (Unicode-normalise passwords) is the cheap one left from the Phase 2 set and gets more expensive as real accounts accumulate. Item **3** (Argon2id) is the next substantive one and wants its own step, since it carries a migration strategy.

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

**The `migrate` CLI is installed to `$(go env GOPATH)/bin`, not on the default `PATH`.** `make migrate-up`/`migrate-down` will report `migrate: command not found` from a plain shell unless `$(go env GOPATH)/bin` is on `PATH` — export it first, or run the `migrate` binary by its full path.

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

**A nil Go slice and an empty one are `len()`-identical but `encoding/json`-different.** `var s []T` marshals as `null`; `s := []T{}` marshals as `[]`. Every list-shaped response field needs the latter, deliberately, even when every existing test only ever checks `len(s)` — that check cannot tell the two apart. This is what Step 17's `Simulate` bug was.

**`toLocaleDateString()` with no `timeZone` option uses the *browser's* local zone, not UTC.** Any value that's a calendar date rather than a real instant (a form's `start_date`/`end_date`, a daily bar's `bar_timestamp`) needs `{timeZone: 'UTC'}` passed explicitly, or it can render a day off depending on where the browser sits relative to UTC. `frontend/src/format.ts`'s `formatDate` is the one place in this app that does this correctly — reuse it rather than calling `toLocaleDateString()` directly on a calendar-date field.

**A bare `tsc --noEmit` silently no-ops against this project's `tsconfig` setup and reports zero errors regardless of what's broken.** Use `npm run build` (which runs `tsc -b`) or `npx tsc -b` directly to actually typecheck this frontend.

**`Pick<UnionType, K>` does not distribute over a union in TypeScript** — it collapses a discriminated union's fields into one flat shape and loses the pairing a `switch` on the discriminant needs to narrow the other field. See `strategy-display.ts`'s `BacktestParamsByKind` for the fix (a direct union, not a `Pick`).

**A generic bound checked on a sum doesn't protect against the addends overflowing first.** `maxWarmupBars` is checked once as `WarmupBars() > 500`, but each strategy constructor also bounds its own period-like fields individually before feeding them into that arithmetic — see Step 18's overflow bug above. Any future "one check covers every case" design over integer inputs derived by arithmetic needs the same treatment.

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
