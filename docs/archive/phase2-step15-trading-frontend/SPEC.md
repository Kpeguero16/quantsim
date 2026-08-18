# SPEC — Trading Frontend: Order Ticket, Positions, Orders & Portfolio (Step 15)

Status: **Approved 2026-08-17.** All ten decisions resolved as recommended; §8 records them. Implementation is unblocked — not started.
Scope: `frontend/src/` only — a new `frontend/src/trading/` directory, additions to `frontend/src/api/{client.ts,types.ts}`, edits to `frontend/src/market/Dashboard.tsx`, and new frontend dev dependencies and `frontend/package.json` scripts for §2.10. No backend, migration, or gateway changes.

Prior specs archived at `docs/archive/phase1-step4-auth/` through `docs/archive/phase2-step14-trading-engine-mvp/`.

---

## 1. Objective

Step 14 shipped a complete, tested, documented trading API and deliberately built no UI against it (`docs/archive/phase2-step14-trading-engine-mvp/SPEC.md` §1 non-goals: "none of it exists yet and none is built here... sized as its own step once this API exists to build against"). `docs/NEXT_SESSION.md` already scoped this step down to four endpoints and two invariants the backend made visible on purpose. Nothing has changed since that scoping — this spec is that step.

**Objective:** wire `POST /trading/orders`, `GET /trading/orders`, `GET /trading/positions`, and `GET /trading/portfolio` into the existing dashboard (`frontend/src/market/Dashboard.tsx`) — an order ticket for the currently selected symbol, an open-positions table, an order history including rejections, and a portfolio summary (cash, total equity, total unrealized P/L) — using the same conventions the auth and market-data frontend work already established: a typed wire layer in `api/`, status-union hooks, Tailwind design tokens from `index.css`, and WHY-only comments.

**Non-goals:**
- **Limit orders, stop-loss, take-profit.** The backend only implements market orders (`services/trading-engine/internal/service/types.go`: `OrderTypeMarket` is the only order type); there is nothing for a richer form to submit to.
- **Order cancellation.** Orders fill or reject synchronously inside one request — there is no pending state to cancel (`Order.Status` comment: `'pending'` "is never observed by a caller").
- **WebSocket-pushed updates.** Same non-goal Step 14 carried forward; positions and orders are polled/fetched, not streamed.
- **An equity curve or any historical-performance chart.** No endpoint returns equity over time; `GET /trading/portfolio` is a point-in-time snapshot. `CandlestickChart.tsx` remains price-history only.
- **Multi-account, watchlist customization, or anything the backend doesn't support.** One account per user, a fixed default watchlist — not revisited here.
- **A native mobile layout.** Responsive within the existing Tailwind breakpoint conventions (`sm:`/`lg:`), not a redesign.

---

## 2. Design decisions

### 2.1 No router — tabs inside the existing shell

**(a)** Introduce `react-router-dom` now that the app has more than two logical views (chart, positions, orders, portfolio, plus login/dashboard).
**(b)** Keep `App.tsx`'s existing shape — no router — and add the new views as tab state inside `Dashboard.tsx`.

**Recommendation: (b).** `App.tsx`'s own comment is explicit about why there's no router today: "with exactly two views, react-router-dom would be a dependency, a provider, and route config for what a conditional does in one line... The trade is no deep links and no back button." Nothing in this step changes that trade — there's still no stated need for a shareable URL to "your positions tab" or browser-back between trading views, and a `useState<'chart' | 'positions' | 'orders' | 'portfolio'>` inside `Dashboard` costs nothing new. Reaching for a router the moment a fourth view appears would be adding a dependency to solve a problem (deep linking) nobody asked for.

### 2.2 Layout — three-column grid, order ticket always visible

`Dashboard.tsx` currently renders `grid-cols-1 lg:grid-cols-[320px_1fr]` (watchlist | detail panel). This step extends it to `grid-cols-1 lg:grid-cols-[320px_1fr_320px]`: watchlist unchanged on the left, a tab strip (Chart / Positions / Orders / Portfolio, defaulting to Chart so today's landing view doesn't change) driving the middle panel, and a new `OrderTicket` pinned in the right column regardless of which tab is active.

