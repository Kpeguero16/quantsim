# Implementation Plan — Trading Frontend (Step 15)

## Overview

`SPEC.md` is **approved** (2026-08-17); §8 records all ten decisions, every one resolved as recommended. This plan turns it into 13 tasks across 4 phases, on branch `step15-trading-frontend`.

Wire the four trading endpoints Step 14 shipped into the existing dashboard: a typed wire layer, two data hooks, and five new components (an order ticket, a positions table, an order history table, a portfolio summary, and a null-safety helper shared by two of them), landing inside `frontend/src/market/Dashboard.tsx`'s existing shell. **Frontend only** — no backend, migration, or gateway change (§1 Non-goals).

This is the first frontend step to add automated tests (§2.10) — `vitest` + `@testing-library/react`, scoped to the units that hold real logic rather than full component coverage.

---

## Architecture decisions

Restated from `SPEC.md` §2 — none of these are reopened here:

- **No router.** Tabs are `useState` inside `Dashboard.tsx` — §2.1
- **Three-column layout**: watchlist | tabbed content (Chart/Positions/Orders/Portfolio) | order ticket pinned across every tab — §2.2
- **One polled `usePortfolio`** (15s, matching `usePrices`), no separate positions call; **`useOrders`** fetches once and exposes `refetch()`, no interval — §2.3
- **Explicit `refetch()`** on both hooks immediately after a successful order — §2.4
- **`latest_price: null` ⇒ em-dash, and so does the `unrealized_pl` it feeds**, even though the backend returns a real `0.0` for the latter — §2.5
- **All four persisted rejection reasons appear in history; `invalid_request` never does** — it's never written to the `orders` table (§2.6)
- **Client-side quantity bounds only** (`0.0001`–`1e9`), no symbol pre-check — §2.7
- **Direct submit, no confirmation modal** — §2.8
- **Shared `frontend/src/format.ts`**, `PriceList.tsx` migrates to it — §2.9
- **`vitest` + Testing Library, scoped to logic** — §2.10

---

## One thing the spec named but didn't place

`SPEC.md` §5 says the null-price/null-P/L rule from §2.5 should be tested "as a pure function the component calls, not asserted through rendering," but §3's file list doesn't name that function. **This plan adds `frontend/src/trading/position-display.ts`**: a pure `derivePositionDisplay(position: Position) => { priceLabel, plLabel, plDirection }` that `PositionsTable` calls per row and `PortfolioSummary` uses indirectly (via a `hasUnpriced(positions)` helper in the same file) for its muted note. One implementation of the null rule instead of two components independently re-deriving it — the same reasoning Step 14's plan used to put weighted-avg-cost math in one place (`money.go`) rather than duplicating it.

---

## Dependency graph

```
T1 vitest + Testing Library scaffold
   │
   ├─> T2 wire types + api client methods
   │       │
   │       ├─> T3 format.ts (+tests), PriceList.tsx migrated
   │       ├─> T4 rejection-reason.ts (+tests)
   │       │
   │       └─> T5 use-portfolio.ts + use-orders.ts
   │               │
   │               ├─> T6 position-display.ts (+tests)
   │               │       │
   │               │       ├─> T7 PositionsTable.tsx
   │               │       └─> T8 PortfolioSummary.tsx
   │               │
   │               └─> T9 OrdersTable.tsx (needs T4)
   │
   └─> T10 OrderTicket.tsx (needs T2, T4)
           │
           └─> T11 Dashboard.tsx wiring (needs T5, T7, T8, T9, T10)
                   │
                   └─> T12 manual verification pass
                           │
                           └─> T13 docs close-out + merge
```

**Safe to parallelize if work is ever split:** T3 and T4 are independent of each other and of T5. T7 and T8 both depend on T6 but not on each other.

---

## Phase 1 — Wire layer and tooling

### Task 1 — `vitest` + Testing Library scaffold

**Description:** Everything else that has a test in this plan (T3, T4, T6) depends on this landing first. No trading code yet — just a runner and one smoke test proving it works.

**Files:** `frontend/package.json`, `frontend/vitest.config.ts`, `frontend/src/smoke.test.ts` (deleted once T3 lands and a real test exists to prove the setup instead)

- devDependencies: `vitest`, `@testing-library/react`, `@testing-library/jest-dom`, `jsdom`
- `"test": "vitest run"` script, `"test:watch": "vitest"` for local iteration
- `vitest.config.ts`: `environment: 'jsdom'`, picks up `vite.config.ts`'s existing aliases/plugins if any so imports resolve identically to the app build

