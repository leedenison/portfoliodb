.PHONY: tools generate build server-test db-test client-test integration-test integration-test-record run run-server init-db init-test-db stop clean clean-generated clean-docker clean-next test

# Load .env so DB_INITIALISE_SCRIPT etc. are available to run/init-db
-include .env
export

# Compose file and env for local stack (run from repo root so .env is found)
COMPOSE_RUN = docker compose -f docker/server/docker-compose.yml --env-file .env
# Dev stack: same as above plus override with source mounts and live-reload (Air + next dev)
COMPOSE_DEV = docker compose -f docker/server/docker-compose.yml -f docker/server/docker-compose.dev.yml --env-file .env

# Install Go and npm tooling required for generate and tests. Run once (or after adding deps).
tools:
	@command -v go >/dev/null 2>&1 || { echo "go is required; install from https://go.dev/dl"; exit 1; }
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE_DEV) run --rm client npm ci

generate:
	buf generate --template buf.gen.go.yaml && buf generate --template buf.gen.ts.yaml
	go generate ./server/db

clean:
	rm -f portfoliodb portfoliodb.exe

clean-generated:
	find proto -name '*.pb.go' -delete 2>/dev/null || true
	find server -name '*_mock.go' -delete 2>/dev/null || true

clean-docker:
	$(COMPOSE_DEV) down --rmi local --volumes
	docker compose -f docker/server/docker-compose.test.yml down --rmi local --volumes

# Remove client node_modules and .next (e.g. after switching Node versions). Re-run 'make tools' to reinstall.
clean-next:
	rm -rf client/node_modules client/.next

build: generate
	go build -o portfoliodb ./server/cmd/portfoliodb

# Full stack (Postgres 5432, Redis 6379, portfoliodb, Envoy, client SPA) for local dev. SPA at localhost:8080.
# Uses dev override: source mounts, host UID/GID, Air + next dev for live-reload.
run:
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

server-test: generate
	go test ./server/...

client-test:
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE_DEV) run --rm client npm run test:run
	
db-test:
	docker compose -f docker/server/docker-compose.test.yml up -d
	@echo "Waiting for Postgres..."
	@scripts/postgres-ready.sh "docker compose -f docker/server/docker-compose.test.yml"
	TEST_DATABASE_URL="postgres://portfoliodb:portfoliodb@localhost:5433/portfoliodb_test?sslmode=disable" go test -v ./server/db/postgres/...
	@docker compose -f docker/server/docker-compose.test.yml down

integration-test:
	go test -tags integration -v ./server/plugins/...

integration-test-record:
	VCR_MODE=record go test -tags integration -v -count=1 ./server/plugins/...

test: server-test client-test db-test integration-test