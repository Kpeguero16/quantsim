# agents.md — QuantSim Master Context File

This file serves as the **persistent system context** for any LLM assisting in the design, planning, architecture, or implementation of the QuantSim platform.

Any AI agent referencing this document should treat it as the single source of truth for project scope, architecture decisions, constraints, and engineering goals.

---

# Working Agreement (Architect / Implementer)

As of 2026-07-29, QuantSim resumed development under a different workflow than Phase 1 started with. See `docs/intent/quantsim-resume.md` for the full rationale.

- **Khalil is the architect.** He sets direction, writes/edits specs and PRDs, makes architecture decisions, and reviews at checkpoints.
- **Claude (via Claude Code) is the implementer.** For any non-trivial unit of work, write a PRD/spec first and get it reviewed before building. Then implement freely and stop at checkpoints.
- **Checkpoints are scoped to one logical piece of implementation at a time** (e.g. "AccountStore done," "JWT helpers done") — not per-service or per-phase. Keep diffs small enough to review meaningfully.
- **Claude Code only.** Cursor is no longer used; do not reference `.cursor/` or Cursor-specific tooling in new guidance.
- **Time budget is 3-5 hrs/week** (revised down from the original 5-10 hrs/week estimate below — school takes priority).

---

# Project Identity

**Project Name:** QuantSim  
**Type:** Real-Time Trading & Strategy Backtesting Platform  
**Domain:** Fintech / Quantitative Analysis / Distributed Systems  
**Trading Type:** Simulated (Paper Trading Only)  
**Market Data:** Real (Live + Historical)

---

# Project Purpose

QuantSim is a flagship, resume‑level distributed system designed to demonstrate production‑grade software engineering skills.

The system simulates real-world trading environments using live market data while enabling users to test strategies, analyze portfolio performance, and generate AI-driven insights.

This project prioritizes:

- Systems design depth
- Backend engineering rigor
- Financial data modeling
- Real-time infrastructure
- Cloud & container deployment
- AI integration

Over simple CRUD functionality.

---

# Core Feature Scope (Locked)

## 1. Real Market Data Ingestion

### Responsibilities
- Ingest live stock price data
- Normalize external API feeds
- Stream prices internally
- Store historical datasets

### Data Types
- OHLC candles
- Tick price updates
- Volume
- Multi-year historical price history

### Data Flow

Market API → Data Ingestion Service → Redis → WebSockets → Frontend

---

## 2. Simulated Trading Engine

### Responsibilities
- Execute paper trades
- Validate account balances
- Track positions
- Maintain trade history
- Calculate profit/loss

### Order Execution Flow

1. User submits order
2. Validate balance
3. Fetch live price
4. Execute simulated fill
5. Update portfolio
6. Store transaction

### Supported Orders

**MVP**
- Market buy/sell

**Advanced**
- Limit orders
- Stop-loss
- Take-profit

---

## 3. Strategy Backtesting Engine (Major System)

### Purpose
Evaluate trading strategies against historical market data.

### Example Strategies
- Moving average crossover
- RSI thresholds
- MACD signals

### Processing Flow

Historical Data → Strategy Engine → Trade Simulator → Metrics Engine

### Performance Metrics
- Total return %
- Sharpe ratio
- Max drawdown
- Win rate
- Profit factor

### Stretch Features
- Custom strategy configuration
- Script-based strategies (sandboxed execution)

---

## 4. AI Trade Insights Engine (Major System)

### Purpose
Generate intelligent analytics and narrative insights about user trading behavior and portfolio performance.

### Insight Categories

**Portfolio Risk**
- Sector exposure
- Diversification score
- Volatility analysis

**Performance Benchmarking**
- vs S&P 500
- vs NASDAQ
- Risk-adjusted return

**Behavioral Insights**
- Overtrading detection
- Panic selling
- Risk profile analysis

**Strategy Evaluation**
- Outperformance vs buy-and-hold
- Drawdown comparison

### Implementation Phases

Phase 1:
- Rule-based analytics
- SQL/statistical computations

Phase 2:
- LLM-generated insights
- Structured prompts + narrative outputs

---

# System Architecture

## High-Level Services

- Market Data Service
- Trading Engine
- Backtesting Engine
- AI Insights Service
- Authentication Service
- API Gateway

---

## Architecture Diagram

