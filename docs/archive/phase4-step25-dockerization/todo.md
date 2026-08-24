# Todo — Dockerization (Step 25)

Tracks `tasks/plan.md`'s 5 tasks and 2 checkpoints. **All done. What remains is documentation and the merge.**

Branch `step25-dockerization`, cut from `main` at `f86c74f`. **Nothing committed yet.** Root `SPEC.md` and `tasks/` stay untracked as always.

---

## State of the machine

**Everything is put back.** The containerized stack is down, the host loop is verified working (`make run-auth` bound to 127.0.0.1:8081 and answered `/healthz` 200, then stopped cleanly). Exactly two containers in the project, Postgres and Redis, both up.

Database restored and **verified by query**: `users=20 accounts=20 trades=0 orders=0 positions=0 backtests=0`, `historical_prices=3525`. No `insights:*` or `narrative:*` keys.

The throwaway account and everything it touched are gone, backtests included -- it had 3 runs and 70 trade rows, and the first cleanup attempt **rolled back** on `backtests_user_id_fkey` rather than deleting a partial account. Worth knowing: a user that has run a backtest cannot be removed by the obvious five deletes.

**This step cost 2 narrative generations**, unlike Steps 23 and 24 at $0.00. Deliberate: the insights tab fires the narrative from a mount effect, not a button, so the tab cannot be looked at for free.

---

## The defect this step found in itself

**The first run of the container stack pointed the entire application at the wrong database, and every check passed anyway.**

`docker-compose.yml` built its connection string from `POSTGRES_DB`, which is the obvious variable and the wrong one. `POSTGRES_DB` names the database the postgres image creates at first boot -- here `quantsim`, which is an empty decoy. The application lives in `postgres`, which is what `DATABASE_URL` has always said, and `README.md` and `docs/PHASE2_CHECKLIST.md` both record that on purpose.

What made it dangerous is that it did not look like a failure. The migrate one-shot created the schema in the decoy on demand, so `/healthz` was 200, registration returned 201, the order filled at the right price, and positions came back correct. **The only symptom was `GET /insights/portfolio` returning 404 `symbol_unavailable`**, because that is the one endpoint needing data that was already there.

Fixed with `POSTGRES_APP_DB`, defaulting to `postgres`, documented at length in `.env.example` and in the compose file. The decoy was restored to empty (`DROP SCHEMA public CASCADE`) and confirmed at 0 tables.

Worth keeping: had the journey stopped at "order filled", this step would have shipped and the defect would have surfaced in deployment as data loss.

## T1 — The service image. Done.

`infra/docker/Dockerfile.service`, one file parameterised by `ARG SERVICE`, plus `.dockerignore`.

**`.dockerignore` denies by default.** `*` then `!pkg/ !services/ !frontend/ !infra/docker/`. The usual shape (list what to exclude) fails open, and the thing most worth keeping out of a layer is `.env`. It caught its own first mistake: `infra/docker/nginx.conf` was excluded and the frontend build failed loudly on the `COPY` rather than silently shipping a default config.

Context probe on the builder stage: `/src` contains `pkg` and `services/auth` and nothing else. No `go.work`, no `.env`, **0 `*_test.go`**.

All six images build. 15.3MB (gateway) to 21.6MB (ai-insights). Each refuses to start with `exit 1` and a named variable when its environment is missing.

## T2 — Compose, the app profile, the migration one-shot. Done.

`profiles: [app]` on all eight, so `docker compose up -d` still starts exactly two containers. Per-service `environment:` rather than `env_file`, `BIND_ADDR=0.0.0.0`, only 8080 and 5173 published, `read_only` + `cap_drop: ALL` + `no-new-privileges` everywhere.

`migrate/migrate:v4.19.0` as a one-shot on `service_completed_successfully`. Reports `no change` against a database already at 9.

**`make docker-down` had to change, and that was not in the plan.** Compose's `down` ignores services whose profile is not active, so a plain `docker compose down` after `stack-up` leaves seven containers holding a reference to the network it just deleted. The next `stack-up` then fails with `network <hash> not found`, which names nothing that leads back to the cause. `docker-down` now takes down every profile. `docker-up` is untouched, which is the promise that mattered.

## T3 — The frontend image. Done.

`node:22` build to `nginxinc/nginx-unprivileged:1.29-alpine`, port 5173, no SPA fallback.

**`nginx-unprivileged` rather than `nginx`** because stock nginx drops privileges itself and needs `CAP_SETUID` to do it, which `cap_drop: ALL` removes. The unprivileged image starts as uid 101 with no setuid step, so read-only root and dropped capabilities both hold. Confirmed on the running container: `user=101 readonly=true`.

