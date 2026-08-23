# Plan — Report cache invalidation on a fill (Step 24)

Branch `step24-report-cache-invalidation`, cut from `main` at `e0ba025`.

Like Step 23, the risk here was not in the edit. It was in choosing which side of the seam does the work, and then in three details that would each have left the step looking finished while the defect survived: doing it asynchronously, letting a cancelled request take it with it, and letting a Redis outage fail an order.

Four tasks, one checkpoint.

---

## T1 — Choose the side of the seam

Establish what each design actually costs before picking. Specifically: does a cheap freshness probe exist today, does trading-engine have Redis, and which direction does the dependency already run.

Acceptance: a decision with the rejected option's cost stated in facts rather than preference.

## T2 — The shared key

Move `insightsKey` out of ai-insights and into `pkg/`, typed so a caller cannot pass an arbitrary string. Two services produce this string now, so it cannot be a literal in either.

Acceptance: ai-insights produces a byte-identical key to before, asserted against a literal rather than against the function.

## T3 — Invalidate on a fill

The interface, the no-op for unconfigured Redis, the Redis implementation, and the call in `PlaceOrder`. Plus `REDIS_URL` wiring and `.env.example`.

Acceptance: `go vet` clean, existing suites green, and orders still place with no Redis at all.

## T4 — Tests and the adversarial pass

Six tests per SPEC §4, then mutate the fix. The three that matter are the ones covering §2.2, §2.3 and §2.6, because each guards a way of shipping this that looks correct.

Acceptance: every mutant killed or explained, and every mutant confirmed to have applied and built.

## Checkpoint A — Runtime evidence

Place a real order through `POST /trading/orders` against the running stack with a report already cached, and confirm the next `GET /insights/portfolio` reflects it. Run the same sequence without Redis as the control, since a test that cannot fail proves nothing.

---

## Not in this step

Consolidating the four Redis client construction sites, two of which ignore context deadlines today. SPEC §6.
