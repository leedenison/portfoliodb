# PortfolioDB

gRPC-backed service for portfolio and transaction data. Backend is Go with PostgreSQL; API and ingestion are defined in Protocol Buffers.

## Prerequisites

| Tool | Purpose |
|------|---------|
| **Docker & Docker Compose** | Required for local development, building, testing, and code generation. |

## Quick start

These steps verify the system builds and the core test suites pass.

### 1. Generate code and build

```bash
make build
```

This runs protobuf and mock code generation as a dependency, then builds the server binary.

### 2. Run the tests

```bash
make test
```

Runs the server, client, database, and plugin integration test suites. Individual suites are available as `make server-test`, `make client-test`, `make db-test`, and `make integration-test`.

### 3. Run the server locally

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