```
                Market Data APIs
                        │
                Data Ingestion Service (Go)
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
- Charting libraries

## Backend
- Go (primary language)
- REST APIs
- WebSockets

## Data Layer
- Postgres (relational data)
- Redis (cache + pub/sub)

## AI Layer
- LLM APIs
- Analytics prompt pipelines

## Infrastructure
- Docker
- Docker Compose
- Cloud deployment: **AWS free tier** (Phase 4 target — EC2 + docker-compose for the service stack; RDS optional in place of self-hosted Postgres; Redis stays self-hosted in a container since ElastiCache has no free tier)

### Optional Enhancements
- Terraform (IaC, stretch goal alongside Phase 4 deployment)
- Kubernetes
- CI/CD pipelines

---

# Data Storage Model

## Core Entities

- Users
- Accounts
- Positions
- Orders
- Trades
- Historical Prices
- Strategies
- Backtest Results

---

# Market Data Pipeline

## Responsibilities
- Fetch live prices
- Store historical candles
- Broadcast price updates

## Ingestion Methods

MVP:
- REST polling

Advanced:
- WebSocket feeds
- Stream processing

---

# Backtesting Computation Model

## Steps

1. Load historical dataset
2. Apply strategy rules
3. Simulate trades
4. Track equity curve
5. Compute metrics

---

# AI Insights Data Flow

```
Portfolio + Trade Data → Analytics Engine → Insight Generator → Dashboard
```

---

# Repository Structure

```
/services
  /auth
  /gateway
  /market-data
  /trading-engine
  /backtesting
  /ai-insights
/pkg
/frontend
/infra
  /docker
  /migrations
/docs
```

---

# Development Roadmap

## Phase 1 — Foundations
- Auth system
- Accounts (starting balance: $100k simulated per account)
- Portfolio schema
- Market data ingestion

## Phase 2 — Trading Engine
- Order execution
- Trade storage
- P/L tracking
- **Security items scheduled for this phase** — see **docs/security-backlog.md**
  (items 1, 2, and 4). Phase 2 is where they belong because Phase 2 is what
  makes them matter: today account takeover buys a read-only view of public
  market data, but once `/trading/*` executes orders against a $100k balance
  the same weakness lets someone trade as another user. The auth surface does
  not get weaker in Phase 2 — the consequences of its existing gaps get worse.
  - Rate limiting on the auth routes (nothing throttles credential stuffing today)
  - Refresh-token revocation and a real server-side logout (tokens live 7 days
    with no kill switch; "sign out" is client-side only)
  - A gateway-wide request body cap, naturally done when `/trading/*` stops
    returning `501`

## Phase 3 — Backtesting Engine
- Historical ingestion
- Strategy simulator
- Metrics dashboards

## Phase 4 — AI Insights + Infra
- Portfolio analytics
- Insight generation
- Dockerization
- Cloud deployment
- Work through **docs/deferred-tuning.md** — timeouts, connection pooling, and
  other defaults deliberately left unset because the right values depend on
  traffic shape that only exists once deployed

---

# Engineering Constraints

Time availability:
- 3–5 hours/week (revised 2026-07-29; original estimate was 5–10 hours/week)

Project timeline:
- ~3–4 months (original estimate; paused 2026-04 to 2026-07 for school — treat as directional, not a hard deadline)

Priority order:
1. Resume impact
2. Systems depth
3. Backend complexity
4. Infra exposure
5. UI polish

---

# Resume Signaling Goals

This project must demonstrate:

- Distributed systems design
- Real-time streaming architecture
- Financial trading infrastructure
- Strategy backtesting computation
- AI analytics integration
- Cloud deployment
- Container orchestration

---

# Guidance for AI Agents Referencing This File

When assisting on this project:

- **Write a PRD/spec before implementing any non-trivial unit of work.** Get it reviewed by Khalil (architect) before building. This is a workflow requirement, not a suggestion — see Working Agreement above.
- **Implement in checkpoint-sized slices** — one logical piece at a time (e.g. one store, one service method group, one handler set), then stop for review rather than building an entire service end-to-end unprompted.
- Default to microservices architecture
- Prioritize Go for backend services
- Use Postgres for relational data
- Use Redis for caching/pub-sub
- Design for real-time streaming
- Emphasize scalability considerations
- Avoid suggesting CRUD-only designs
- Provide production-grade patterns
- Use a consistent JSON error response shape for APIs (e.g. code + message)
- Prefer idempotent ingestion (e.g. upsert by symbol/timeframe/timestamp) for market data
- Phase 1: manual/curl verification is sufficient; automated tests are optional or Phase 2+
- When adding or restructuring code, keep it test-ready: separate entrypoints (cmd) from business logic and stores; depend on interfaces for DB/external clients so unit tests can inject mocks. See **docs/TESTING_STRUCTURE.md** for layout and conventions.

Always optimize for:
- Resume differentiation
- Backend/system design depth
- Fintech realism

---

# Future Expansion Possibilities

- Options trading simulation
- Order book depth visualization
- Social trading leagues
- Copy-trading
- Mobile clients

---

# Author Context

Computer science student building a flagship fintech system as a portfolio piece, alongside coursework — roughly 3-5 hrs/week of available time.

Role on this project: **architect**, not implementer. Directs scope, reviews specs and checkpoints, makes architecture calls. Prefers to spend limited hours on system design and decision-making rather than hand-writing implementation — see `docs/intent/quantsim-resume.md` for how this was decided.

Still cares about:
- Finance-related systems
- Distributed architecture work
- Go as the backend language (implemented by Claude, reviewed by Khalil)

---

# End of Context File

This document should be referenced in all architectural, backend, infrastructure, and analytics planning discussions related to QuantSim.

