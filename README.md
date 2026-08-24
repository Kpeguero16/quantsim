# QuantSim — Real-Time Trading & Strategy Backtesting Platform

## Overview

**QuantSim** is a full‑stack, distributed trading simulation platform that uses real market data to power simulated (paper) trading, advanced strategy backtesting, and AI‑generated portfolio insights.

The platform is designed to replicate the architecture and behavior of modern fintech trading systems while remaining a safe environment for users to test strategies and analyze performance without risking real capital.

This project demonstrates real‑time systems design, financial data processing, cloud infrastructure, containerization, and AI integration.

---

# Status

QuantSim is in active development (Phase 4). This section reflects what's actually built, not the target design further down.

| Service | State |
|---|---|
| `services/auth` | Built — registration, login, JWT issuance, case-insensitive identity lookup, per-IP and per-account rate limiting on `/auth/*`, refresh-token revocation on logout |
| `services/gateway` | Built — reverse proxy to auth, market-data, trading-engine, backtesting, and ai-insights; auth-aware rate limiting, router-wide request body cap |
| `services/market-data` | Built — Alpaca ingestion, Redis caching |
| `services/trading-engine` | Built — market buy/sell orders, positions, portfolio, trade history; frontend dashboard (order ticket, positions, trade/order history) |
| `services/backtesting` | Built — moving-average-crossover, RSI, and MACD strategies, next-bar-open fills, the five `agents.md` §3 metrics; multi-symbol portfolio runs drawing on one shared pool of capital (1–10 symbols); frontend dashboard tab (strategy form, results, run history) |
| `services/ai-insights` | Built — `GET /insights/portfolio`: risk (position weights, cash weight, concentration HHI, annualized volatility, max drawdown), benchmarking against buy-and-hold SPY and QQQ, and behavioral findings with the trade IDs that caused them. Every figure is derived from an equity curve reconstructed on demand from the trade log and stored bars, and reconciled against the live account. Deterministic and rule-based — the LLM layer that *phrases* these numbers is a later step |

Schema is at migration version 9. Auth, trading-engine, and backtesting each have a store-layer integration test suite that runs against a real Postgres (`make test-integration`); everything else is unit-tested against in-memory fakes. The frontend has its own `vitest` suite (61 tests) for validation and error-mapping logic. No CI is wired up yet.

For a detailed, checkpointed history of what's shipped, see `docs/PHASE1_CHECKLIST.md`, `docs/PHASE2_CHECKLIST.md`, and `docs/PHASE3_CHECKLIST.md`. For what's next, see `docs/NEXT_SESSION.md`.

---

# Local Development

There are two ways to run QuantSim, and they publish the same ports, so pick one:

