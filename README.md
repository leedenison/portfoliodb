# PortfolioDB

gRPC-backed service for portfolio and transaction data. Backend is Go with PostgreSQL; API and ingestion are defined in Protocol Buffers.

## Prerequisites

To develop and run this project you need:

| Tool | Purpose |
|------|---------|
| **Go 1.25+** | Build and run the server; run tests; install buf and protoc plugins via `make tools`. |
| **Node.js & npm** | Client app and `protoc-gen-es` for TypeScript codegen; install client deps via `make tools`. |
| **Docker & Docker Compose** | Run PostgreSQL for local development and for the DB integration tests. |

Install project tooling and dependencies (buf, protoc-gen-go, protoc-gen-go-grpc, and client npm packages) with:

```bash
make tools
```

Ensure `$(go env GOPATH)/bin` is on your `PATH` so `buf` and the protoc plugins are found.

## Quick start

These steps verify the system builds and the core test suites pass.

### 1. Install tools and generate code

```bash
make tools
make generate
make build
```

### 2. Run server tests

`make server-test` runs all server tests.

```bash
make server-test
```

### 3. Run client tests

`make client-test` runs the Next.js/Vitest front-end tests under `client/`.

```bash
make client-test
```

### 4. Run database tests

`make db-test` brings up Postgres in Docker, applies migrations, runs the DB integration tests, and then tears everything down.

```bash
make db-test
```

### 5. Run the server locally

From the repo root, configure the environment and then use `make run`:

```bash
# Required: Google OAuth client ID used for login (Web application)
# Must be set before the client docker container is built.
export GOOGLE_OAUTH_CLIENT_ID="your-client-id.apps.googleusercontent.com"

# Required for the client to talk to the server via gRPC-Web / Envoy
export NEXT_PUBLIC_GRPC_WEB_BASE="http://localhost:8080"

# Optional: treat a specific Google user as admin (instrument import/export, admin UI)
# To find your Google subject (`sub`), you can use the oauth3 playground:
# https://www.oauth.com/playground/google-openid-connect/
export ADMIN_AUTH_SUB="your-google-subject-id"

# Start Postgres, Redis, Envoy, and the server
make run
```

The gRPC server listens on `localhost:50051` behind Envoy on `http://localhost:8080` for gRPC-Web.

**GOOGLE_OAUTH_CLIENT_ID** is required for Auth (server uses it to verify Google ID tokens). Create a **OAuth 2.0 Client ID** (Web application) in [Google Cloud Console](https://console.cloud.google.com/apis/credentials).

**DB_INITIALISE_SCRIPT** (optional) — Path to a SQL file run against the database after Postgres is up, when you use `make run`. Set it in `.env` (e.g. `DB_INITIALISE_SCRIPT=local/dev-init.sql`). Use it to load seed data, create test users, or run one-off migrations. If unset or the file is missing, the step is skipped. 