**Acceptance criteria:**
- [ ] `npm run test` runs and passes on the smoke test
- [ ] `npm run build` and `npm run lint` are unaffected (test files excluded from the `tsc -b` build, matching how `*.test.ts` is conventionally excluded)

**Verification:**
- [ ] `cd frontend && npm run test` — green
- [ ] `cd frontend && npm run build` — still clean

**Dependencies:** None. **Scope:** S (3 files).

---

### Task 2 — Wire types and API client methods

**Description:** The typed contract for all four endpoints, mirroring `services/trading-engine/internal/service/types.go` field-for-field, matching `api/types.ts`'s existing convention (`SPEC.md` §6).

**Files:** `frontend/src/api/types.ts`, `frontend/src/api/client.ts`

- `types.ts` additions: `Side = 'buy' | 'sell'`, `Order` (`filled_price: number | null`, `rejection_reason: string | null` — pointers on the Go side, nullable here for the same reason), `Trade` (`realized_pl: number | null`), `Position` (`latest_price: number | null`), `PlaceOrderRequest`, `PlaceOrderResult`, `OrdersResponse`, `PositionsResponse`, `PortfolioResponse`
- `client.ts` additions to the `api` object: `placeOrder(body: PlaceOrderRequest)` → `POST /trading/orders`, `orders()` → `GET /trading/orders`, `positions()` → `GET /trading/positions`, `portfolio()` → `GET /trading/portfolio`. All four use the default `authenticated: true` — no change to `request()` itself

**Acceptance criteria:**
- [ ] Every field name matches the Go `json` tag exactly, including which fields are nullable (`Order.filled_price`, `Order.rejection_reason`, `Trade.realized_pl`, `Position.latest_price` — all four, and only those four, are `| null`)
- [ ] A header comment on the new block in `types.ts` names `services/trading-engine/internal/service/types.go` as the source of truth, matching the existing comment's convention
- [ ] No changes to `request()`, `refreshAccessToken()`, or any existing method — purely additive

**Verification:**
- [ ] `cd frontend && npm run build` — `tsc -b` clean
- [ ] `cd frontend && npm run lint` — clean

