# Next session — state of play

Last updated **2026-08-18**, at the close of Step 18 (RSI & MACD strategies).

This file answers three questions on picking the project back up: *is anything half-finished?*, *what do I do next?*, and *what will trip me up?* It is meant to be rewritten each time, not appended to.

---

## Step 18 is code-complete and verified, but not yet committed or merged

| | |
|---|---|
| Branch | `step18-rsi-macd-strategies` — checked out, 19 tasks across 5 phases in 19 commits (three task-pairs — T4+T5, T6+T7, T9+T10 — landed together where splitting them would have meant an artificially broken intermediate state). Nothing has been pushed. |
| Commit | **Not merged yet, deliberately.** Every task landed compiling and green (`internal/store` was allowed to break temporarily between T6/T7 and T9, confirmed via `go build` that nothing else was affected), but merging to `main` needs Khalil's explicit go-ahead per this project's git workflow (branch per step, review before merge). |
| Tests | `make vet`/`test`/`test-integration` all green across all five services. `npm run lint`/`build`/`test` (58 tests) green — `build` uses the project's real `tsc -b`, not a bare `tsc --noEmit`, which was found mid-step to silently no-op against this project's referenced `tsconfig` and report zero errors regardless of what's broken. |
| Mutation testing | Three controls broken deliberately and confirmed caught, then cleanly reverted (`git diff` empty afterward): `maxWarmupBars`, RSI's `oversold < overbought`, and `crossoverSignals`' edge-only firing (this one alone failed 5 tests across both strategies that share it). |
| Dev database | `users=20`, `accounts=20`, `backtests=0` — unchanged from before this step. Migration `008_backtest_strategies` **is applied** to the real dev database (`strategy`/`params` columns present, `short_window`/`long_window` gone) — this happened during Checkpoint B verification, before any frontend code existed, and is not something a merge needs to redo. Two throwaway accounts (`step18verify2` for Checkpoint B's curl pass, `step18browser` for the browser pass) were created and deleted in turn, each restoring the baseline exactly. |
| Local processes | The `gateway`, `backtesting`, and frontend dev server (`vite`, port 5173) started for this session's verification were killed at the end. `auth`, `market-data`, and `trading-engine` were already running from earlier and were left as they were — check with `lsof -i :8080-8084` and `:5173` before assuming any port's state. |

`docs/archive/phase3-step18-rsi-macd-strategies/{SPEC.md,plan.md,todo.md}` hold this step's spec, plan and todo — moved there (plain `mv`, not `git mv`, since none of the three were ever committed under `tasks/` before being moved) the same way Steps 16/17's were.

### Before merging: review the diff

`git status` on `step18-rsi-macd-strategies` is clean — everything is committed, one commit per task (T4+T5 and T6+T7 and T9+T10 landed paired, since each pair was too tightly coupled to split meaningfully; noted in their own commit messages). The next session (or Khalil, right now) should:

1. `git log --oneline main..step18-rsi-macd-strategies` to see all 19 commits, and spot-check a few diffs — particularly `services/backtesting/internal/service/strategy.go` (the new `Strategy` interface + three implementations, 384 lines), `infra/migrations/008_backtest_strategies.{up,down}.sql`, and `frontend/src/api/types.ts` (the discriminated-union rewrite).
2. Decide whether to squash before merging — Steps 16/17 both ended up as a single squashed `feat(stepN):` commit on `main` despite per-task commits existing on the branch during development; Step 18 has 19 commits including several docs/checkpoint ones, so squashing is probably worth it here too, but that's Khalil's call.
3. Merge `step18-rsi-macd-strategies` to `main`, delete the branch locally and on the remote, matching Steps 14–17's close-out.
4. **After merging:** `agents.md`'s Phase 3 roadmap line ("RSI/MACD strategies, multi-symbol backtests — not started") is now stale for the RSI/MACD half — update it the same way Step 17's close-out did (`f7dd5a6`, a separate `docs:` commit on `main` after the merge, alongside a README/TESTING_STRUCTURE pass). Not done as part of this branch, matching that precedent.

---

## What Step 18 shipped

The backtesting engine's second and third strategies — `agents.md` §3 names three example strategies (moving-average crossover, RSI thresholds, MACD signals); Step 16 built the first and Step 17 shipped its UI, both deliberately deferring RSI/MACD until the pipeline and a strategy picker existed. Both preconditions were met, so this step made the engine genuinely multi-strategy rather than single-strategy-with-room-to-grow:

- A `Strategy` interface (`Kind`/`Params`/`WarmupBars`/`GenerateSignals`) with three implementations behind one `NewStrategy(kind, raw)` constructor — the single place an unknown kind, malformed params, or an out-of-bounds parameter all surface as `ErrInvalidRequest`.
- `wilderRSI` and `ema`, two new pure indicators, each verified against a hand-computed reference fixture *before* either strategy was built on top of them — the deliberate sequencing this step's plan called D1, because a wrong indicator produces a plausible-looking equity curve with every downstream test still green.
- A breaking change to the wire format and schema — `{strategy, params}` replaces `{short_window, long_window}` on `POST /backtests` and in the `Backtest` response; `strategy TEXT` + `params JSONB` replaces the two window columns in Postgres. **No compatibility shim** — taken deliberately, since the only client is this repo's own frontend, updated in the same step.
- `Simulate`, `ComputeMetrics`, `backtest_trades`, the next-bar-open fill rule, and all five metrics are **completely untouched**. Every line of new code sits upstream of `[]Signal` — the payoff of how Step 16 originally split the pipeline.
- A strategy `<select>` in the existing `BacktestForm.tsx`, swapping visible field groups per strategy with every strategy's own conventional defaults pre-populated from mount, and one `describeStrategy` helper (with a defensive unknown-strategy fallback) replacing the two places that used to format `{short}/{long}` inline.

Full writeup, including the mutation-testing results and the manual browser pass, in `PHASE3_CHECKLIST.md`'s Step 18 entry.

### Two things worth knowing about even though nothing was broken by them

Unlike Steps 16 and 17, this step found no real bug in existing code — but two things came up mid-work worth remembering:

1. **`tsc --noEmit` silently no-ops against this project's `tsconfig` setup.** The root `tsconfig.json` is a bare `references`-only file (no `include`/`files` of its own); running `npx tsc --noEmit` from `frontend/` reports zero errors *regardless of what's actually broken*, because it isn't resolving the referenced project configs the way `tsc -b` does. Verified directly: ran both against a deliberately-broken intermediate state (old field names still referenced after the wire-format change) — `tsc --noEmit` reported nothing, `tsc -b` (the actual `npm run build` command) reported exactly the expected errors. **Always typecheck this frontend with `npm run build` or `npx tsc -b`, never a bare `tsc --noEmit`.**
2. **`Pick<UnionType, K>` does not distribute over the union in TypeScript.** `strategy-display.ts`'s first draft of `BacktestParamsByKind` was `Pick<Backtest, 'strategy' | 'params'>`. Since `Backtest` is now a three-variant discriminated union, `Pick` over it collapses to one flat `{ strategy: StrategyKind; params: BacktestParams }` shape — the pairing between a given strategy and its own params type is lost, which breaks exactly the narrowing a `switch (backtest.strategy)` needs to narrow `backtest.params` inside each case. Fixed by restating the type as a direct three-member union instead of deriving it with `Pick`. Worth remembering for any future discriminated-union work in this codebase: `Pick`/`Omit` over a union needs the `T extends unknown ? Pick<T, K> : never` distributive form, or — simpler, as done here — just write the union out directly.

---

## What to do next

**1. Merge Step 18** (see above) — this is the immediate next action, not a new step.

**2. Multi-symbol / portfolio-level backtests** are now the last named item from `agents.md` §3's backtesting scope. Both Step 16 and Step 18 deferred it for the same reason: it's a materially different simulator (correlation, cross-symbol position sizing), not a small extension — the natural next *major* piece of work in this system, but a bigger lift than either strategy step was.

**3. The two long-standing small items**, both still open and both still lower priority:

- `market-data`'s store has no tests (`historical_price_store.go`). The integration harness exists in **three** copies (auth, trading-engine, backtesting); a fourth use is the point to actually extract to `pkg/testutil/` — see `docs/deferred-tuning.md` §11 and `docs/TESTING_STRUCTURE.md` §6a.
- Pre-existing `gofmt` drift in `services/auth/internal/service/{interfaces.go,types.go}`, untouched since Step 11. Worth a one-line cleanup commit before any `fmt` check lands in CI.

**4. Security backlog:** items 1, 2 and 4 are closed. Item **8** (Unicode-normalise passwords) is the cheap one left from the Phase 2 set and gets more expensive as real accounts accumulate. Item **3** (Argon2id) is the next substantive one and wants its own step, since it carries a migration strategy.

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

**A bare `tsc --noEmit` silently no-ops against this project's `tsconfig` setup and reports zero errors regardless of what's broken.** This is Step 18's own discovery — see above. Use `npm run build` (which runs `tsc -b`) or `npx tsc -b` directly to actually typecheck this frontend.

**`Pick<UnionType, K>` does not distribute over a union in TypeScript** — it collapses a discriminated union's fields into one flat shape and loses the pairing a `switch` on the discriminant needs to narrow the other field. See `strategy-display.ts`'s `BacktestParamsByKind` for the fix (a direct union, not a `Pick`).

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
