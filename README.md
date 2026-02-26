# PortfolioDB

gRPC-backed service for portfolio and transaction data. Backend is Go with PostgreSQL; API and ingestion are defined in Protocol Buffers.

## Prerequisites

To develop and run this project you need:

| Tool | Purpose |
|------|---------|
| **Go 1.25+** | Build and run the server; run tests. |
| **Docker & Docker Compose** | Run PostgreSQL for local development and for the DB integration tests. |
| **Buf CLI** | Generate Go code from `.proto` files (`make generate`). Install: [buf.build/docs/installation](https://buf.build/docs/installation) (e.g. `go install github.com/bufbuild/buf/cmd/buf@latest` or official install script). |

Optional for manual gRPC checks:

- **grpcurl** – call gRPC endpoints from the CLI (e.g. `go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest`).

## Quick start

These steps verify the system builds, tests pass, and the server can run against a real database.

### 1. Generate code and build

```bash
make generate
make build
```

### 2. Unit tests

```bash
make test
```

Runs all tests under `./server/...` except the Postgres integration tests (those need a DB).

### 3. Database integration tests

Starts Postgres in Docker, applies migrations, runs `server/db/postgres` tests, then tears down:

```bash
make test-db
```

### 4. Run the server locally

From the repo root:

```bash
# Start Postgres
docker compose -f docker/server/docker-compose.yml up -d postgres

# Apply migrations
cat server/migrations/001_initial.sql | docker compose -f docker/server/docker-compose.yml exec -T postgres psql -U portfoliodb -d portfoliodb -q

# Run the server (uses same DB as Docker Postgres)
export PORTFOLIODB_DB_URL="postgres://portfoliodb:portfoliodb@localhost:5432/portfoliodb?sslmode=disable"
```

The gRPC server listens on `localhost:50051`. To run the server in Docker:

```bash
docker compose -f docker/server/docker-compose.yml up -d
```

### 5. Smoke test the gRPC server with grpcurl

With the server running on `localhost:50051`, from the repo root:

```bash
grpcurl -plaintext \
  -H 'x-auth-sub: smoke-test' \
  -H 'x-auth-name: Smoke Test' \
  -H 'x-auth-email: smoke@local' \
  -import-path proto \
  -proto proto/api/v1/api.proto \
  -d '{"auth_sub":"smoke-test","name":"Smoke Test","email":"smoke@local"}' \
  localhost:50051 \
  portfoliodb.api.v1.ApiService/CreateUser
```

A successful response includes a `user_id`. Running it again is idempotent (same user, same ID).