**Dependencies:** T1 (so later tasks have a working test setup; T2 itself has no tests — it's pure type/method declarations). **Scope:** S (2 files).

---

### Task 3 — `format.ts`, `PriceList.tsx` migrated

**Description:** Lift `PriceList.tsx`'s local `formatPrice` into a shared module, add `formatQuantity`. First real test of the vitest setup.

**Files:** `frontend/src/format.ts`, `frontend/src/format.test.ts`, `frontend/src/market/PriceList.tsx`

- `formatPrice(price: number): string` — moved verbatim (2-decimal, `toLocaleString`)
- `formatQuantity(quantity: number): string` — up to 4 decimal places, matching `quantityScale = 1e4` in the backend, but trims trailing zeros (10 shares reads as "10", not "10.0000")
- `PriceList.tsx`: delete its local `formatPrice`, import from `../format`

**Acceptance criteria:**
- [ ] `formatPrice(1234.5)` → `"1,234.50"`; `formatQuantity(10)` → `"10"`; `formatQuantity(0.0001)` → `"0.0001"`; `formatQuantity(1.5)` → `"1.5"`
- [ ] `PriceList.tsx` has no local `formatPrice` left; the watchlist renders unchanged

**Verification:**
- [ ] `cd frontend && npm run test` — new tests green
- [ ] Manual: watchlist prices in `npm run dev` render identically to before the migration

**Dependencies:** T1. **Scope:** S (3 files).

---

### Task 4 — `rejection-reason.ts`

**Description:** The four-value map from `SPEC.md` §2.6. Deliberately four, not five — see the boundary in §2.6 about `invalid_request` never reaching this path from order history (T9 relies on that; T10 handles `invalid_request` separately, inline).

**Files:** `frontend/src/trading/rejection-reason.ts`, `frontend/src/trading/rejection-reason.test.ts`

```ts
export type RejectionReason =
  | 'insufficient_balance'
  | 'insufficient_position'
  | 'symbol_unavailable'
  | 'upstream_unavailable'

export function describeRejection(reason: string): string
```

`describeRejection` takes the raw string from `Order.rejection_reason` (typed `string | null` in T2, since the wire value has no compile-time guarantee) and falls back to the raw string itself for anything unrecognized — never throws, never renders blank.

**Acceptance criteria:**
- [ ] All four known reasons map to a distinct, one-line, non-technical description
- [ ] An unknown string returns something render-safe (the raw string), not an exception
- [ ] A comment states why `invalid_request` is not one of the four cases, pointing at `SPEC.md` §2.6

**Verification:**
- [ ] `cd frontend && npm run test` — green

**Dependencies:** T1. **Scope:** S (2 files).

### ⏸️ Checkpoint A — Wire layer complete
- [ ] `npm run build`, `npm run lint`, `npm run test` all green
- [ ] No visible UI change yet — `PriceList.tsx`'s migration is the only rendering-adjacent edit, and it must be invisible
- [ ] **Review with Khalil before proceeding**

---

## Phase 2 — Data hooks

### Task 5 — `usePortfolio` and `useOrders`

**Description:** The two hooks every component in Phase 3 consumes. Same status-union shape `usePrices`/`useSymbols` already use, extended with an imperative `refetch` per `SPEC.md` §2.3–§2.4.

**Files:** `frontend/src/trading/use-portfolio.ts`, `frontend/src/trading/use-orders.ts`

```ts
export type PortfolioState =
  | { status: 'loading' }
  | { status: 'ok'; portfolio: PortfolioResponse }
  | { status: 'error'; message: string }

export function usePortfolio(intervalMs = 15_000): PortfolioState & { refetch: () => void }
```

`useOrders` is the same shape without the interval — fetch on mount, expose `refetch()`, no `setInterval` (§2.3's reasoning: nothing outside this session mutates these orders, so there's no clock-driven reason to poll).

Both hooks guard against a stale response landing after unmount or after a newer request has already superseded it, the same `cancelled` pattern `usePrices` already uses — this matters more here than there, since `refetch()` can now overlap with an in-flight poll tick.

**Acceptance criteria:**
- [ ] `usePortfolio` polls every `intervalMs` (default matching `POLL_INTERVAL_MS` from `use-prices.ts` — imported, not re-declared, so the two can't drift)
- [ ] `refetch()` on either hook triggers an immediate fetch outside the poll/mount cycle and does not reset state to `loading` if a good value is already held (avoids a UI flash on every refetch)
- [ ] An in-flight request that resolves after a newer one (poll tick fires during a `refetch()`, or vice versa) does not overwrite the newer result
- [ ] Both hooks stop firing after unmount (cleanup on the effect, matching `usePrices`)

**Verification:**
- [ ] `cd frontend && npm run build`, `npm run lint` clean
- [ ] Manual, once T11 wires them in: watch Network tab, confirm one portfolio request per 15s and exactly one orders request on mount

**Dependencies:** T2. **Scope:** M (2 files).

---

## Phase 3 — Components

### Task 6 — `position-display.ts`

**Description:** The null-safety rule from `SPEC.md` §2.5, as one pure function instead of two components independently reimplementing it (see "One thing the spec named but didn't place" above).

**Files:** `frontend/src/trading/position-display.ts`, `frontend/src/trading/position-display.test.ts`

```ts
export interface PositionDisplay {
  priceLabel: string   // formatted price, or an em-dash placeholder
  plLabel: string       // formatted P/L, or an em-dash placeholder
  plDirection: 'up' | 'down' | 'flat' | 'unknown'
}

export function derivePositionDisplay(position: Position): PositionDisplay
export function hasUnpricedPosition(positions: Position[]): boolean
```

`plDirection: 'unknown'` exists specifically so callers don't need to separately branch on `latest_price === null` to pick a text color — the em-dash and its neutral color both come from one field.

**Acceptance criteria:**
- [ ] `latest_price: null` ⇒ `priceLabel` is an em-dash, `plLabel` is an em-dash, `plDirection: 'unknown'` — **regardless of what `unrealized_pl` actually contains** (the test constructs a position with `latest_price: null, unrealized_pl: 0` and separately one with `unrealized_pl: 47.50`; both must produce the same em-dash output, proving the derivation keys off `latest_price` and never inspects `unrealized_pl` when it's null)
- [ ] `latest_price` present, positive `unrealized_pl` ⇒ `plDirection: 'up'`; negative ⇒ `'down'`; exactly `0` with a real price ⇒ `'flat'` (a real flat is a real flat — only the null case is `'unknown'`)
- [ ] `hasUnpricedPosition([])` is `false`; one unpriced position among several is `true`

**Verification:**
- [ ] `cd frontend && npm run test` — green, including the "same em-dash regardless of the backend's `0`" case above, which is the one a careless implementation would get wrong

**Dependencies:** T5 (for `Position` type reuse; no runtime dependency on the hook itself). **Scope:** S (2 files).

---

### Task 7 — `PositionsTable.tsx`

**Description:** Open positions from the shared portfolio poll — `PortfolioResponse.positions`, not a second `GET /trading/positions` call (§2.3).

**Files:** `frontend/src/trading/PositionsTable.tsx`

Columns: Symbol, Quantity (`formatQuantity`), Avg Cost (`formatPrice`), Latest Price (`position-display.ts`'s `priceLabel`), Unrealized P/L (`plLabel`, colored by `plDirection` using the existing `text-up`/`text-down` tokens from `index.css`, `text-ink-subtle` for `'unknown'`). Empty state ("No open positions.") when the array is empty — not an error, matching `OrdersResponse`'s own "never 404" convention.

**Acceptance criteria:**
- [ ] Loading/error/ok states match the `PortfolioState` passed in from `Dashboard.tsx` (no independent fetch inside this component)
- [ ] Every numeric cell uses `tabular font-mono`, matching `PriceList.tsx`'s existing convention
- [ ] A position with `latest_price: null` renders visibly differently (em-dash, muted color) from one at `$0.00`

**Verification:**
- [ ] `cd frontend && npm run build`, `npm run lint` clean
- [ ] Manual (once T11 wires the tab in): a position with a live price and one with `market-data` stopped, side by side, visibly distinct

**Dependencies:** T6. **Scope:** S/M (1 file).

---

### Task 8 — `PortfolioSummary.tsx`

**Description:** Cash, total equity, total unrealized P/L, plus the muted at-cost note from §2.5.

**Files:** `frontend/src/trading/PortfolioSummary.tsx`

Three stat figures (`formatPrice` throughout) plus, when `hasUnpricedPosition(portfolio.positions)` is true, a muted line: "N position(s) valued at cost — live price unavailable" (N computed by filtering, not hardcoded).

**Acceptance criteria:**
- [ ] The muted note appears only when at least one position has `latest_price: null`, and its count matches exactly
- [ ] `total_equity` and `total_unrealized_pl` render as plain formatted numbers — the note is the only signal of degraded pricing, not a change to the numbers themselves (the backend already values them at cost; the frontend does not recompute anything)

**Verification:**
- [ ] `cd frontend && npm run build`, `npm run lint` clean
- [ ] Manual: stop `market-data`, confirm the note appears with the right count and disappears once it's back and a poll ticks

**Dependencies:** T6. **Scope:** S (1 file).

---

### Task 9 — `OrdersTable.tsx`

**Description:** Full order history, rejections included, per §2.6.

**Files:** `frontend/src/trading/OrdersTable.tsx`

Columns: Time (`created_at`, relative or locale time), Symbol, Side, Quantity, Status badge (`filled` / `rejected` — exactly two values per `Order.Status`'s own comment that `'pending'` is never observed), Price (`filled_price` if filled, em-dash if rejected — never `rejection_reason` and `filled_price` rendered as if both could be present, since the backend guarantees exactly one is set), and a reason column that calls `describeRejection` only when `rejection_reason` is non-null.

**Acceptance criteria:**
- [ ] Rejected orders render with no price and a human-readable reason from `rejection-reason.ts`
- [ ] Filled orders render with a price and no reason text
- [ ] Newest-first ordering is preserved as returned by the API — this component does not re-sort (the backend already orders by `created_at DESC`)
- [ ] Empty history renders "No orders yet." — not an error

**Verification:**
- [ ] `cd frontend && npm run build`, `npm run lint` clean
- [ ] Manual (once T11 wires the tab in): after T12's rejection-driving pass, every rejected row shows readable copy, not a raw error code

**Dependencies:** T5, T4. **Scope:** S/M (1 file).

---

### Task 10 — `OrderTicket.tsx`

**Description:** The one component that writes. Buy/sell form for `Dashboard`'s currently-selected symbol.

**Files:** `frontend/src/trading/OrderTicket.tsx`

Props: `symbol: string | null`, `onOrderPlaced: () => void` (calls both hooks' `refetch()` — owned by `Dashboard`, not duplicated here per §2.4). Side toggle (buy/sell, keeps last choice per §2.8), quantity input (`type="number" min="0.0001" step="0.0001"`, soft ceiling at `1e9` per §2.7), disabled with a "Select a symbol to trade." message when `symbol` is null (mirrors the existing chart panel's placeholder copy in `Dashboard.tsx`).

On submit: client-side check first (quantity in range, side selected) — a failure here renders inline and **never calls the API**, so it can never produce a history row (§2.6's `invalid_request` boundary, enforced from the client side too, not just documented as a backend fact). A passing client-side check calls `api.placeOrder`; success clears the quantity field, keeps symbol/side, and calls `onOrderPlaced()`; a caught `ApiError` renders inline using `describeRejection(error.code)` for the four known codes, a fixed message for `invalid_request` (distinct from the four — this is the "malformed request slipped past client validation" case, e.g. a value the number input allowed that the backend still rejects), and `error.message` as the fallback for anything else (matching `ApiError`'s own transport-error convention).

**Acceptance criteria:**
- [ ] Client-side validation blocks submission for quantity `<= 0`, `< 0.0001`, `> 1e9`, non-numeric, or no side selected — none of these produce a network request
- [ ] The submit button is disabled while a request is in flight, and only then — not disabled by default while `symbol` is set
- [ ] A successful order clears quantity and calls `onOrderPlaced()` exactly once
- [ ] All four backend rejection reasons render via `describeRejection`; the `invalid_request` fallback and the transport-error fallback are visibly different from those four and from each other
- [ ] No confirmation modal (§2.8)

**Verification:**
- [ ] `cd frontend && npm run build`, `npm run lint` clean
- [ ] Manual (T12 drives all four rejection paths and the malformed-input path through this exact component)

**Dependencies:** T2, T4. **Scope:** M (1 file).

---

### Task 11 — `Dashboard.tsx` wiring

**Description:** Assemble everything built in T5–T10 into the three-column layout from §2.2. The only task that touches the existing file.

**Files:** `frontend/src/market/Dashboard.tsx`

- Grid: `grid-cols-1 lg:grid-cols-[320px_1fr_320px]` — watchlist (unchanged) | tab content | `OrderTicket` (new, always mounted)
- Tab state: `useState<'chart' | 'positions' | 'orders' | 'portfolio'>('chart')` — Chart stays the default so the existing landing view is unchanged for anyone who never opens a new tab
- `usePortfolio()` and `useOrders()` called once here, passed down: `PositionsTable`/`PortfolioSummary` get `portfolio` state, `OrdersTable` gets `orders` state, `OrderTicket` gets `symbol={selected}` and `onOrderPlaced={() => { portfolio.refetch(); orders.refetch() }}`
- Header gains a balance figure sourced from the same `usePortfolio()` call — no second fetch for it

**Acceptance criteria:**
- [ ] Exactly one `usePortfolio` and one `useOrders` instance in the component tree — no child component calls either hook itself
- [ ] Switching tabs does not remount `OrderTicket` or reset its in-progress input (it lives outside the tab-conditional render)
- [ ] The Chart tab's existing behavior (symbol selection, loading skeleton, `CandlestickChart`) is unchanged — this task adds to `Dashboard.tsx`, it does not rewrite what's already there
- [ ] Responsive: below `lg`, the three sections stack watchlist → tab content → order ticket, matching the existing single-column fallback

**Verification:**
- [ ] `cd frontend && npm run build`, `npm run lint` clean
- [ ] Manual: full click-through of all four tabs, resize below `lg` and confirm the stack order, place one order and watch balance/position/order all update without a page refresh

**Dependencies:** T5, T7, T8, T9, T10. **Scope:** M/L (1 file, largest single-file change in this step).

### ⏸️ Checkpoint B — UI complete
- [ ] `npm run build`, `npm run lint`, `npm run test` all green
- [ ] A buy and a sell both placed manually through the running app and confirmed in the UI (balance, position, order history all reflect it without a refresh)
- [ ] **Review with Khalil before proceeding**

---

## Phase 4 — Verification and close-out

### Task 12 — Manual adversarial pass

**Description:** `docs/NEXT_SESSION.md`'s standing instruction — green builds and green tests are not evidence a UI is correct. Drive every path `SPEC.md` calls out by name.

**Acceptance criteria — each is a specific thing to try, with the result written down:**
- [ ] Buy and sell, confirm balance/position/order all update within one interaction (no waiting for the next 15s poll)
- [ ] Drive all four rejection reasons and confirm each shows distinct, readable copy in `OrdersTable`, not a raw error code: an oversized buy (`insufficient_balance`), an oversized sell (`insufficient_position`), a symbol `market-data` has never cached (`symbol_unavailable`), `market-data` stopped mid-session (`upstream_unavailable`)
- [ ] Submit quantity `0`, a negative number, and a value below `0.0001` — confirm none of them reach the network (check the browser's Network tab, not just the UI) and none produce an order-history row once `market-data` is back up
- [ ] Stop `market-data`: confirm Positions shows em-dashes (not `$0.00`) for price and P/L, Portfolio shows the muted at-cost note with the right count, and a buy attempt fails with the `upstream_unavailable` copy — all three checked in the same session, the same posture split Step 14's own review verified on the backend
- [ ] Resize below the `lg` breakpoint and confirm the three sections stack in watchlist → content → order ticket order, and the order ticket is still usable (not clipped or overlapping)
- [ ] Switch tabs mid-typing in the order ticket's quantity field — confirm the value is not lost

**Verification:**
- [ ] Findings written into `PHASE2_CHECKLIST.md` in T13, including anything found *and* fixed, matching Step 14's write-up as the template

**Dependencies:** T11. **Scope:** M (verification; fixes as found, no fixed file list).

---

### Task 13 — Documentation and close-out

**Files:** `PHASE2_CHECKLIST.md`, `docs/NEXT_SESSION.md`, `docs/archive/phase2-step15-trading-frontend/{SPEC.md,plan.md,todo.md}`

**Acceptance criteria:**
- [ ] `PHASE2_CHECKLIST.md`: Step 15 written up, including T12's findings
- [ ] `docs/NEXT_SESSION.md` **rewritten**, not appended: what Step 16 should be (next candidate per `agents.md`'s roadmap — Phase 3 backtesting engine, or the two long-standing small items from the last `NEXT_SESSION.md`, whichever Khalil directs at close-out)
- [ ] Spec, plan, and todo archived under `docs/archive/phase2-step15-trading-frontend/`

**Verification:**
- [ ] `npm run build`, `npm run lint`, `npm run test` all green on the final commit
- [ ] Branch merged to `main`

**Dependencies:** T12. **Scope:** S (docs only).

### ⏸️ Checkpoint C — Step 15 complete
- [ ] All acceptance criteria met; all four endpoints have a working UI
- [ ] `npm run build`, `npm run lint`, `npm run test` all green
- [ ] **Ready for merge review**

---

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| The null-price/null-P&L rule (§2.5) gets reimplemented slightly differently in `PositionsTable` and `PortfolioSummary`, and one of them drifts | Medium — exactly the "0 reads as flat" bug `NEXT_SESSION.md` calls out, resurfacing in the frontend after the backend was careful to avoid it | T6 centralizes the rule in one tested function; T7/T8 both consume it rather than re-deriving |
| `usePortfolio`'s poll and `refetch()` race — a poll tick resolves after a `refetch()` triggered by a fresh order, overwriting the just-placed order's effect with stale data | Medium — a placed order would appear to not have happened until the next tick | T5's acceptance criteria explicitly require discarding a superseded response, the same `cancelled`-flag shape `usePrices` already uses |
| Client-side quantity validation (§2.7) silently drifts from the backend's actual bounds if either changes later | Low — the two are independent literals, not read from a shared source | Both bounds are commented with exactly where they come from (`services/trading-engine/internal/service/trading.go`'s `minQuantity`/`maxQuantity`) so a future change to either is easy to grep for, even though nothing enforces they stay in sync automatically |
| `invalid_request` accidentally becomes reachable as a history row if `OrderTicket`'s client-side check has a gap the backend's doesn't | Low — cosmetic, not a data-integrity issue (the backend still never persists it) | T10's acceptance criteria require the client check to match the backend's actual bounds, and T12 manually drives boundary values through the real form, not just the pure functions |
| Scope creep into limit orders, cancellation, or an equity chart | Low | §1 non-goals and this plan's task list are exhaustive — nothing here reaches for what the backend doesn't expose |

## Decisions resolved before implementation

Resolved 2026-08-17, all as recommended — the same form `SPEC.md` §8 uses.

| # | Decision | Resolution |
|---|---|---|
| — | Where the §2.5 null rule lives | **`trading/position-display.ts`**, one pure function, consumed by both `PositionsTable` and `PortfolioSummary` — named in this plan since `SPEC.md` §3 didn't place it |

**Remaining note, not a question:** 13 tasks against a 3–5 hr/week budget is a multi-session step, smaller than Step 14's 17. Checkpoints A–C are the natural session boundaries; Phase 3 (T6–T11) is the largest block.
