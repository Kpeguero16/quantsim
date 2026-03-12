-include .env
export

.PHONY: help docker-up docker-down docker-ps migrate-up migrate-down migrate-force run-auth run-market-data run-gateway

help:
	@echo "QuantSim Phase 1 targets:"
	@echo "  make docker-up       - Start Postgres and Redis (docker compose up -d)"
	@echo "  make docker-down     - Stop Postgres and Redis"
	@echo "  make docker-ps       - List running containers"
	@echo "  make migrate-up      - Apply database migrations"
	@echo "  make migrate-down    - Roll back one migration"
	@echo "  make migrate-force VERSION=N - Clear dirty state (e.g. VERSION=1 then make migrate-up)"
	@echo "  make run-auth        - Run auth service"
	@echo "  make run-market-data - Run market-data service"
	@echo "  make run-gateway     - Run API gateway"

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
	cd services/auth && go run .

run-market-data:
	cd services/market-data && go run .

run-gateway:
	cd services/gateway && go run .