**Why pinned rather than its own tab:** placing an order is the one action that should never require leaving whatever you're looking at — checking a position and immediately trading it shouldn't cost a tab switch. On narrow screens (below `lg`), the grid collapses to one column and stacks watchlist → tab content → order ticket, matching the existing single-column fallback rather than inventing a second responsive pattern.

`OrderTicket` reads `symbol` from `Dashboard`'s existing `selected` state — the same value the chart panel already renders — rather than having its own symbol picker. There is one "currently looking at / currently trading" symbol, not two independently-selected ones.

### 2.3 Data fetching — one polled portfolio hook, one fetch-and-refetch orders hook

`GET /trading/portfolio` already returns `balance`, `positions`, `total_equity`, and `total_unrealized_pl` in a single call (`PortfolioResponse` in `services/trading-engine/internal/service/types.go`) — exactly so a dashboard doesn't have to compose three requests. A single `usePortfolio()` hook, polling on the same interval as `usePrices` (`POLL_INTERVAL_MS = 15_000` in `frontend/src/market/use-prices.ts`), feeds the header's balance display, the Positions tab, and the Portfolio tab. No separate `GET /trading/positions` call — `portfolio.positions` is a strict superset of what `GET /trading/positions` alone would return, so a second endpoint here would only be a second poll of overlapping data.

**Recommendation on cadence:** 15s, matching `usePrices`, because `total_equity` and every position's `unrealized_pl` are only as fresh as the prices they're priced against — polling the portfolio faster than the prices it depends on buys nothing.

`GET /trading/orders` is different: nothing outside this browser session can create an order against this account, so there's no clock-driven reason for it to refresh on its own. **Recommendation:** `useOrders()` fetches once on mount and exposes an imperative `refetch()`, with no `setInterval`. This mirrors `usePrices`'s own reasoning in reverse — that hook polls because the *underlying value* changes on a clock; this one doesn't, so it shouldn't either.

### 2.4 Explicit refetch after placing an order, not wait-for-next-tick

After a successful `POST /trading/orders`, `OrderTicket` calls both `usePortfolio`'s and `useOrders`'s `refetch()` directly, rather than letting the next poll tick pick up the new balance/position/order. Waiting up to 15s to see the balance move after you just traded would read as broken, not eventually-consistent. This is the one place `usePortfolio`/`useOrders` need an imperative escape hatch that `usePrices`/`useSymbols` don't have today — those two hooks have no caller that ever needs to force a refresh.

### 2.5 Null-safe rendering: `latest_price` and the unrealized P/L it feeds

`docs/NEXT_SESSION.md` already states the rule for `latest_price`: null means market-data couldn't price the holding right now, and rendering it as `0` or dropping the row reports the user as having lost everything they hold. `PriceList.tsx`'s existing `not-cached` branch (renders an em-dash, not a zero) is the precedent to reuse for the Positions table's `latest_price` column.

**One more null-safety case the backend's own comment flags but the frontend has to independently honor:** when `latest_price` is null, `Position.UnrealizedPL` is *not* also null — the backend computes it "against `avg_cost`" (SPEC §2.9 of Step 14), i.e. it comes back as a real `0.0`. A `0` unrealized P/L rendered next to a real price means "flat, no change." A `0` rendered because the price is simply unknown means something different, and showing the same "$0.00" for both would quietly misrepresent an unpriced position as a break-even one. **Recommendation:** whenever `latest_price` is null, render the Unrealized P/L cell the same way as the price cell (em-dash, muted), regardless of what number `unrealized_pl` actually holds — the union type driving both cells should key off `latest_price === null`, not off the P/L value.

