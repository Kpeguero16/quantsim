# QuantSim Phase 1 — Trading Foundations Checklist

Work through these steps in order. Each step builds on the previous one.

**Prerequisites:** Go 1.21+, Node 18+, Docker, Docker Compose, [golang-migrate CLI](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate).

---

## Step 1: Repository Scaffolding

- [ ] Create directory structure:
  - [ ] `services/auth/`
  - [ ] `services/market-data/`
  - [ ] `services/gateway/`
  - [ ] `services/trading-engine/` (empty placeholder)
  - [ ] `services/backtesting/` (empty placeholder)
  - [ ] `services/ai-insights/` (empty placeholder)
  - [ ] `pkg/` (shared Go code: JWT middleware, DB helpers, common types)
  - [ ] `frontend/`
  - [ ] `infra/docker/`
  - [ ] `infra/migrations/`
  - [ ] `docs/`
- [ ] Run `go mod init` in each service directory and in `pkg/`
- [ ] Create root `.gitignore` (`.env`, binaries, `node_modules/`, `tmp/`, `*.exe`)
- [ ] Create `.env.example` with placeholder keys:
  - `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_HOST`, `POSTGRES_PORT`
  - `DATABASE_URL` (e.g. `postgres://user:password@localhost:5432/dbname?sslmode=disable` for golang-migrate)
  - `REDIS_URL`
  - `JWT_SECRET`
  - `ALPACA_API_KEY`, `ALPACA_API_SECRET` (mapped to Alpaca headers: KEY → `APCA-API-KEY-ID`, SECRET → `APCA-API-SECRET-KEY`)
- [ ] Create `Makefile` with targets: `docker-up`, `docker-down`, `migrate-up`, `migrate-down`, `run-auth`, `run-market-data`, `run-gateway`

---

## Step 2: Docker Compose

- [ ] Create `docker-compose.yml` at repo root with:
  - [ ] Postgres 16 — port 5432, env vars from `.env`, named volume for data persistence
  - [ ] Redis 7 — port 6379, named volume
  - [ ] (Optional) pgAdmin — port 5050 for visual DB inspection
- [ ] Run `docker compose up -d` and verify both services are reachable

---

## Step 3: Database Migrations

- [ ] Install [golang-migrate CLI](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate)
- [ ] Create migration files in `infra/migrations/`:
  - [ ] `001_users.up.sql` — `users` table (id UUID PK, email, username, password_hash, created_at, updated_at)
  - [ ] `001_users.down.sql` — drop `users`
  - [ ] `002_accounts_portfolio.up.sql` — `accounts`, `positions`, `orders`, `trades` tables. Suggested schema: `accounts` (id, user_id, balance, currency, created_at, updated_at); `positions` (id, account_id, symbol, quantity, created_at, updated_at); `orders` (id, account_id, symbol, side, quantity, status, price/type, created_at); `trades` (id, account_id, order_id, symbol, side, quantity, price, executed_at)
  - [ ] `002_accounts_portfolio.down.sql` — drop all four tables
  - [ ] `003_historical_prices.up.sql` — `historical_prices` table with composite index on (symbol, timeframe, timestamp)
  - [ ] `003_historical_prices.down.sql` — drop `historical_prices`
- [ ] Run migrations from repo root: `migrate -path infra/migrations -database "$DATABASE_URL" up` (use `DATABASE_URL` from `.env`)
- [ ] Verify all tables exist

---

## Step 4: Auth Service

Working directory: `services/auth/`

- [ ] Add Go dependencies:
  - `go-chi/chi/v5` (router)
  - `jackc/pgx/v5` (Postgres driver)
  - `golang-jwt/jwt/v5` (JWT)
  - `golang.org/x/crypto` (bcrypt)
- [ ] Implement endpoints:
  - [ ] `POST /auth/register` — hash password, insert user, create account with $100k balance, return JWT
  - [ ] `POST /auth/login` — validate credentials, return access token + refresh token
  - [ ] `POST /auth/refresh` — validate refresh token, issue new pair
  - [ ] `GET /auth/me` — protected; return user profile from JWT claims
