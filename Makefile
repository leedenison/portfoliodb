.DEFAULT_GOAL := help

# Load .env so DB_INITIALISE_SCRIPT etc. are available to run/init-db
-include .env
export

# === Stack Isolation ===
# Each stack uses an explicit -p project name and non-overlapping host ports.
# All stacks can run simultaneously without collision.
#   Dev  (portfoliodb-dev):  postgres 5432, redis 6379, grpc 50051, envoy 8080
#   Test (portfoliodb-test): postgres 5433, redis 6380
#   E2E  (portfoliodb-e2e):  postgres 5434, redis 6381, grpc 50052, envoy 8081

COMPOSE_RUN  = docker compose -p portfoliodb      -f docker/docker-compose.yml --env-file .env
COMPOSE_DEV  = docker compose -p portfoliodb-dev   -f docker/docker-compose.yml -f docker/docker-compose.dev.yml --env-file .env
COMPOSE_E2E  = docker compose -p portfoliodb-e2e   -f docker/docker-compose.yml -f docker/docker-compose.e2e.yml --env-file .env
COMPOSE_TEST = docker compose -p portfoliodb-test  -f docker/docker-compose.test.yml

# Git revision for Docker build args (works in both regular repos and worktrees).
BUILD_REV ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
export BUILD_REV

# --- Stamp infrastructure ---
# Stamp files track when expensive operations (tools, generate) last ran.
# Make compares stamp mtimes against source file mtimes -- if any source is
# newer, the stamp is stale and the recipe re-runs. Downstream targets depend
# on stamps, so staleness propagates automatically.
STAMP_DIR := .stamps

$(STAMP_DIR):
	@mkdir -p $(STAMP_DIR)

# --- tools stamp ---
# Re-run when Go module deps or JS package manifests change.
TOOLS_DEPS := go.mod go.sum client/package.json client/package-lock.json e2e/package.json e2e/package-lock.json

$(STAMP_DIR)/tools: $(TOOLS_DEPS) | $(STAMP_DIR)
	@command -v go >/dev/null 2>&1 || { echo "go is required; install from https://go.dev/dl"; exit 1; }
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE_DEV) run --rm client npm ci
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE_E2E) --profile test run --rm playwright npm ci
	@touch $@

# --- generate stamp ---
# Re-run when proto files, buf configs, or go:generate source changes.
PROTO_FILES := $(shell find proto -name '*.proto' 2>/dev/null)
GENERATE_DEPS := $(PROTO_FILES) buf.gen.go.yaml buf.gen.ts.yaml buf.gen.e2e.yaml server/db/db.go

$(STAMP_DIR)/generate: $(GENERATE_DEPS) | $(STAMP_DIR)
	buf generate --template buf.gen.go.yaml && buf generate --template buf.gen.ts.yaml && buf generate --template buf.gen.e2e.yaml --path proto/e2e --path proto/api
	go generate ./server/db
	@touch $@

# PHONY aliases so 'make tools' and 'make generate' still work directly.
tools: $(STAMP_DIR)/tools
generate: $(STAMP_DIR)/generate

build: $(STAMP_DIR)/generate
	go build -o portfoliodb ./server/cmd/portfoliodb

# Full stack (Postgres 5432, Redis 6379, portfoliodb, Envoy, client SPA) for local dev. SPA at localhost:8080.
# Uses dev override: source mounts, host UID/GID, Air + next dev for live-reload.
run: $(STAMP_DIR)/tools $(STAMP_DIR)/generate
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE_DEV) up -d --build
	@echo "Waiting for Postgres..."
	@scripts/postgres-ready.sh "$(COMPOSE_DEV)"
	@echo "Waiting for portfoliodb (gRPC)..."
	@scripts/server-ready.sh
	@$(MAKE) init-db

# Run the DB initialise script when DB_INITIALISE_SCRIPT is set and the file exists. Used by 'make run'.
init-db:
	@scripts/init-db.sh "$(COMPOSE_DEV)" "$(DB_INITIALISE_SCRIPT)"

# Stop containers started by 'make run'.
stop:
	$(COMPOSE_DEV) down

server-test: $(STAMP_DIR)/generate
	go test ./server/...

client-test: $(STAMP_DIR)/tools
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE_DEV) run --rm client npm run test:run

db-test: $(STAMP_DIR)/generate
	$(COMPOSE_TEST) up -d
	@echo "Waiting for Postgres..."
	@scripts/postgres-ready.sh "$(COMPOSE_TEST)"
	TEST_DATABASE_URL="postgres://portfoliodb:portfoliodb@localhost:5433/portfoliodb_test?sslmode=disable" go test -v ./server/db/postgres/...
	@$(COMPOSE_TEST) down

integration-test: $(STAMP_DIR)/generate
	go test -tags integration -v ./server/plugins/...

integration-test-record: $(STAMP_DIR)/generate
	VCR_MODE=record go test -tags integration -v -count=1 ./server/plugins/...

