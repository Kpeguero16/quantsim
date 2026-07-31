# QuantSim Phase 1 — Trading Foundations Checklist

Work through these steps in order. Each step builds on the previous one.

**Prerequisites:** Go 1.21+, Node 18+, Docker, Docker Compose, [golang-migrate CLI](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate).

---

## Concepts: Env Files, Markdown, and Docker

If you're new to these, here's what they are and how QuantSim uses them.

### Env files (`.env` and `.env.example`)

- **What they are:** Plain-text files that hold **environment variables** — configuration like database URLs and API keys. Each line is usually `KEY=value` (no spaces around `=`). Example: `POSTGRES_USER=quantsim`.
- **Why two files?**
  - **`.env`** — Your **local** file with real secrets (passwords, API keys). It is in `.gitignore` and must **never** be committed. You create it by copying `.env.example` and filling in real values.
  - **`.env.example`** — A **template** committed to the repo. It lists every variable the app needs, with placeholder or example values (e.g. `POSTGRES_PASSWORD=your_password_here`). New developers copy it to `.env` and replace placeholders.
- **How apps use them:** Backend services (Go) often load `.env` via a library (e.g. `godotenv`) or you export variables in the shell before running. Docker Compose can inject variables from `.env` into containers.
- **Rules:** No quotes needed for simple values; no spaces around `=`. One variable per line. Lines starting with `#` are comments.

### Markdown (`.md` files)

- **What it is:** A simple format for documents: headings (`#`, `##`), lists (`-` or `1.`), **bold**, *italic*, and code (backticks or fenced blocks). This checklist is a Markdown file.
- **How QuantSim uses it:** `README.md` (project overview), `PHASE1_CHECKLIST.md` (this file), and `docs/` for design notes. You can edit `.md` in any text editor or in Claude Code.
- **No special runtime:** Markdown is for humans and docs; the app doesn’t “run” it.

### Docker and Docker Compose

- **Docker:** Runs apps in **containers** — isolated environments with their own filesystem and network. You use **images** (e.g. `postgres:16`) to start a **container** (the running instance).
- **Docker Compose:** A tool that reads a **`docker-compose.yml`** file and starts multiple containers together (e.g. Postgres + Redis) with one command: `docker compose up -d`.
- **Key pieces in `docker-compose.yml`:**
  - **services:** Each service is one container (e.g. `postgres`, `redis`).
  - **image:** Which image to run (e.g. `postgres:16`, `redis:7`).
  - **ports:** Map a port on your machine to a port in the container (e.g. `5432:5432` so you can connect to Postgres on `localhost:5432`).
  - **environment:** Variables the container sees (often from `.env`).
  - **volumes:** Persistent storage so data survives when you stop/restart containers.
- **Common commands:** `docker compose up -d` (start in background), `docker compose down` (stop and remove), `docker compose ps` (list running containers).

---

## Restructuring for testing (future unit tests)

The repo is structured so you can add unit tests later without big refactors. When implementing each service:

- Put **entrypoints** in `cmd/` (e.g. `cmd/server/main.go`) and **business logic** in a service layer that depends on **interfaces** (e.g. `UserStore`, `AccountStore`) so tests can inject mocks.
- Put unit tests in `*_test.go` next to the code; optional shared helpers in `pkg/testutil/`.
- See **docs/TESTING_STRUCTURE.md** for the full layout, interface patterns, and Makefile targets.

---

## Step 1: Repository Scaffolding

- [x] Create directory structure:
  - [x] `services/auth/`
  - [x] `services/market-data/`
  - [x] `services/gateway/`
  - [x] `services/trading-engine/` (empty placeholder)
  - [x] `services/backtesting/` (empty placeholder)
  - [x] `services/ai-insights/` (empty placeholder)
  - [x] `pkg/` (shared Go code: JWT middleware, DB helpers, common types)
  - [x] `frontend/`
  - [x] `infra/docker/`
  - [x] `infra/migrations/`
  - [x] `docs/`
