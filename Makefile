.PHONY: generate build server-test db-test client-test run run-server init-db init-test-db clean docker-clean

# Compose file and env for local stack (run from repo root so .env is found)
COMPOSE_RUN = docker compose -f docker/server/docker-compose.yml --env-file .env
# Dev stack: same as above plus override with source mounts and live-reload (Air + next dev)
COMPOSE_DEV = docker compose -f docker/server/docker-compose.yml -f docker/server/docker-compose.dev.yml --env-file .env

generate:
	buf generate
	go generate ./server/db

clean:
	rm -f portfoliodb portfoliodb.exe
	find proto -name '*.pb.go' -delete 2>/dev/null || true
	find server -name '*_mock.go' -delete 2>/dev/null || true

docker-clean:
	$(COMPOSE_RUN) down --rmi local --volumes
	docker compose -f docker/server/docker-compose.test.yml down --rmi local --volumes

build: generate
	go build -o portfoliodb ./server/cmd/portfoliodb

server-test: generate
	go test ./server/...

client-test: generate
	cd client && npm run test:run

# Full stack (Postgres 5432, Redis 6379, portfoliodb, Envoy, client SPA) for local dev. SPA at localhost:8080.
# Uses dev override: source mounts, host UID/GID, Air + next dev for live-reload.
# Does not depend on generate so BSR rate limits do not block starting the stack; Air runs buf generate in-container.
run:
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE_DEV) up -d --build
	@echo "Waiting for Postgres..."
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		if $(COMPOSE_DEV) exec -T postgres pg_isready -U portfoliodb 2>/dev/null; then break; fi; \
		sleep 1; \
		if [ $$i -eq 10 ]; then echo "Postgres not ready"; exit 1; fi; \
	done
	cat server/migrations/001_initial.sql | $(COMPOSE_DEV) exec -T postgres psql -U portfoliodb -d portfoliodb -q
	cat server/plugins/local/identifier/migrations/001_instrument_ref.sql | $(COMPOSE_DEV) exec -T postgres psql -U portfoliodb -d portfoliodb -q

db-test: generate
	docker compose -f docker/server/docker-compose.test.yml up -d
	@echo "Waiting for Postgres..."
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		if docker compose -f docker/server/docker-compose.test.yml exec -T postgres pg_isready -U portfoliodb 2>/dev/null; then break; fi; \
		sleep 1; \
		if [ $$i -eq 10 ]; then echo "Postgres not ready"; exit 1; fi; \
	done
	cat server/migrations/001_initial.sql | docker compose -f docker/server/docker-compose.test.yml exec -T postgres psql -U portfoliodb -d portfoliodb_test -q
	cat server/plugins/local/identifier/migrations/001_instrument_ref.sql | docker compose -f docker/server/docker-compose.test.yml exec -T postgres psql -U portfoliodb -d portfoliodb_test -q
	TEST_DATABASE_URL="postgres://portfoliodb:portfoliodb@localhost:5433/portfoliodb_test?sslmode=disable" go test -v ./server/db/postgres/...
	@docker compose -f docker/server/docker-compose.test.yml down