- [ ] JWT config: HS256 signing, ~15min access token, ~7 day refresh token
- [ ] Extract shared JWT middleware into `pkg/auth/` for reuse by other services
- [ ] Return 4xx/5xx with JSON body (code + message) for invalid input and server errors
- [ ] Test all endpoints with curl or Postman
- [ ] (Optional) Add 1–2 integration tests per service

---

## Step 5: Market Data Service — Alpaca Client + Historical Ingestion

Working directory: `services/market-data/`

- [ ] Add Go dependencies: `go-chi/chi/v5`, `jackc/pgx/v5`
- [ ] Build Alpaca REST client (plain HTTP, no SDK):
  - Base URL: `https://data.alpaca.markets/v2`
  - Auth headers: `APCA-API-KEY-ID` (from `ALPACA_API_KEY`), `APCA-API-SECRET-KEY` (from `ALPACA_API_SECRET`)
- [ ] Implement endpoints:
  - [ ] `POST /market-data/ingest` — accepts symbol list, fetches **daily** bars from Alpaca (`/v2/stocks/{symbol}/bars`, timeframe=1Day), upserts into `historical_prices` by (symbol, timeframe, timestamp) for idempotency
  - [ ] `GET /market-data/history/:symbol` — query historical candles from Postgres
  - [ ] `GET /market-data/symbols` — return curated watchlist (AAPL, MSFT, GOOGL, AMZN, TSLA, SPY, QQQ)
- [ ] Return 4xx/5xx with JSON body for errors
- [ ] Run ingestion once to populate the database
- [ ] Verify historical data is queryable

---

## Step 6: Market Data Service — Live Polling + Redis

- [ ] Add `go-redis/redis/v9` dependency
- [ ] Add background goroutine that:
  - [ ] Polls Alpaca `/v2/stocks/snapshots` every 10–15 seconds for the watchlist
  - [ ] Writes latest prices to Redis keys (e.g., `price:AAPL`)
  - [ ] Publishes updates to Redis pub/sub channel `prices:{symbol}` (consumed by gateway/WebSocket in a later phase)
- [ ] Implement endpoint:
  - [ ] `GET /market-data/prices/:symbol` — read latest price from Redis cache
- [ ] Start the service and verify Redis keys are populated and updating

---

## Step 7: API Gateway

Working directory: `services/gateway/`

- [ ] Add dependency: `go-chi/chi/v5`
- [ ] Build reverse proxy that:
  - [ ] Routes `/auth/*` → auth service
  - [ ] Routes `/market-data/*` → market data service
  - [ ] Routes `/trading/*` → trading engine (placeholder for Phase 2)
- [ ] Apply shared JWT middleware (from `pkg/auth/`) on protected routes: protect `/market-data/*` and `/trading/*`; leave `/auth/*` public
- [ ] Configure CORS for `localhost:5173` (Vite dev server)
- [ ] Verify: all backend requests work through the single gateway port

---

## Step 8: Minimal Frontend

- [ ] Scaffold project: `npm create vite@latest frontend -- --template react-ts`
- [ ] Add Tailwind CSS
- [ ] Build pages:
  - [ ] **Login / Register page** — form calling `/auth/register` and `/auth/login`, stores JWT in memory or localStorage/sessionStorage
  - [ ] **Dashboard shell** — after login, display symbol list with latest prices from `/market-data/prices/:symbol`
  - [ ] **Simple price chart** — fetch `/market-data/history/:symbol` for one symbol, render OHLC candles with Lightweight Charts or Recharts
- [ ] Verify: can register, log in, see prices, and view a chart end-to-end

---

## Phase 1 Complete

When all boxes above are checked, you have a working foundation:
- User auth with JWT
- Market data flowing from Alpaca into Postgres and Redis
- A gateway unifying all services
- A minimal UI proving it all works

**Handoff criteria (before starting Phase 2):**
- Migrations run clean; all tables present
- `.env.example` is complete and documented
- Gateway routes `/auth/*`, `/market-data/*`, and `/trading/*` (placeholder)
- E2E: register → login → dashboard with prices → chart for one symbol works

Next up: **Phase 2 — Trading Engine** (order execution, trade history, P/L tracking).