The Portfolio tab gets the same treatment for `total_unrealized_pl` and `total_equity`, plus one addition: if any position in the response has a null `latest_price`, show a small muted note near those two figures (e.g. "N position(s) valued at cost — live price unavailable") so the summary doesn't read as more precise than it is. Cheap to build — it's a filter over the array already in hand — and it's the same principle `NEXT_SESSION.md` calls out, extended to the one screen that aggregates across positions.

### 2.6 Rejected orders, and the one rejection that never appears

`GET /trading/orders` includes rejected orders by design (Step 14 SPEC §2.5), and `OrdersTable` must show them, not filter them out — an order list showing only fills "would look complete while being wrong" (`NEXT_SESSION.md`). Each rejected order carries `rejection_reason`, one of four values the handler actually emits (`services/trading-engine/internal/handler/trading.go`): `insufficient_balance`, `insufficient_position`, `symbol_unavailable`, `upstream_unavailable`. `rejection-reason.ts` maps each to one line of user-facing copy.

**`invalid_request` is not in that list on purpose.** `ErrInvalidOrder`'s own comment in `services/trading-engine/internal/service/errors.go` says it plainly: "Nothing is persisted for one of these — it never became an order." A malformed submission (bad side, non-numeric quantity, empty symbol) never reaches the order table at all — it's purely an inline form-validation error in `OrderTicket`, shown and dismissed without ever appearing in history. `OrdersTable`'s status badge therefore only ever needs two states, `filled` and `rejected` (matching `Order.Status`'s own comment that `'pending'` is never observed by a caller) — there is no third "invalid" badge to design for, because the backend guarantees it can't exist as a row.

### 2.7 Client-side validation mirrors the backend's bounds, never replaces them

`OrderTicket`'s quantity field gets `min="0.0001"` `step="0.0001"`, matching `minQuantity = 1/quantityScale` in `services/trading-engine/internal/service/trading.go` (`quantityScale = 1e4`), and a soft upper bound at `maxQuantity = 1e9`. This exists purely to catch the common mistake before a round trip — the backend remains the sole authority, and every rejection it can still produce (a symbol that stops being priceable between page load and submit, a balance that changed in another tab) is handled by rendering whatever `rejection_reason` comes back, not by trying to out-guess it client-side. No symbol-availability pre-check either: Step 14 §2.7 deliberately has no separate whitelist server-side ("market-data's own `404` becomes the order's rejection reason directly"), and duplicating one here would be the exact coupling that decision avoided, moved to a different tier.

### 2.8 Direct submit, no confirmation modal

**(a)** A confirm-before-submit modal ("Buy 10 AAPL at market — confirm?").
**(b)** Submit directly on click; disable the button and show a pending state until the response returns; render the result (fill or rejection) inline.

**Recommendation: (b).** This is paper trading against a simulated balance — `agents.md`'s own priority order puts "UI polish" last and there's no real-money stake a confirm step is protecting against. A market order also fills synchronously in one round trip (Step 14 §2.6 — no async execution, no order book), so there's no meaningful window between "submit" and "know the outcome" for a confirmation step to usefully occupy. Keep the symbol and side (a trader placing several orders in the same name usually repeats both); clear the quantity field on a successful fill so a duplicate order isn't one accidental extra click away.

### 2.9 Shared formatting: extract `frontend/src/format.ts`

`PriceList.tsx` already has a local `formatPrice` (2-decimal, `toLocaleString`). This step needs the same formatting for cash, cost basis, P/L, and total equity across four new components, plus a `formatQuantity` for share counts (up to 4 decimal places, matching `quantityScale`). **Recommendation:** lift `formatPrice` out of `PriceList.tsx` into a new `frontend/src/format.ts` alongside a new `formatQuantity`, and have `PriceList.tsx` import it rather than keep its own copy. One small, in-scope refactor rather than five near-identical local copies of the same 6-line function.

### 2.10 Frontend test tooling: introduce it now, scoped to logic

