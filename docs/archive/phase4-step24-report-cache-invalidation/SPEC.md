# SPEC — Report cache invalidation on a fill (Step 24)

Status: **Implemented, verified against the running stack, ready for docs and merge.** §5.1 carries the live evidence. §2.1 was decided against the read-side alternative; §2.2 and §2.3 are the two that would be easy to get subtly wrong. §6 carries what this step deliberately leaves.

Scope: `services/trading-engine/` gains a Redis client and one call after a fill; `pkg/` gains a small package holding the cache key. `services/ai-insights/` changes only to use that shared key instead of its own private one. No migration, no new table, no gateway change, no frontend change, and **no change to any figure, threshold or rule**.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase4-step23-report-hash-stability/`.

---

## 1. Objective

Step 22 found it and Step 23 left it: a fill's report refetch is defeated by the five-minute `insights:{user_id}` cache. The user places an order, the dashboard refetches, and for up to five minutes it shows figures computed before their own trade, with nothing on screen saying so.

Step 23 sharpened the stakes rather than changing them. Now that `ReportHash` is stable, the frontend's §2.3 check finally means something, and this is the one remaining case that makes it fire: the report is stale, the narrative was generated from a fresher one, the hashes disagree, and the reader is told to regenerate a report that is correct apart from being five minutes behind their own trade.

**Objective:** a successful fill removes that user's cached report, so the next request recomputes.

**Non-goals:**

- **Read-side freshness validation.** §2.1.
- **Changing the five-minute TTL.** The TTL is right for what it protects. This step fixes the one event that must not wait for it.
- **Caching histories by symbol instead of reports by user.** The bigger idea, and a real one: the report math is microseconds and the whole cost is HTTP, most of it per-symbol history identical for every user. It deletes this problem rather than patching it, and it is a rewrite of §2.8, not an invalidation. §6.
- **Invalidating the narrative cache.** It needs no invalidation and must not get one. §2.7.
- **Fixing the unbounded Redis clients in `auth` and `market-data`.** Found while surveying for this step, recorded in §6, not touched here.
- **Invalidating on a rejected order.** §2.6.

---

## 2. Design decisions

### 2.1 Write-side invalidation, not read-side validation

Two designs work. trading-engine deletes the key when it fills an order, or ai-insights checks whether its cached report is still current before serving it.

Read-side is attractive because it keeps the dependency direction as it stands: ai-insights already calls trading-engine, and trading-engine calls nothing. It is rejected on cost and on shape. `ListTrades` orders `executed_at ASC`, so `?limit=1` returns the *oldest* trade and there is no cheap latest-trade probe today; read-side needs a new endpoint before it can even start. Worse, it pays an HTTP round trip on **every cached read, forever**, to detect an event that happens once per fill. The cache exists to avoid round trips.

Write-side costs one Redis `DEL` on the order path and nothing at all on the read path. It is also the ordinary shape of cache invalidation: the writer knows what it invalidated.

What it costs is worth stating plainly. trading-engine gains a Redis dependency it does not have today, and it has to name a key belonging to another service. §2.4 and §2.5 exist to make both of those loud rather than implicit.

### 2.2 Synchronous, not fire-and-forget

The invalidation runs inline in `PlaceOrder`, before it returns, not in a goroutine.

This is the decision most likely to be "improved" later into a background send, and doing that would silently restore the entire defect. The event this step exists to fix is *the dashboard refetching immediately after a fill*. A goroutine races that refetch, and it is not a close race: the refetch is one HTTP round trip away, and a goroutine on a busy server has no ordering guarantee at all. Losing that race puts the stale report back for the full five minutes, which is precisely what was reported.

The cost is one local Redis `DEL` on the order path, bounded at 500ms by §2.5's client. That is real latency on a write, and it is the right place to spend it.

### 2.3 It must never fail an order, and it must survive the client hanging up

Two failure modes, one rule each.

**A Redis failure is logged and swallowed.** The order committed. Returning a 500 after a successful fill would be a far worse bug than a stale cache, and retrying is not available either, because the trade is already durable. Invalidation is best-effort by construction, so it fails open and says so in the log. With no Redis configured at all, trading-engine places orders exactly as it does today.

**The delete uses `context.WithoutCancel`.** The fill commits, then the client disconnects, the request context is cancelled, a `DEL` on that context never runs, and the stale report survives. That is the original defect returning through a path nobody would think to look at. Keep the context's values, drop its cancellation.

### 2.4 The key is shared through `pkg/cachekeys`, and it takes a `uuid.UUID`

Today `insightsKey` is unexported inside ai-insights' cache package. Write-side invalidation means a second service must produce the same string, and the obvious version of that is `"insights:" + userID` written out in trading-engine, where a change to ai-insights' key format breaks nothing at compile time and everything at runtime, quietly.

So the key moves to `pkg/cachekeys` and both services call it. A format change then breaks both builds together, which is the only kind of coupling worth having across a service boundary.

**It takes a `uuid.UUID` rather than a string, and that is not decoration.** ai-insights parses the JWT subject to a UUID before keying on it precisely so an arbitrary subject cannot become an arbitrary Redis key, and a subject containing a colon cannot be shaped to look like another namespace. A string parameter lets any caller opt out of that guarantee; a `uuid.UUID` parameter makes it structural. Both services already hold the id in that type.

`pkg/cachekeys` adds no dependency: `pkg` already requires `google/uuid`.

### 2.5 trading-engine builds its Redis client with `ContextTimeoutEnabled`, and that is a copy

go-redis v9 defaults `ContextTimeoutEnabled` to false, and while it is false the client ignores context deadlines entirely and waits its own `ReadTimeout` instead. Code that reads as bounded is not. This already cost this project 6.05s on `GET /insights/portfolio` once.

The survey for this step found the fix in exactly one place, `ai-insights/internal/cache/client.go`, and found **`auth` and `market-data` both calling `redis.NewClient` directly without it**. Two services carry the latent bug today.

trading-engine gets its own small builder rather than becoming the third. That is deliberately a copy, not an extraction: putting the builder in `pkg` would add `go-redis` to a module whose only dependencies are `jwt` and `uuid`, and every service importing `pkg/auth` would inherit it, including `backtesting`, which uses no Redis at all. A ten-line function duplicated once is cheaper than that, and its comment names the canonical explanation rather than restating it.

The real fix is consolidating all four sites, which means touching `auth` and `market-data`. §6.

### 2.6 A rejected order does not invalidate

`recordRejection` writes an `orders` row and nothing else. No trade, no position, no balance change.

The report reads trades and the live portfolio. It never reads the orders table. So a rejection changes no input to any figure, and invalidating on one would discard a valid cached report to recompute a byte-identical replacement. Invalidation belongs after `ExecuteOrder` returns without error, not at the top of `PlaceOrder`.

### 2.7 The narrative cache needs nothing, and that is the design working

`narrative:{user_id}:{report_hash}` is keyed on the report's content. A fill produces a different report, so a different hash, so a different key, so a miss and a regeneration. There is nothing to invalidate, and an explicit delete would only be a way to get it wrong.

The consequence is worth naming rather than discovering on a bill: **after this step, a fill means the next narrative view is a fresh billed generation.** That is correct, because the prose describes figures that changed. It is also a real cost change in a way the report cache is not, and it only became possible now that Step 23 made the hash stable enough to key on.

---

## 3. The change

`pkg/cachekeys/` (new)

- `Insights(userID uuid.UUID) string`, returning `insights:{uuid}`. Carries the namespace convention comment currently living in `redis_insights_cache.go`.

`services/ai-insights/internal/cache/redis_insights_cache.go`

- `insightsKey` is deleted and both call sites use `cachekeys.Insights`. The produced key stays byte-identical to today's, which §4.5 asserts against a literal rather than against the function.

`services/trading-engine/internal/cache/` (new)

- `NewClient(opts *redis.Options) *redis.Client`, per §2.5.
- `RedisInsightsInvalidator` with `InvalidateInsights(ctx, userID uuid.UUID) error`: one bounded `DEL`.

`services/trading-engine/internal/service/`

- `InsightsInvalidator` interface, plus a no-op used when Redis is unconfigured, mirroring ai-insights' `noopCache` so the call site needs no branch.
- `PlaceOrder` invalidates after a successful `ExecuteOrder`, per §2.2, §2.3 and §2.6.

`services/trading-engine/cmd/server/main.go`

- Optional `REDIS_URL`, with a loud log when unset, matching ai-insights' precedent that an unset cache URL is a supported configuration and a noisy one.

`.env.example`

- `REDIS_URL` documented for trading-engine, including that orders work without it.

---

## 4. Tests

1. **A successful fill invalidates**, with the placing user's id.
2. **A rejected order does not.** Both rejection paths, per §2.6.
3. **A failing invalidator does not fail the order.** The result and error must be exactly what they are with a working one. §2.3's first rule, and the one whose absence would be found in production.
4. **Invalidation is not cancelled by the request context.** A cancelled context passed to `PlaceOrder` must still produce the delete. Without this, §2.3's `WithoutCancel` is one refactor from being dropped silently.
5. **The key matches, byte for byte.** `cachekeys.Insights` against a written-out `insights:{uuid}` literal. A test that builds its expectation with the function under test proves nothing.
6. **`miniredis` for the invalidator itself**, per Step 21's finding that a mock reimplementing the logic it stands in for cannot test it.

Each confirmed to fail against the unfixed code before being believed.

---

## 5. Verification

Unit and integration as usual, then the live check this actually needs: place a real order against the running stack with a report already cached, and confirm the next `GET /insights/portfolio` reflects the fill rather than the pre-trade figures. That is the user-visible defect, and nothing below the HTTP level proves it is gone.

The narrative endpoint stays untouched throughout. It is the only billed path, and §2.7 means a fill would now trigger a generation.

### 5.1 What the live run showed

A throwaway account with a reconciling three-buy history, a cached report, then real orders through `POST /trading/orders`.

| | Redis configured | No Redis (the control) |
|---|---|---|
| `insights:{user}` after the fill | **deleted** | **survives** |
| Refetched `behavior.trade_count` | **6**, matching Postgres | **4**, while Postgres held 5 |

The control is this step's defect reproduced exactly: five trades in the database, four in the reader's report. It also exercises the configuration §3 documents, since a trading-engine with no `REDIS_URL` is precisely a trading-engine that does not invalidate. Orders placed normally throughout it (HTTP 201).

**One figure legitimately does not move after a fill, and it reads like a miss.** A position's quantity stays at its pre-trade value, because holdings describe `as_of_date` — where the bar calendar ends — and a trade after that date is projected forward for the reconciliation guard only, never into reported positions. That is §2.12's documented tail truncation, not a failure to invalidate. `behavior.trade_count` is the figure that moves.

---

## 6. Open

**Consolidate the four Redis client construction sites.** `ai-insights` has the `ContextTimeoutEnabled` fix, `auth` and `market-data` do not, and this step adds a fourth that does. Both unfixed services ignore context deadlines on every Redis call today, which is the same defect that cost 6.05s in Step 21, sitting unexercised in two services. Its own step, because it touches auth's token revocation path.

**Caching histories by symbol instead of reports by user.** §1's non-goal, and the better design if the report cache ever causes a third defect. It would delete this step's problem rather than patch it, at the cost of rewriting §2.8.

**Nothing invalidates on a bar correction.** If `historical_prices` is ever backfilled or corrected, cached reports keep the old figures for five minutes and cached narratives keep them for a day. Not reachable today, since ingestion only appends, and worth remembering before it is.
