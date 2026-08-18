# Trading Frontend — Task Checklist (Step 15)

> **`SPEC.md` is APPROVED** — all ten design decisions resolved 2026-08-17, every
> one as recommended (§8). **Implementation is unblocked.**
>
> The UI for the four `/trading/*` endpoints Step 14 shipped. Frontend only; no
> backend, migration, or gateway change.

Full detail (acceptance criteria, verification, dependency graph, risks, the one
decision the spec left unplaced) in `tasks/plan.md`.

Branch: `step15-trading-frontend`.

Each checkpoint is a stop-for-review point per `agents.md`: implement, verify, **stop**.

---

## ⚠️ The two things that will go wrong if they go wrong quietly

**1. `latest_price: null` rendered as `0`, or the `unrealized_pl` it feeds rendered as
a real flat `0.00`.** The backend already made the price null on purpose; it also
returns a real `0.0` for `unrealized_pl` when the price is null, which is the exact
number a careless frontend would render as "flat, no change." `position-display.ts`
(Task 6) exists specifically so this rule lives in one tested place, not reinvented per
component.

**2. An `invalid_request` rejection reaching `OrdersTable` as a row.** The backend
never persists one — `ErrInvalidOrder`'s own comment says "it never became an
order." If `OrderTicket`'s client-side check has a gap, the *symptom* would be a
network request the backend correctly rejects and still doesn't persist — so this
would surface as confusing UI copy, not a data bug. Task 12 drives boundary values
through the real form to check for it directly.

---

### Phase 1: Wire layer and tooling
- [ ] **Task 1** — `vitest` + `@testing-library/react` + `jsdom` scaffold. `npm run test` script, smoke test proving it runs
- [ ] **Task 2** — Wire types (`Order`, `Trade`, `Position`, `PortfolioResponse`, `PlaceOrderRequest`, etc. in `api/types.ts`, field-for-field against `services/trading-engine/internal/service/types.go`) + four new `api/client.ts` methods
- [ ] **Task 3** — `format.ts` (`formatPrice` moved from `PriceList.tsx`, new `formatQuantity`), `PriceList.tsx` migrated
- [ ] **Task 4** — `rejection-reason.ts`: four known codes → readable copy. `invalid_request` deliberately excluded — it never reaches order history

- [ ] ⏸️ **Checkpoint A: Wire layer complete** — build/lint/test all green, no visible UI change

### Phase 2: Data hooks
- [ ] **Task 5** — `usePortfolio` (polls 15s, matching `usePrices`) and `useOrders` (fetch-on-mount only). Both expose `refetch()`. Guard against a stale response overwriting a newer one

### Phase 3: Components
- [ ] **Task 6** — 🔴 `position-display.ts`: the null-price/null-P&L rule, one pure function. Test must prove the em-dash output is identical whether `unrealized_pl` comes back as `0` or as a real number, when `latest_price` is null
- [ ] **Task 7** — `PositionsTable.tsx`, reading `portfolio.positions` — no second fetch
- [ ] **Task 8** — `PortfolioSummary.tsx`, with the muted "N valued at cost" note
- [ ] **Task 9** — `OrdersTable.tsx`, rejections included, exactly two status values (`filled`/`rejected`)
- [ ] **Task 10** — 🔴 `OrderTicket.tsx`. Client-side bounds (`0.0001`–`1e9`) block submission before any network call; success clears quantity and calls `onOrderPlaced()`; all four rejection reasons plus an `invalid_request` fallback plus a transport-error fallback are three visibly different messages
- [ ] **Task 11** — `Dashboard.tsx` wiring: three-column grid, tab strip (default Chart, unchanged landing view), hooks lifted once, header balance added

- [ ] ⏸️ **Checkpoint B: UI complete** — a buy and a sell both placed manually and reflected live, no refresh needed

### Phase 4: Verification and close-out
- [ ] **Task 12** — 🔴 Manual adversarial pass: all four rejections driven end to end with readable copy; garbage quantities never hit the network or persist; `market-data` killed mid-session (em-dashes, muted note, `upstream_unavailable` on a buy attempt — all three in one session); narrow-screen stacking; tab-switch doesn't lose in-progress input
- [ ] **Task 13** — Close-out: Step 15 in `PHASE2_CHECKLIST.md` with Task 12's findings; **rewrite** `docs/NEXT_SESSION.md`; archive spec/plan/todo

- [ ] ⏸️ **Checkpoint C: Step 15 complete** — build/lint/test all green. Ready to merge to `main`

---

## Decisions this plan makes that the spec did not

**Resolved 2026-08-17.** Reasoning in `tasks/plan.md` "One thing the spec named but didn't place":

1. **The §2.5 null rule lives in `trading/position-display.ts`**, one pure function
   consumed by both `PositionsTable` and `PortfolioSummary` — `SPEC.md` §5 required it
   be a pure function but §3's file list didn't name it

---

## Reminders that have cost time before

**Restart the frontend dev server after changing env vars**, not just code — `vite`
hot-reloads component changes but not `.env` changes. Not usually an issue this step
(`VITE_API_BASE_URL` isn't touched), noted because Step 14's reminders about
restarting backend services after code changes apply in spirit here too: if a trading
tab looks stale, check whether the right services are actually running before
debugging the frontend.

**Four backend services need to be up for this step's manual verification** —
`docker-up`, `run-auth`, `run-market-data`, `run-trading-engine`, `run-gateway`, same
as Step 14's own restart sequence in `docs/NEXT_SESSION.md`. `run-frontend` is the
fifth. A stopped `market-data` is Task 12's own test case, not a bug to chase if it
happens on purpose.

**`npm run test` is new this step.** If it's ever flaky or slow, that's information —
Step 14's backend testing philosophy (`docs/TESTING_STRUCTURE.md`) is deliberately
narrow rather than broad; this frontend suite should stay the same way (logic only, no
component-rendering suite) unless a real gap is found.
