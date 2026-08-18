-include .env
export

.PHONY: help docker-up docker-down docker-ps migrate-up migrate-down migrate-force run-auth run-market-data run-gateway run-trading-engine run-backtesting run-frontend test test-integration test-all test-db-drop vet

# GO_MODULES is every module in the workspace. Kept in one place so a new
# service is added to test and vet by editing a single line.
GO_MODULES := pkg services/auth services/gateway services/market-data services/trading-engine services/backtesting

help:
	@echo "QuantSim targets:"
	@echo "  make docker-up       - Start Postgres and Redis (docker compose up -d)"
	@echo "  make docker-down     - Stop Postgres and Redis"
	@echo "  make docker-ps       - List running containers"
	@echo "  make migrate-up      - Apply database migrations"
	@echo "  make migrate-down    - Roll back one migration"
	@echo "  make migrate-force VERSION=N - Clear dirty state (e.g. VERSION=1 then make migrate-up)"
	@echo "  make run-auth        - Run auth service"
	@echo "  make run-market-data - Run market-data service"
	@echo "  make run-gateway     - Run API gateway"
	@echo "  make run-trading-engine - Run trading engine"
	@echo "  make run-backtesting - Run backtesting engine"
	@echo "  make run-frontend    - Run the Vite dev server (localhost:5173)"
	@echo ""
	@echo "  make test            - Unit tests, all modules. No Docker needed"
	@echo "  make test-integration- Store tests against a real Postgres (needs make docker-up)"
	@echo "  make test-all        - Both of the above"
	@echo "  make test-db-drop    - Drop the quantsim_test database"
	@echo "  make vet             - go vet every module, including tagged tests"

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-ps:
	docker compose ps

migrate-up:
	migrate -path infra/migrations -database "$$DATABASE_URL" up

migrate-down:
	migrate -path infra/migrations -database "$$DATABASE_URL" down 1

migrate-force:
	migrate -path infra/migrations -database "$$DATABASE_URL" force $(VERSION)

run-auth:
	cd services/auth && go run ./cmd/server

run-market-data:
	cd services/market-data && go run ./cmd/server

run-gateway:
	cd services/gateway && go run ./cmd/server

run-trading-engine:
	cd services/trading-engine && go run ./cmd/server

run-backtesting:
	cd services/backtesting && go run ./cmd/server

run-frontend:
	cd frontend && npm run dev

# Unit tests across every module. Needs no Docker and no network: the auth and
# gateway suites run against in-memory fakes.
#
# This does NOT cover services/auth/internal/store -- those tests live behind
# the integration tag below, and a green run here says nothing about any SQL.
test:
	@for m in $(GO_MODULES); do \
		echo "==> $$m"; \
		(cd $$m && go test ./...) || exit 1; \
	done

# Store-layer tests against a real Postgres in the quantsim_test database,
# which the harness drops and recreates on every run.
#
# Skips (exit 0) when Postgres is unreachable, so this is safe to run with
# Docker stopped. It does NOT skip on a broken harness or a failed migration --
# those exit non-zero, because a suite that cannot tell "nothing to test
# against" from "the harness is broken" protects nothing.
#
# -count=1 because results depend on database state and must never be cached.
#
# -v is not for detail, it is load-bearing. Without it `go test` prints only
# `ok` and suppresses the output of skipped tests, so a run where every test
# skipped is visually identical to one where all of them passed -- the precise
# failure this suite is built to avoid, reintroduced by the command that runs
# it. With -v, "SKIP" is on screen and impossible to miss.
test-integration:
	cd services/auth && go test -tags=integration -count=1 -v ./integration/...
	cd services/trading-engine && go test -tags=integration -count=1 -v ./integration/...
	cd services/backtesting && go test -tags=integration -count=1 -v ./integration/...

test-all: test test-integration

# Drop the test database. Rarely needed now that the harness recreates it each
# run, but useful for reclaiming the space or after an interrupted run leaves a
# connection behind.
#
# Targets quantsim_test by name and nothing else. The dev data lives in the
# database called `postgres`; `quantsim` exists but is empty.
test-db-drop:
	docker compose exec -T postgres psql -U "$$POSTGRES_USER" -d postgres \
		-c 'DROP DATABASE IF EXISTS quantsim_test WITH (FORCE)'

# vet every module, and vet the integration package under its build tag.
#
# The tagged pass is the point: files behind a build tag are never
# type-checked by any default command, which is exactly how a tagged test file
# rots unnoticed until someone finally runs it.
vet:
	@for m in $(GO_MODULES); do \
		echo "==> $$m"; \
		(cd $$m && go vet ./...) || exit 1; \
	done
	cd services/auth && go vet -tags=integration ./integration/...
	cd services/trading-engine && go vet -tags=integration ./integration/...
	cd services/backtesting && go vet -tags=integration ./integration/...