# E2E tests: replay mode (VCR cassettes, dummy API keys, no rate limits).
# Full stack at isolated ports: Postgres 5434, Redis 6381, Envoy 8081.
# Each spec file seeds its own data via the db.ts helper.
# Tears down any existing E2E stack first to avoid stale containers/env vars.
e2e-test: $(STAMP_DIR)/generate
	@$(COMPOSE_E2E) --profile test down --remove-orphans 2>/dev/null; \
		$(COMPOSE_E2E) up -d --build --force-recreate; \
		echo "Waiting for Postgres..."; \
		scripts/postgres-ready.sh "$(COMPOSE_E2E)"; \
		echo "Waiting for portfoliodb (gRPC)..."; \
		scripts/server-ready.sh localhost:50052; \
		HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE_E2E) --profile test run --rm playwright npx playwright test; \
		rc=$$?; $(COMPOSE_E2E) --profile test down; exit $$rc

# E2E tests: record mode (real API calls, real keys from env, real rate limits).
# Requires: OPENFIGI_API_KEY, MASSIVE_API_KEY, EODHD_API_KEY, OPENAI_API_KEY
# VCR_MODE is passed to both the server (for go-vcr) and Playwright (for seed logic).
# Tears down any existing E2E stack first to avoid stale containers/env vars.
e2e-record: $(STAMP_DIR)/generate
	@$(COMPOSE_E2E) --profile test down --remove-orphans 2>/dev/null; \
		VCR_MODE=record $(COMPOSE_E2E) up -d --build --force-recreate; \
		echo "Waiting for Postgres..."; \
		scripts/postgres-ready.sh "$(COMPOSE_E2E)"; \
		echo "Waiting for portfoliodb (gRPC)..."; \
		scripts/server-ready.sh localhost:50052; \
		logdir="/tmp/e2e-record-$$(date +%Y%m%d-%H%M%S)"; mkdir -p "$$logdir"; \
		HOST_UID=$$(id -u) HOST_GID=$$(id -g) VCR_MODE=record $(COMPOSE_E2E) --profile test run --rm playwright \
			sh -c 'npx playwright test 2>&1; echo $$? > /e2e/.e2e-rc' | tee "$$logdir/playwright.log"; \
		rc=$$(cat e2e/.e2e-rc 2>/dev/null || echo 1); rm -f e2e/.e2e-rc; \
		VCR_MODE=record $(COMPOSE_E2E) logs --no-log-prefix portfoliodb > "$$logdir/server.log" 2>&1; \
		echo "Logs saved to $$logdir/"; \
		$(COMPOSE_E2E) --profile test down; exit $$rc

test: server-test client-test db-test integration-test

clean: clean-stamps
	rm -f portfoliodb portfoliodb.exe

clean-generated:
	find proto -name '*.pb.go' -delete 2>/dev/null || true
	find server -name '*_mock.go' -delete 2>/dev/null || true
	rm -f $(STAMP_DIR)/generate

clean-docker:
	$(COMPOSE_DEV) down --rmi local --volumes
	$(COMPOSE_TEST) down --rmi local --volumes
	$(COMPOSE_E2E) --profile test down --rmi local --volumes --remove-orphans

# Remove client node_modules and .next (e.g. after switching Node versions). Re-run 'make tools' to reinstall.
clean-next:
	rm -rf client/node_modules client/.next
	rm -f $(STAMP_DIR)/tools

clean-stamps:
	rm -rf $(STAMP_DIR)

help:
	@echo "portfoliodb Makefile"
	@echo ""
	@echo "Setup:"
	@echo "  make tools              Install Go tools and npm deps (auto-skipped if up-to-date)"
	@echo "  make generate           Run protobuf + mock codegen (auto-skipped if up-to-date)"
	@echo ""
	@echo "Development:"
	@echo "  make run                Start dev stack (Postgres, Redis, gRPC, Envoy, Next.js)"
	@echo "  make stop               Stop dev stack"
	@echo "  make build              Build server binary"
	@echo ""
	@echo "Testing:"
	@echo "  make test               Run all tests (server, client, db, integration)"
	@echo "  make server-test        Go unit tests"
	@echo "  make client-test        Next.js tests (in container)"
	@echo "  make db-test            Postgres integration tests (isolated container)"
	@echo "  make integration-test   Plugin integration tests (VCR replay)"
	@echo "  make e2e-test           Full E2E with Playwright (VCR replay)"
	@echo "  make e2e-record         Full E2E with Playwright (VCR record, needs API keys)"
	@echo ""
	@echo "Cleanup:"
	@echo "  make clean              Remove binary and stamps"
	@echo "  make clean-generated    Remove generated protobuf/mock files"
	@echo "  make clean-docker       Remove all Docker containers, images, and volumes"
	@echo "  make clean-next         Remove client node_modules and .next"
	@echo ""
	@echo "After 'git pull', run 'make tools generate' if deps or protos changed."
	@echo "Dependencies are tracked automatically -- stale steps re-run as needed."

.PHONY: tools generate build server-test db-test client-test integration-test integration-test-record e2e-test e2e-record run init-db stop clean clean-generated clean-docker clean-next clean-stamps test help