| | |
|---|---|
| **The whole stack in Docker** — one command, nothing to install but Docker | [jump](#run-the-whole-stack-in-docker) |
| **Services on the host** — Postgres and Redis in Docker, the Go services under `go run`. A code change is one restart away, which is why this is the development loop | [jump](#run-the-services-on-the-host) |

## Run the whole stack in Docker

Needs only Docker and a `.env`. No Go toolchain, no Node, no `migrate` CLI: migrations run as a one-shot container that every service waits on.

```bash
cp .env.example .env        # fill in real values (DB password, JWT secret, Alpaca keys)
make stack-up               # build the images and start everything
```

Then open **http://localhost:5173**. `make stack-logs` follows every service, `make stack-down` stops it all.

**One origin.** Caddy serves the compiled frontend and proxies `/auth`, `/market-data`, `/trading`, `/backtests` and `/insights` to the gateway, so the browser only ever talks to 5173. The bundle contains no API host, and nothing crosses origins. Only 5173 is published (plus Postgres and Redis on loopback for `make test-integration`); the gateway and the five services publish nothing at all.

To deploy this to AWS, see **`docs/DEPLOYMENT.md`**. auth, market-data, trading-engine, backtesting and ai-insights are reachable only from the compose network.

**`make docker-up` still starts Postgres and Redis and nothing else.** The application services sit behind compose's `app` profile precisely so that stays true.

**If a report says no historical data is available, check `POSTGRES_APP_DB`.** The container stack builds its own connection string and takes the database name from that variable, defaulting to `postgres`. Pointed at the wrong database it does not look broken — migrations create the schema wherever they land, so registration and orders both succeed and only a report that needs pre-existing bars gives it away. See the note in `.env.example`.

## Run the services on the host

### Prerequisites
- Go 1.25+
- Node 18+
- Docker & Docker Compose
- [golang-migrate CLI](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate)

### Setup

```bash
cp .env.example .env        # fill in real values (DB password, JWT secret, Alpaca keys)
make docker-up               # start Postgres + Redis
make migrate-up               # apply migrations
```

### Running services

Each runs in its own terminal:

```bash
make run-auth             # :8081
make run-gateway          # :8080
make run-market-data      # :8082
make run-trading-engine   # :8083
make run-backtesting      # :8084
make run-ai-insights      # :8085
make run-frontend         # :5173 (Vite dev server)
```

### Testing

```bash
make test               # unit tests, all modules — no Docker needed
make test-integration   # auth/trading-engine/backtesting store tests against a real Postgres — needs `make docker-up`
make test-all           # both
make vet                # go vet, including files behind the integration build tag
```

Frontend, from `frontend/`:

```bash
npm run lint    # oxlint
npm run build   # tsc -b && vite build
npm run test    # vitest run
```

### Other useful targets

```bash
make docker-down      # stop Postgres + Redis
make docker-ps        # list running containers
make stack-build      # rebuild the container images without starting anything
make stack-ps         # list the containerized stack
make migrate-down     # roll back one migration
make test-db-drop     # drop the quantsim_test database
```

Run `make help` for the full list.

**Watch out:** `DATABASE_URL` points at the `postgres` database — that's where real dev data lives. `POSTGRES_DB=quantsim` is a separate, empty database. `psql -d quantsim` connecting successfully and showing no tables is expected, not data loss.

---

# Core Features

## 1. Real Market Data Ingestion — ✅ built

### Description
QuantSim ingests real stock market data (via Alpaca) and streams it throughout the platform to power trading simulations, charts, and analytics.

### Data Types
- OHLC candle data
- Tick price updates
- Trading volume
- Historical price history

### Architecture Flow

```
Market Data API → Data Ingestion Service → Redis → WebSockets → Frontend
```

### Implementation Phases

**Phase 1 (MVP)** — done
- REST polling every few seconds
- Normalized price storage

**Phase 2 (Advanced)** — not started
- Live WebSocket market feeds
- Stream processing

### Engineering Signals
- External API ingestion
- Streaming pipelines
- Data normalization

---

## 2. Simulated Trading Engine — ✅ built (MVP)

### Description
Users execute simulated trades using real market prices. All trades are paper trades — no real money involved.

### Order Execution Flow

1. User submits buy/sell order
2. System validates balance
3. Latest market price retrieved
4. Simulated fill executed
5. Portfolio updated
6. Trade stored in database

### Supported Order Types

**MVP** — done
- Market buy
- Market sell

**Advanced** — not started
- Limit orders
- Stop‑loss
- Take‑profit

### Core Data Models
- Users
- Accounts
- Positions
- Orders
- Trade History

### Engineering Signals
- Financial systems modeling
- Transaction processing
- Portfolio state management

---

## 3. Strategy Backtesting Engine ⭐ — ✅ built (MVP)

### Description
Users can test trading strategies against historical market data to evaluate performance before applying them in live simulations.

### Example Use Case

> "If I bought when the 50‑day moving average crossed the 200‑day moving average, what would my returns be?"

---

### Strategy Input Methods

**Option A — Prebuilt Strategies**
- Moving average crossover — done
- RSI signals — done
- MACD signals — done

**Option B — Configurable Strategies**
Users define:
- Indicators
- Thresholds
- Timeframes

**Option C — Code‑Based Strategies (Stretch)**
- User‑uploaded Python scripts
- Sandboxed execution

---

### Backtesting Architecture

```
Historical Data → Strategy Engine → Trade Simulator → Performance Metrics
```

---

### Performance Metrics — done

- Total return (%)
- Sharpe ratio
- Max drawdown
- Win rate
- Profit factor

Displayed via a metrics grid and trade log in the dashboard's Backtest tab (no equity-curve chart yet — the backend does not store one).

---

### Data Requirements

- Daily candles (MVP)
- Minute candles (advanced)
- Multi‑year historical datasets

Stored in Postgres or columnar storage.

---

### Engineering Signals
- Algorithmic trading systems
- Data processing pipelines
- Quantitative performance analytics

---

## 4. AI Trade Insights Engine ⭐ — 🚧 not started

### Description
Generates intelligent insights about user portfolios, strategies, and trading behavior.

---

### Insight Categories

#### Portfolio Risk Analysis
- Sector concentration
- Volatility exposure
- Diversification scoring

#### Performance Benchmarking
- Compare vs S&P 500
- Compare vs NASDAQ
- Risk‑adjusted performance

#### Behavioral Analysis
- Overtrading detection
- Panic selling patterns
- Risk/reward profiling

#### Strategy Evaluation
- Outperformance vs buy‑and‑hold
- Drawdown risk analysis

---

### Architecture

```
Trade Data + Portfolio Data → Analytics Service → AI Insight Generator → Dashboard
```

---

### Implementation Phases

**Phase 1 — Rule‑Based Insights**
- SQL analytics
- Heuristic scoring

**Phase 2 — LLM Insights**
- Structured prompts
- Narrative trade analysis

---

### Engineering Signals
- AI product integration
- Financial analytics
- Data storytelling systems

---

# System Architecture

This is the target architecture. See [Status](#status) for what's actually running today.

```
                Market Data APIs
                        │
                Data Ingestion (Go)
                        │
                      Redis
                        │
        ┌───────────────┼───────────────┐
        │               │               │
 Trading Engine   Backtesting Engine   AI Insights
        │               │               │
        └───────────────┬───────────────┘
                        │
                     Postgres
                        │
                   API Gateway
                        │
                    WebSockets
                        │
                     React UI
```

---

# Tech Stack

## Frontend
- React
- Tailwind CSS
- lightweight-charts (TradingView)

## Backend
- Go (primary services)
- REST + WebSocket APIs

## Data Layer
- Postgres (relational data)
- Redis (caching + pub/sub)

## Backtesting & Analytics
- Historical market datasets
- Strategy computation engine

## AI Layer
- LLM APIs or local models
- Structured analytics prompts

## DevOps / Infrastructure
- Docker (service containerization)
- Docker Compose (local orchestration)
- Cloud deployment (AWS/GCP/Azure) — not yet configured

Optional, considered but not adopted (see `docs/deferred-tuning.md` for what would trigger each):
- Kubernetes
- Terraform
- CI/CD pipelines

---

# Repository Layout

```
/services
  /auth            — built
  /gateway         — built
  /market-data     — built
  /trading-engine  — built
  /backtesting     — built
  /ai-insights     — built
/pkg
/frontend
/infra
  /docker
  /migrations
/docs              — design notes, session handoffs, archived specs
/tasks             — active spec checkpoints (plan.md, todo.md)
```

---

# Development Roadmap

Originally scoped at ~5–10 hrs/week over ~3–4 months; now several months in (with a pause for school in between) and mid-Phase 3, so treat this as direction rather than a schedule.

## Phase 1 — Trading Foundations — done
- User authentication
- Account balances
- Portfolio schema
- Market data ingestion

## Phase 2 — Trading Engine — done
- Auth hardening (rate limiting, refresh-token revocation, gateway body cap) — done
- Order execution — done
- Trade history — done
- Profit/loss tracking — done
- Live portfolio UI — done

## Phase 3 — Backtesting Engine — done
- Historical data ingestion — done (Phase 1's market-data ingestion covers it)
- Strategy simulator (MA crossover, RSI, MACD) — done
- Performance dashboards — done
- Multi-symbol backtests — done (one shared pool of capital per run)

## Phase 4 — AI Insights + Deployment — not started
- Portfolio analytics
- AI trade insights
- Dockerization
- Cloud deployment

---

# Deployment Architecture

## Local
See [Local Development](#local-development) above.

## Cloud
- Container hosting (ECS / EC2 / GKE)
- Managed Postgres (RDS / Cloud SQL)
- Object storage (S3)

Not yet implemented — no cloud deployment exists today.

---

# Resume Value Demonstrated

This project showcases:

- Distributed backend systems
- Real‑time WebSocket streaming
- Financial trading infrastructure
- Strategy backtesting engines
- AI analytics integration
- Cloud deployment & containerization

---

# Future Enhancements

- Options & derivatives simulation
- Order book depth visualization
- Social trading leagues
- Copy‑trading systems
- Mobile app client

---

# Security

Do not commit `.env`; use environment variables or a secrets manager in production. Use HTTPS in production.

Known gaps and accepted risks are tracked in `docs/security-backlog.md`.

---

# License

MIT — see `LICENSE`.

---

# Author

**Khalil Peguero**

Software Engineer | Full‑Stack Developer | Fintech Enthusiast

---

# Summary

QuantSim is designed to function as a production‑grade fintech simulation platform that bridges real‑time trading systems, quantitative research tooling, and AI‑driven analytics into one cohesive distributed architecture. Phase 1 (auth, market data ingestion), Phase 2 (trading engine), and Phase 3 (backtesting engine) are done — the latter through MA-crossover/RSI/MACD strategies, their frontend, and multi-symbol portfolio runs. Phase 4 (AI insights, deployment) is underway: the rule-based portfolio analytics service is built, with insight generation, Dockerization, and cloud deployment still ahead.
