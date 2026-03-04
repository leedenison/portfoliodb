.PHONY: tools generate build server-test db-test client-test run run-server init-db init-test-db clean clean-generated clean-docker clean-volumes test

# Compose file and env for local stack (run from repo root so .env is found)
COMPOSE_RUN = docker compose -f docker/server/docker-compose.yml --env-file .env
# Dev stack: same as above plus override with source mounts and live-reload (Air + next dev)
COMPOSE_DEV = docker compose -f docker/server/docker-compose.yml -f docker/server/docker-compose.dev.yml --env-file .env

# Install Go and npm tooling required for generate and tests. Run once (or after adding deps).
tools:
	@command -v go >/dev/null 2>&1 || { echo "go is required; install from https://go.dev/dl"; exit 1; }
	@command -v npm >/dev/null 2>&1 || { echo "npm is required; install Node.js from https://nodejs.org"; exit 1; }
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	cd client && npm ci

generate:
	buf generate
	go generate ./server/db

clean:
	rm -f portfoliodb portfoliodb.exe

clean-generated:
	find proto -name '*.pb.go' -delete 2>/dev/null || true
	find server -name '*_mock.go' -delete 2>/dev/null || true

clean-docker:
	$(COMPOSE_RUN) down --rmi local --volumes
	docker compose -f docker/server/docker-compose.test.yml down --rmi local --volumes

# Remove dev stack named volumes (client_node_modules, client_next). Stops client first so volumes are not in use.
clean-volumes:
	$(COMPOSE_DEV) stop client
	docker volume rm portfoliodb_client_node_modules portfoliodb_client_next

build: generate
	go build -o portfoliodb ./server/cmd/portfoliodb

# Full stack (Postgres 5432, Redis 6379, portfoliodb, Envoy, client SPA) for local dev. SPA at localhost:8080.
# Uses dev override: source mounts, host UID/GID, Air + next dev for live-reload.
run:
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE_DEV) up -d --build
	@echo "Waiting for Postgres..."
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		if $(COMPOSE_DEV) exec -T postgres pg_isready -U portfoliodb 2>/dev/null; then break; fi; \
		sleep 1; \
		if [ $$i -eq 10 ]; then echo "Postgres not ready"; exit 1; fi; \
	done
	cat server/migrations/001_initial.sql | $(COMPOSE_DEV) exec -T postgres psql -U portfoliodb -d portfoliodb -q

server-test:
	go test ./server/...

client-test:
	cd client && npm run test:run
	
db-test:
	docker compose -f docker/server/docker-compose.test.yml up -d
	@echo "Waiting for Postgres..."
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		if docker compose -f docker/server/docker-compose.test.yml exec -T postgres pg_isready -U portfoliodb 2>/dev/null; then break; fi; \
		sleep 1; \
		if [ $$i -eq 10 ]; then echo "Postgres not ready"; exit 1; fi; \
	done
	cat server/migrations/001_initial.sql | docker compose -f docker/server/docker-compose.test.yml exec -T postgres psql -U portfoliodb -d portfoliodb_test -q
	TEST_DATABASE_URL="postgres://portfoliodb:portfoliodb@localhost:5433/portfoliodb_test?sslmode=disable" go test -v ./server/db/postgres/...
	@docker compose -f docker/server/docker-compose.test.yml down

test: server-test client-test db-test