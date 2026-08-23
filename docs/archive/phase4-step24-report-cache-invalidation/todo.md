# Todo — Report cache invalidation on a fill (Step 24)

Tracks `tasks/plan.md`'s 4 tasks and 1 checkpoint. **All done. What remains is documentation and the merge.**

Branch `step24-report-cache-invalidation`, cut from `main` at `e0ba025`, squashed to one `feat(step24)` (`5d9d685`) and merged `--no-ff` (`4bcc583`). Branch deleted, `main` pushed. Root `SPEC.md` and `tasks/` were never carried onto `main`; this file is their archived copy.

---

## State of the machine

**Everything is put back.** Services stopped, database at baseline and verified by query: `users=20 accounts=20 trades=0 orders=0 positions=0`, `historical_prices=3525`. Postgres and Redis containers up. No `insights:*` or `narrative:*` keys.

**This step cost nothing.** The narrative endpoint was never called. That mattered more here than in Step 23: §2.7 means a fill now causes the next narrative view to be a fresh billed generation, so a careless verification pass could have spent real money.

---

## T1 — Choose the side of the seam. Done.

Write-side, decided on facts rather than taste:

- **trading-engine has no Redis at all** — no client, no dependency, no `REDIS_URL`. That is the real cost of write-side and it is the argument for read-side.
- **There is no cheap freshness probe today.** `ListTrades` orders `executed_at ASC`, so `?limit=1` returns the *oldest* trade. Read-side needs a new endpoint before it can begin.
- **Read-side pays per read, forever.** An HTTP round trip on every cached read to catch an event that happens once per fill, against a cache whose whole purpose is avoiding round trips.

Also ruled out along the way: publishing an event for ai-insights to subscribe to. Redis pub/sub is fire-and-forget, so a subscriber that is down loses the message and the cache stays stale, which is the defect being fixed wearing a different hat.

**Noted, not acted on.** The report math is ~25µs. Essentially the entire cost this cache avoids is HTTP, and most of that is per-symbol history identical for every user. Caching histories by symbol instead of reports by user would delete this problem rather than patch it. That is a rewrite of §2.8 and it is in SPEC §6, not this step.

## T2 — The shared key. Done.

`pkg/cachekeys.Insights(uuid.UUID) string`. Takes a UUID rather than a string so ai-insights' existing guarantee — that a token subject cannot become an arbitrary Redis key, and a subject containing a colon cannot forge another namespace — is structural rather than conventional. `pkg` already required `google/uuid`, so this adds no dependency.

ai-insights' `insightsKey` is gone; `RedisInsightsCache` parses the string it receives at its own edge. A parse failure returns an error, which that interface already documents as safe: every method may fail and no failure is a request failure, so the service logs and computes. That is strictly better than what it replaces, where an unparseable id quietly became a key.

The namespace-collision test moved to `pkg/cachekeys` along with the key it tests.

## T3 — Invalidate on a fill. Done.

`InsightsInvalidator` in trading-engine's service package, a `noopInvalidator` for unconfigured Redis, `RedisInsightsInvalidator` over one `DEL`, and the call in `PlaceOrder` after `ExecuteOrder` returns without error.

`REDIS_URL` is optional and loud when unset. Confirmed at runtime in both directions: with it, the boot log is silent and fills invalidate; without it, the warning prints and orders still place (HTTP 201).

## T4 — Tests and the adversarial pass. Done.

Ten tests across three packages. Six mutants, all killed:

| Mutant | Result |
|---|---|
| No invalidation at all | killed |
| Keep the request's cancellation instead of `WithoutCancel` | killed |
| Return the invalidation error from `PlaceOrder` | killed |
| Invalidate before `ExecuteOrder`, so rejections invalidate too | killed |
| Invalidate `uuid.Nil` instead of the placing user | killed |
| Change the namespace prefix to `insight:` | killed, in both trading-engine and `pkg` |

**One mutant did not apply on the first attempt and had to be rewritten.** Replacing `cachekeys.Insights(userID)` with a string literal left the import and the parameter unused, so it failed to build. A mutant that does not build is not a caught mutant, and it looks exactly like one in a results table. Re-run as `cachekeys.Insights(uuid.Nil)`, which keeps everything used and still breaks the behaviour.

**The cancellation test needed a mock hook to be reachable at all.** Cancelling the context before `PlaceOrder` is refused at the price fetch and never reaches a fill, so it tested nothing. `mock.TradingStore.OnExecute` lets the test cancel *during* the fill, which is the only way to reach the post-fill code with an already-cancelled request context. Without that, the test passed against code with no `WithoutCancel` in it.

## Checkpoint A — Runtime evidence. Done.

Seeded a throwaway user with a reconciling three-buy history, cached a report, then placed real orders through `POST /trading/orders`.

| | Redis configured | No Redis (the control) |
|---|---|---|
| `insights:{user}` after the fill | **deleted** | **survives** |
| Refetched `trade_count` | **6**, matching Postgres | **4**, while Postgres held 5 |

The control is the defect reproduced exactly: the database had five trades and the reader's report said four.

**One figure legitimately does not move, and it will look like a miss.** The AAPL position quantity stays at its pre-trade value after a fill placed today, because holdings describe `as_of_date` (2026-07-28, where the bar calendar ends) and a trade after that date is projected forward only for the reconciliation guard, never into reported positions. That is SPEC §2.12's documented tail-truncation behaviour, not something this step failed to invalidate. `behavior.trade_count` is the figure that moves, and it did.

---

## Verification

| | |
|---|---|
| `make vet` | clean |
| `make test` | green, all seven modules, 0 failures |
| `make test-integration` | **63/0**, unchanged from Steps 22 and 23 |
| `GOWORK=off go build ./...` | passes for all seven modules, including `pkg`'s new package |
| Mutations | 6 run, **6 killed** |
| Live stack | key deleted on a fill; report and database agree at 6 trades. Control without Redis: key survives, report stale at 4 against 5 |
| Cost | **$0.00.** The narrative endpoint was never called. |
