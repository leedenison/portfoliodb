.PHONY: generate build test test-db clean docker-clean

generate:
	buf generate

clean:
	rm -f portfoliodb portfoliodb.exe
	find proto -name '*.pb.go' -delete 2>/dev/null || true

docker-clean:
	docker compose -f docker/server/docker-compose.yml down --rmi local --volumes
	docker compose -f docker/server/docker-compose.test.yml down --rmi local --volumes

build: generate
	go build -o portfoliodb ./server/cmd/portfoliodb

test:
	go test ./server/...

test-db: generate
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
