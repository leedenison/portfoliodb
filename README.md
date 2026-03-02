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
- **jq** – JSON parsing and editing for CLI testing; install via your package manager, e.g. `apt install jq` or `brew install jq`).

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

The gRPC server listens on `localhost:50051`.

**Optional:** To treat a user as admin (e.g. for instrument export/import), set `ADMIN_AUTH_SUB` to that user’s Google subject (same value as in their session after Auth). Example: `export ADMIN_AUTH_SUB=smoke-test`.

To run the server in Docker (Postgres, Redis, portfoliodb, Envoy):

```bash
docker compose -f docker/server/docker-compose.yml up -d
```

Set `GOOGLE_OAUTH_CLIENT_ID` (and optionally `ACCOUNT_CREATE_EMAIL_ALLOWLIST`, `ADMIN_AUTH_SUB`) when using Auth. Envoy listens on 8080 (gRPC-Web + CORS + cookies); point the SPA API base to `http://localhost:8080` when using Envoy. CORS is configured for `http://localhost:3000` (SPA origin).

### 5. Run the client (SPA)

The web front end is a Next.js app under `client/`. To run it locally:

```bash
cd client && npm install && npm run dev
```

Open [http://localhost:3000](http://localhost:3000). In full stack setup, Envoy serves the built client (see docker/server); the SPA and API share one origin so session cookies work.

### 6. Smoke test the gRPC server with grpcurl

With the server running on `localhost:50051`, obtain an ID token (e.g. via Google Sign-In or test OAuth flow), then call Auth to establish a session. See [docs/auth.md](docs/auth.md) and scripts/tests for the auth flow. Admin: set `ADMIN_AUTH_SUB` to the user’s Google subject (same value as in session) for admin role.