`frontend/package.json` has no test runner today (`npm run lint` is `oxlint`; there is no `npm run test`) — Steps 8 and 13 shipped the entire auth and market-data frontend on manual verification alone, consistent with `agents.md`'s "Phase 1: manual/curl verification is sufficient; automated tests are optional or Phase 2+."

**(a)** Continue with manual verification only, matching every prior frontend step.
**(b)** Add `vitest` + `@testing-library/react` (+ `jsdom`) now, scoped to the units in this step that hold real logic: `rejection-reason.ts`'s mapping, the null-price/null-P/L rendering rule in §2.5, and `format.ts`'s formatting functions. Not component snapshot tests, not end-to-end coverage.

**Recommendation: (b), narrowly.** Every prior frontend step was rendering data the backend already validated (prices, symbols, `/me`). This step is the first to encode financial-correctness *rules* in the frontend itself — the null-price-vs-zero distinction, the four-reasons-not-five rejection mapping, the quantity floor — exactly the kind of logic `agents.md`'s testing guidance already asks backend code to isolate behind interfaces "so unit tests can inject mocks." A regression in `rejection-reason.ts` silently mapping `insufficient_position` to the wrong copy, or in the null-check in §2.5 quietly reverting to "0 means flat," is the kind of bug `make test`'s green backend suite would never catch and a human would only notice by accident. This is a smaller ask than it looks: three or four pure-function test files, no DOM rendering required for most of it, no change to `oxlint` or the build.

If Khalil prefers (a) for this step and reconsiders once the backtesting/AI-insights frontend work arrives, that's a reasonable call too — flagging it as a real decision rather than assuming it.

---

## 3. Project structure

```
frontend/src/
  format.ts                    # formatPrice (moved from PriceList.tsx), formatQuantity — new
  trading/
    OrderTicket.tsx             # buy/sell form for Dashboard's selected symbol
    PositionsTable.tsx          # open positions, from the shared portfolio poll
    OrdersTable.tsx              # order history, rejections included
    PortfolioSummary.tsx         # cash, total_equity, total_unrealized_pl
    use-portfolio.ts              # polls GET /trading/portfolio, exposes refetch()
    use-orders.ts                  # fetches GET /trading/orders, exposes refetch()
    rejection-reason.ts             # rejection_reason -> user-facing copy (4 values, §2.6)
  market/
    Dashboard.tsx                # +tab state, +order ticket column, +usePortfolio/useOrders
    PriceList.tsx                # formatPrice import replaces its local copy
  api/
    types.ts                     # +Order, Trade, Position, PortfolioResponse, PlaceOrderRequest, etc.
    client.ts                    # +placeOrder, orders, positions, portfolio
```

(If §2.10 is approved as recommended, also `frontend/src/format.test.ts`, `frontend/src/trading/rejection-reason.test.ts`, and a test for the §2.5 null-rendering rule — placement TBD once the component that owns that logic is written.)

---

## 4. Configuration