**The empty-string trap in the build arg is real and is handled.** The app reads `import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080'`, and `??` falls back on null/undefined only. Exporting `VITE_API_BASE_URL=""` would compile the base URL to `""` and turn every API call into a same-origin request to nginx. The Dockerfile takes the `env -u` branch when the arg is empty. Verified by grepping the built bundle: `http://localhost:8080` is in it.

Also fixed: `expires 1y` plus an `add_header` emitted **two** `Cache-Control` headers. One directive now.

## T4 — Makefile, .env.example, README. Done.

`stack-up`, `stack-down`, `stack-build`, `stack-logs`, `stack-ps`. `.env.example` gained the two-run-modes header, the `POSTGRES_APP_DB` block, and the password-in-a-URL warning. `README.md` leads Local Development with a choice between the two ways to run.

`infra/docker/.gitkeep` removed; the directory has real files now.

## T5 — The adversarial pass. Done.

| # | Check | Result |
|---|---|---|
| 4.1 | Full journey through the containerized gateway | register 201, login 200, /me 200, price 200, order **filled 201**, positions 200, insights **200**, backtests 200 |
| 4.2 | **Control:** `BIND_ADDR=127.0.0.1` for auth alone | auth logs `listening on 127.0.0.1:8081`; gateway logs `dial tcp 172.18.0.5:8081: connect: connection refused`; login **502**. Restored: 401 |
| 4.3 | `.env` in any image | **0** `.env` files, **0** hits for `JWT_SECRET`, **0** for `ALPACA_API_SECRET`, across all seven |
| 4.3 | **Control:** an image built to contain `.env` | 1 file, 1 secret hit. The check can fail |
| 4.4 | Published ports | exactly `127.0.0.1` on 8080, 5173, 5432, 6379. The five internal services publish nothing |
| 4.5 | `docker compose build --no-cache`, `go.work` present in the tree | **exit 0, 7/7 images** |
| 4.6 | Dirty schema gates the stack | `error: Dirty database version 9`, migrate exit 1, **no app service started** -- only postgres and redis running |
| 4.7 | `make docker-up` after all this | **2 containers**, unchanged |
| 4.8 | Step 24 survives containerization | `insights:{user}` **1 -> 0** on the fill; refetched report 2 trades, Postgres 2 trades |
| 4.9 | Hardening applied to **running** containers | seven containers `running=true readonly=true caps=[ALL]`, six as `nonroot`, nginx as 101 |
| 4.10 | `make vet` / `make test` / `make test-integration` | clean / green / **63 PASS, 0 FAIL, 0 SKIP** |
| 2.11 | Source changes | **0** `.go`, `.ts` or `.tsx` files touched |

## Checkpoint A. Done.

The backend journey through the published gateway, before the frontend image existed. This is where the wrong-database defect surfaced.

## Checkpoint B. Done.

Mechanical: `GET /` 200 with the right script tag, `/does-not-exist` **404** (no SPA fallback), assets `public, max-age=31536000, immutable` as a single header, `/index.html` `no-cache`, and a CORS preflight from `Origin: http://localhost:5173` answered **204** with the matching origin.

Browser: signed in and worked every tab. Chart, watchlist, positions, orders, portfolio, backtests, insights, all rendering. Two market orders from the ticket filled and reconciled the balance to $75,970.54 to the cent; three backtests ran. **Every XHR hit `http://localhost:8080` and returned 200, and there were no console errors at all.**

Two things confirmed here that nothing below the browser reaches. Step 24's invalidation through the UI: the report read 1 trade before the browser orders and 3 after, with no stale window. And §2.7's consequence, priced: two generations landed on two different `narrative:{user}:{hash}` keys, because each fill changed the hash.

Host loop back afterwards, verified rather than assumed.

---

## Things that will trip you up

**Right after `make stack-up`, the first price request can 404.** `price:{symbol}` carries roughly a 40-second TTL and market-data repopulates it on a loop; before the first tick lands there is no cached price and `POST /trading/orders` is refused with `symbol_unavailable`. Measured: TTL held at 39 across six samples 15 seconds apart, so the loop does run in the container. Wait a few seconds, or retry.

**`docker exec` without `-i` silently discards a heredoc.** Already in the docs from Step 22 and it cost a cycle again here: the cleanup `psql` ran, printed nothing, and deleted nothing, and the row counts were identical afterwards, which reads as "already clean".

**`docker compose down` does not stop profiled services.** Hence the `docker-down` change above. If `stack-up` ever fails with `network ... not found`, the cure is `docker compose --profile app down`.

**A stack pointed at the wrong database looks healthy.** See the defect section. `behavior.trade_count` and the insights endpoint are what expose it; everything else passes.