- [x] Run `go mod init` in each service directory and in `pkg/`
- [x] Create root `.gitignore` (`.env`, binaries, `node_modules/`, `tmp/`, `*.exe`)
- [x] Create `.env.example` with placeholder keys (see **Concepts: Env files** above):
  - **Where:** One file at repo root named exactly `.env.example` (no space before the dot).
  - **Format:** One variable per line, `KEY=value`. Use placeholder values only (e.g. `your_password_here`); never put real secrets in this file.
  - **Variables to include:**
    - `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_HOST`, `POSTGRES_PORT` — used by Docker Postgres and by your Go apps to connect.
    - `DATABASE_URL` — full connection string for the migrate CLI and Go; e.g. `postgres://user:password@localhost:5432/dbname?sslmode=disable`. Use the same user/password/db as above.
    - `REDIS_URL` — e.g. `redis://localhost:6379/0`.
    - `JWT_SECRET` — long random string used to sign JWTs (e.g. generate with `openssl rand -base64 32`; in `.env.example` use a placeholder like `your_jwt_secret_here`).
    - `ALPACA_API_KEY`, `ALPACA_API_SECRET` — from [Alpaca](https://alpaca.markets); in the app these map to headers `APCA-API-KEY-ID` and `APCA-API-SECRET-KEY`.
  - **Optional:** Add short comments with `#` above each variable so others know what to set.
  - **After creating it:** Copy to `.env` (`cp .env.example .env` or duplicate in Explorer) and fill in real values only in `.env`; keep `.env` out of git.
- [x] Create `Makefile` with targets (see **Concepts** for why we use Make):
  - **What a Makefile is:** A file named `Makefile` at repo root. It defines **targets** (short names) and **recipes** (shell commands). Run with `make <target>` (e.g. `make docker-up`).
  - **Targets to add:**
    - `docker-up` — run `docker compose up -d` to start Postgres and Redis.
    - `docker-down` — run `docker compose down` to stop them.
    - `migrate-up` — run the migrate CLI to apply migrations (use `DATABASE_URL` from env; e.g. `migrate -path infra/migrations -database "$$DATABASE_URL" up`; in Makefiles use `$$` to pass a `$` to the shell).
    - `migrate-down` — run migrate down to roll back one migration.
    - `run-auth`, `run-market-data`, `run-gateway` — start each Go service (e.g. `go run ./cmd/server` or `go run .` from the service directory; ensure they load `.env` or that you export vars first). **Note:** Each target expects a runnable `main`; update the Makefile recipe to match the real entrypoint (e.g. `cd services/auth && go run ./cmd/server` once `cmd/server/main.go` exists).
  - **Tip:** On Windows you may need `make` installed (e.g. via Chocolatey, or use WSL). Alternatively you can run the same commands manually from the repo root.

---

## Step 2: Docker Compose

- [x] Create `docker-compose.yml` at repo root (see **Concepts: Docker and Docker Compose** above):
  - **File format:** YAML — indentation matters (use spaces, typically 2). Top-level key is `services:`; under it you list each container.
  - [x] **Postgres 16:**
    - **Service name:** e.g. `postgres` (you’ll use this as hostname when other containers talk to it; locally use `localhost`).
    - **Image:** `postgres:16`.
    - **Port:** Publish `5432:5432` so you can connect from your machine at `localhost:5432`.
    - **Environment:** Pass `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` from your `.env`. In Compose you can use `env_file: .env` or list each with `environment: POSTGRES_USER: ${POSTGRES_USER}` etc.
    - **Volume:** Use a **named volume** (e.g. `quantsim_pgdata`) mounted to `/var/lib/postgresql/data` so data persists when you run `docker compose down`.
  - [x] **Redis 7:**
    - **Image:** `redis:7`.
    - **Port:** `6379:6379` so you can use `localhost:6379` (or `redis://localhost:6379`).
    - **Volume:** Named volume (e.g. `quantsim_redis`) for persistence (optional but recommended).
  - [x] **(Optional) pgAdmin:** Image `dpage/pgadmin4`, port `5050:80`, and env vars for login; lets you browse Postgres in a web UI at `http://localhost:5050`.
- [x] Run and verify:
  - From repo root (with `.env` in place): `docker compose up -d`.
  - Check containers: `docker compose ps` (both should be “Up”).
  - Verify Postgres: `psql "$DATABASE_URL" -c '\dt'` (or use pgAdmin) — no tables yet until migrations run.
  - Verify Redis: `redis-cli -u "$REDIS_URL" PING` (should reply `PONG`).

---

## Step 3: Database Migrations

**What migrations are:** Versioned SQL scripts — `*_up.sql` creates or alters tables; `*_down.sql` reverses that. The migrate CLI tracks which version is applied and runs only new ones. This keeps schema changes repeatable and reversible.

- [x] Install [golang-migrate CLI](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate) (e.g. `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`; ensure `$GOPATH/bin` or `$HOME/go/bin` is on your PATH).
- [x] Create migration files in `infra/migrations/` (each pair shares a prefix; migrate runs them in order by version number):
  - [x] `001_users.up.sql` — `users` table (id UUID PK, email, username, password_hash, created_at, updated_at)
  - [x] `001_users.down.sql` — `DROP TABLE IF EXISTS users;`
  - [x] `002_accounts_portfolio.up.sql` — `accounts`, `positions`, `orders`, `trades` tables. Suggested schema: `accounts` (id, user_id, balance, currency, created_at, updated_at); `positions` (id, account_id, symbol, quantity, created_at, updated_at); `orders` (id, account_id, symbol, side, quantity, status, price/type, created_at); `trades` (id, account_id, order_id, symbol, side, quantity, price, executed_at)
  - [x] `002_accounts_portfolio.down.sql` — drop all four tables in reverse order (e.g. trades → orders → positions → accounts)
  - [x] `003_historical_prices.up.sql` — `historical_prices` table with composite index on (symbol, timeframe, timestamp)
  - [x] `003_historical_prices.down.sql` — drop `historical_prices`
- [x] Run migrations from repo root: ensure `DATABASE_URL` is set (e.g. `source .env` or export from `.env`), then run `migrate -path infra/migrations -database "$DATABASE_URL" up`. On Windows PowerShell use `$env:DATABASE_URL` if you exported from `.env` manually.
- [x] Verify all tables exist (e.g. `psql "$DATABASE_URL" -c '\dt'` or pgAdmin).

---

## Step 4: Auth Service

Working directory: `services/auth/`

- [x] Core DB dependencies and data layer:
  - [x] `jackc/pgx/v5` (Postgres), `google/uuid` — direct `require`s in `go.mod`
  - [x] `PostgresUserStore` / `PostgresAccountStore` in `internal/store/` (create user, get by email, create account)
  - [x] `UserStore` / `AccountStore` interfaces and request/response types in `internal/service/` (`RegisterRequest`, `LoginRequest`, `TokenPair`, `MeResponse`, etc.)
- [x] Add **direct** dependencies and wire them in HTTP code: `go-chi/chi/v5`, `golang-jwt/jwt/v5`, `golang.org/x/crypto` (bcrypt); run `go mod tidy` after adding imports.
- [x] Implement endpoints:
  - [x] `POST /auth/register` — hash password, insert user, create account with $100k balance, return JWT
  - [x] `POST /auth/login` — validate credentials, return access token + refresh token
  - [x] `POST /auth/refresh` — validate refresh token, issue new pair
  - [x] `GET /auth/me` — protected; return user profile from JWT claims
- [x] JWT config: HS256 signing, ~15min access token, ~7 day refresh token
- [x] Extract shared JWT middleware into `pkg/auth/` for reuse by other services
- [x] Return 4xx/5xx with JSON body (code + message) for invalid input and server errors
- [x] Test all endpoints with curl or Postman
- [x] Add unit tests per service (service + handler layers, table-driven, mocked stores; see `tasks/plan.md`)

---

## Step 5: Market Data Service — Alpaca Client + Historical Ingestion

Working directory: `services/market-data/`

*(Steps 5–8 are not started until Step 4 exposes a working HTTP auth API.)*

- [x] Add Go dependencies: `go-chi/chi/v5`, `jackc/pgx/v5`
- [x] Build Alpaca REST client (plain HTTP, no SDK):
  - Base URL: `https://data.alpaca.markets/v2`
  - Auth headers: `APCA-API-KEY-ID` (from `ALPACA_API_KEY`), `APCA-API-SECRET-KEY` (from `ALPACA_API_SECRET`)
- [x] Implement endpoints:
  - [x] `POST /market-data/ingest` — accepts symbol list, fetches **daily** bars from Alpaca (`/v2/stocks/{symbol}/bars`, timeframe=1Day), upserts into `historical_prices` by (symbol, timeframe, timestamp) for idempotency
  - [x] `GET /market-data/history/:symbol` — query historical candles from Postgres
  - [x] `GET /market-data/symbols` — return curated watchlist (AAPL, MSFT, GOOGL, AMZN, TSLA, SPY, QQQ)
- [x] Return 4xx/5xx with JSON body for errors
- [x] Run ingestion once to populate the database
- [x] Verify historical data is queryable

---

## Step 6: Market Data Service — Live Polling + Redis

- [x] Add `go-redis/redis/v9` dependency
- [x] Add background goroutine that:
  - [x] Polls Alpaca `/v2/stocks/snapshots` every 10–15 seconds for the watchlist
  - [x] Writes latest prices to Redis keys (e.g., `price:AAPL`)
  - [x] Publishes updates to Redis pub/sub channel `prices:{symbol}` (consumed by gateway/WebSocket in a later phase)
- [x] Implement endpoint:
  - [x] `GET /market-data/prices/:symbol` — read latest price from Redis cache
- [x] Start the service and verify Redis keys are populated and updating

---

## Step 7: API Gateway

Working directory: `services/gateway/`

- [x] Add dependency: `go-chi/chi/v5`
- [x] Build reverse proxy that:
  - [x] Routes `/auth/*` → auth service
  - [x] Routes `/market-data/*` → market data service
  - [x] Routes `/trading/*` → trading engine (placeholder for Phase 2 — answers `501`, see spec §2.6)
- [x] Apply shared JWT middleware (from `pkg/auth/`) on protected routes: protect `/market-data/*` and `/trading/*`; leave `/auth/*` public
- [x] Configure CORS for `localhost:5173` (Vite dev server)
- [x] Verify: all backend requests work through the single gateway port

Added beyond the original checklist, from deciding the spec's open questions with a security lens:

- [x] Strip any client-supplied `X-User-ID` at the edge on every route; inject the validated user ID from JWT claims on authenticated routes (for Phase 2's trading engine)
- [x] Bind all three services to `127.0.0.1` by default (`BIND_ADDR` overrides) — a bare `:port` binds every interface, which had been leaving auth and market-data open on the local network
- [x] Reject a `JWT_SECRET` shorter than 32 bytes at startup, in auth and gateway
- [x] `ReadHeaderTimeout` on the gateway; dial and response-header timeouts on the proxy transport

---

## Step 8: Minimal Frontend

- [x] Scaffold project: `npm create vite@latest frontend -- --template react-ts`
- [x] Add Tailwind CSS (v4, via the `@tailwindcss/vite` plugin — no `tailwind.config.js`)
- [x] Build pages:
  - [x] **Login / Register page** — form calling `/auth/register` and `/auth/login`, stores JWT **in memory only** (SPEC.md §2.5 — a page refresh logs you out, deliberately)
  - [x] **Dashboard shell** — after login, display symbol list with latest prices from `/market-data/prices/:symbol`, polled every 15s
  - [x] **Simple price chart** — fetch `/market-data/history/:symbol`, render OHLC candles with Lightweight Charts v5
- [x] Verify: can register, log in, see prices, and view a chart end-to-end

---

## Phase 1 Complete

When all boxes above are checked, you have a working foundation:
- User auth with JWT
- Market data flowing from Alpaca into Postgres and Redis
- A gateway unifying all services
- A minimal UI proving it all works

**Handoff criteria (before starting Phase 2):**
- [x] **Migrations run clean; all tables present** — verified 2026-07-30 by replaying all three migrations up *and* back down against a throwaway database (`quantsim_migrate_test`, since dropped), not just by inspecting the existing one. Up produced exactly the 7 expected tables; `down -all` removed them cleanly. Working DB is at version 3, not dirty.
- [x] **`.env.example` is complete and documented** — verified by diffing its keys against `.env`. Found and fixed a gap: `docker-compose.yml` reads `PGADMIN_EMAIL`/`PGADMIN_PASSWORD`, which were documented nowhere; they are now in `.env.example`. See the note below about the related dead keys in `.env`.
- [x] **Gateway routes `/auth/*`, `/market-data/*`, and `/trading/*` (placeholder)** — verified by curl against a cold-started stack: `/healthz` 200; `/auth/*` public; `/market-data/*` 401 without a token, 200 with; `/trading/*` 401 without a token and `501 not_implemented` with one; unknown paths return the standard `{code, message}` 404 shape.
- [x] **E2E: register → login → dashboard with prices → chart for one symbol works** — all eight steps of `SPEC.md` §3 run clean from a cold start (see Step 8 notes below).
- [ ] Auth-hardening step below is complete

**Known issue for Khalil (local `.env`, not tracked):** `.env` defines `ADMIN_EMAIL` and `ADMIN_PASSWORD`, but nothing reads those names — `docker-compose.yml` expects `PGADMIN_EMAIL`/`PGADMIN_PASSWORD`. Any custom pgAdmin credentials set there have silently never applied; pgAdmin has been falling back to its defaults (`admin@quantsim.local` / `admin`). Low impact — pgAdmin only starts under the `tools` profile and is bound to loopback — but worth renaming the two keys in your `.env`.

### Step 8 verification notes (2026-07-30)

Run from a genuine cold start: all services stopped, `docker compose down`/`up` (containers recreated), then every service restarted.

- **Token refresh actually exercised, not assumed.** `AccessTokenTTL` was temporarily lowered to 10s and the auth service restarted, then reverted. The network log captured the exact contract from `SPEC.md` §2.6: a poll tick produced **seven concurrent 401s → exactly one `POST /auth/refresh` (200) → all seven requests retried and succeeded**, with the session surviving. This is the real expiry path, not a corrupted-token stand-in.
- **`404 price_not_cached` → `—` confirmed live.** Previously deferred; forced here by clearing the Redis price keys (which leaves a ~8s window before the poller refills) and logging in within it. All seven rows rendered the em-dash with screen-reader text "no price cached yet" and **no error banner** — the intended markets-closed degradation. The chart stayed fully functional throughout, since history comes from Postgres rather than Redis. Prices self-healed on the next tick with no reload.
- **Single origin holds.** Across the whole session, every request went to `localhost:8080`; zero requests to `:8081` or `:8082`. The gated routes returning 200 is itself proof the `Authorization` header is being sent, since the gateway 401s without it.
- **No console errors and no CORS failures** at any point — the browser-side proof of the gateway's hand-written CORS middleware that Step 7 deferred to this step.
- Backend unaffected: all 9 Go test suites pass uncached (`go test -count=1 ./...` across `pkg`, `services/auth`, `services/market-data`, `services/gateway`). Frontend `npm run build` and `npm run lint` both clean.

---

## Step 9: Auth Input Validation (hardening, before Phase 2)

Found while drafting the Step 8 frontend spec (see that spec's §2.12): the auth service's registration path validates only that `email`, `username`, and `password` are **non-empty** (`services/auth/internal/handler/auth.go:28`). There is no password minimum length and no email format check anywhere in the handler or service layer — a one-character password and `email = "x"` both register successfully today.

Scheduled 2026-07-29 to land **after Step 8 and before Phase 2**: finishing Phase 1 first keeps the end-to-end momentum, and the fix lands while the auth service is still fresh rather than buried inside the trading-engine spec.

- [x] Write a short spec for the change (validation rules, error codes, where the check lives)
- [x] Enforce a password minimum length and a maximum (bcrypt errors above 72 bytes, which currently surfaces as a `500` rather than a `400`)
- [x] Validate email format
- [x] Return the standard `{code, message}` JSON shape for each rejection, consistent with the existing `invalid_request`
- [x] Table-driven tests covering each rule, matching the Step 4 test conventions
- [x] Verify the Step 8 register form still surfaces the backend's messages correctly

**Completed 2026-07-31.** Spec at `SPEC.md`, checkpoints in `tasks/plan.md` / `tasks/todo.md`.

The step grew past this list, because checking the draft's claims against the current text of **NIST SP 800-63B §3.1.1.2** reversed three of its own decisions:

- Password minimum is **15**, not 8 — 8 is the floor only when the password is one factor of several, and QuantSim has no MFA
- A **blocklist** check is a `SHALL` and the draft omitted it entirely — now 157 embedded entries plus context terms and trivial patterns
- **Case-insensitive usernames** moved from explicit non-goal to in scope, being the same impersonation class the username charset rule already guarded against
- Added: a **64 KiB request body cap**, since length validation runs after decoding and so cannot bound memory

Two bugs beyond the original list were found by reproducing against the running stack and are now fixed: the same address in two capitalisations created **two separate accounts**, and a user who registered as `Khalil@x.test` could not log in as `khalil@x.test`. Migration `004` adds unique indexes on `lower(email)` and `lower(username)`.

The property that made this safe to ship: **`Login` is deliberately not tightened** (`SPEC.md` §2.12), so raising the minimum locks nobody out. Asserted rather than assumed — by a unit test, and directly against the dev database, where an existing 10-character-password account still authenticates.

Verified in the browser end to end: the hint reads "At least 15 characters.", a short password renders the server's own message in the error region, and a valid registration reaches the dashboard.

**Deferred with reasoning, not omitted** (`SPEC.md` §7, tracked in `docs/security-backlog.md`): Argon2id, HIBP breach lookup, Unicode password normalisation, and — the largest remaining gap in the auth surface — **rate limiting on `/auth/login`**, which nothing throttles today.

---

## Phase 1: complete

All nine steps done. Next up: **Phase 2 — Trading Engine** (order execution, trade history, P/L tracking), which should open with **rate limiting** (`docs/security-backlog.md` items 1, 2, 4, 8) — Phase 2 is what makes account takeover consequential, since `/trading/*` moves a $100k simulated balance.