No new environment variables — `/trading/*` is already proxied through the gateway on the existing `VITE_API_BASE_URL` origin (`frontend/src/api/client.ts`'s `BASE_URL`), same as `/auth/*` and `/market-data/*`.

If §2.10 is approved: `frontend/package.json` gains `vitest`, `@testing-library/react`, `@testing-library/jest-dom`, `jsdom` as devDependencies, and a `"test": "vitest run"` script.

---

## 5. Testing strategy

- **If §2.10 is approved:** unit tests (`vitest`, no DOM) for `rejection-reason.ts`'s four-value mapping, `format.ts`'s `formatPrice`/`formatQuantity`, and the null-price/null-P/L derivation described in §2.5 (as a pure function the component calls, not asserted through rendering).
- **Manual verification either way**, against the real stack (`make run-trading-engine` + the rest of `docs/NEXT_SESSION.md`'s restart sequence): place a buy and a sell and watch balance/position/order update without a page refresh; drive each of the four rejection reasons (oversized buy, oversized sell, an un-cached symbol, `market-data` stopped) and confirm the order still appears in history with the right reason; confirm a malformed submission (empty quantity, quantity below `0.0001`) never produces a history row; kill `market-data` and confirm the Positions/Portfolio tabs still render with `latest_price`/P/L as em-dashes rather than erroring or showing `$0.00`.
- **`npm run lint`** (`oxlint`) and **`npm run build`** (`tsc -b && vite build`) clean, matching the bar every prior frontend step has held.

---

## 6. Code style

Continue what `frontend/src/` already established: WHY-only comments (no restating what the code says), wire types in `api/types.ts` mirroring the Go `json` tags field-for-field and snake_case (sources of truth: `services/trading-engine/internal/service/types.go`), status-union hook state (`{status: 'loading'|'ok'|'error'}`, extended here with whatever states each hook actually needs), and Tailwind's semantic tokens from `index.css` (`text-up`/`text-down` for P/L direction — reusing the same green/red the chart already uses rather than inventing a third palette; `text-ink-subtle` for the em-dash states; `tabular font-mono` for every numeric column, matching `PriceList.tsx`'s existing money formatting).

---

## 7. Commands

```bash
cd frontend && npm run dev      # :5173, same as today
cd frontend && npm run build
cd frontend && npm run lint
cd frontend && npm run test     # only if §2.10 is approved
```

---

## Boundaries

- **Always:** treat `latest_price: null` and its derived `unrealized_pl` as a display state, not a number to format (§2.5); keep rejected orders visible in `OrdersTable`, never filtered by status; run `npm run lint` and `npm run build` clean before each task's commit.
- **Ask first:** any new frontend dependency beyond what §2.10 already scopes (`vitest`/`@testing-library/react`/`jsdom`, if approved); any change to `api/client.ts`'s request/refresh machinery outside of adding the four trading methods; introducing `react-router-dom` (§2.1 recommends against it, but if the tab approach turns out to feel wrong in practice, that's a conversation, not a silent swap).
- **Never:** implement limit/stop/take-profit UI or an order-cancel action — the backend has nothing for either to call (§1); render a null `latest_price` or the `unrealized_pl` it produces as `0`/flat (§2.5); let an `invalid_request` rejection appear as a history row — it's a client-side validation message only (§2.6).

---

## 8. Decisions resolved before implementation

Resolved 2026-08-17, all as recommended:

| # | Decision | Resolution |
|---|---|---|
| 1 | Navigation model | **No router** — tabs as local state inside `Dashboard.tsx` — §2.1 |
| 2 | Layout | **Three-column grid**, order ticket pinned across all tabs rather than its own tab — §2.2 |
| 3 | Data fetching shape | **One polled `usePortfolio` (15s, matching price polling)**, no separate positions call; **`useOrders` fetches on mount + explicit refetch, no interval** — §2.3 |
| 4 | Post-order refresh | **Explicit `refetch()` on both hooks** after a successful order, not wait-for-next-tick — §2.4 |
| 5 | Null price / null P/L rendering | **Em-dash for both** whenever `latest_price` is null, regardless of the numeric `unrealized_pl` value; a muted note on the Portfolio tab when any position is priced at cost — §2.5 |
| 6 | Rejection handling | **All four persisted rejection reasons shown in history**; `invalid_request` is inline-form-only and never a history row — §2.6 |
| 7 | Client-side validation scope | **Mirrors the backend's quantity bounds only** (`0.0001`–`1e9`); no symbol pre-check — §2.7 |
| 8 | Order confirmation | **Direct submit, no modal** — §2.8 |
| 9 | Formatting | **Extract `frontend/src/format.ts`**, `PriceList.tsx` adopts it too — §2.9 |
| 10 | Frontend test tooling | **Add `vitest` + Testing Library now, scoped to logic-bearing units only** — §2.10 |

---

## 9. Implementation

Not started. `tasks/plan.md` and `tasks/todo.md` created next, per the gated workflow (`agents.md`: spec reviewed → plan → checkpoints).
