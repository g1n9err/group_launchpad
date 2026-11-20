COMPOSE := docker compose
GO     := go
FNM    := $(HOME)/.local/share/fnm/aliases/default/bin

# Load .env (if present) into every recipe below, so `make api` etc. work
# without manual exports. Precedence note: values here override shell exports
# inside make recipes — unset stray exports if behavior surprises you.
ifneq (,$(wildcard .env))
include .env
export
endif

.DEFAULT_GOAL := help

.PHONY: help
help: ## show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

## ── local environment ──────────────────────────────────────────────────────

.PHONY: dev-up
dev-up: ## start local Postgres (docker compose)
	$(COMPOSE) up -d postgres

.PHONY: dev-down
dev-down: ## stop local Postgres
	$(COMPOSE) stop postgres

.PHONY: full-up
full-up: ## prod-parity: Postgres + api + worker containers (conflicts with host `make api` on :8080)
	@ss -tln | grep -q :8080 && { echo "ERROR: port 8080 busy — stop host 'make api' (or anything on :8080) first"; exit 1; } || true
	$(COMPOSE) --profile full up --build -d

.PHONY: full-down
full-down: ## stop the full stack and remove containers
	$(COMPOSE) --profile full down

## ── run (hot paths for development) ───────────────────────────────────────

.PHONY: api
api: ## run the API on :8080 (needs dev-up first)
	cd backend && $(GO) run ./cmd/api

.PHONY: worker
worker: ## run the River background worker (launches, audits, announcements, and delivery)
	cd backend && $(GO) run ./cmd/worker

.PHONY: fe
fe: ## run the Vite dev server on :5173 (proxies /api → :8080)
	cd frontend && npm run dev

## ── database ───────────────────────────────────────────────────────────────

.PHONY: migrate-up
migrate-up: ## apply all pending migrations
	cd backend && $(GO) run ./cmd/migrate up

.PHONY: migrate-down
migrate-down: ## roll back the last migration
	cd backend && $(GO) run ./cmd/migrate down 1

.PHONY: migrate-version
migrate-version: ## print current schema version
	cd backend && $(GO) run ./cmd/migrate version

.PHONY: psql
psql: ## psql shell into the local Postgres container
	$(COMPOSE) exec postgres psql -U launchpad -d launchpad

## ── codegen & tests ────────────────────────────────────────────────────────

.PHONY: gen
gen: ## regenerate API types on BOTH sides from openapi.yaml
	cd backend && $(GO) generate ./internal/transport/
	cd backend && $(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@latest generate
	cd frontend && npm run gen

.PHONY: test
test: ## all backend tests (race) + frontend production build
	cd backend && $(GO) test -race ./...
	cd frontend && npm run build

.PHONY: test-backend
test-backend: ## backend tests only (needs dev-up)
	cd backend && $(GO) test -race ./...

.PHONY: e2e
e2e: ## browser E2E: disposable Postgres + API + built SPA (no external services)
	@bash scripts/e2e.sh
