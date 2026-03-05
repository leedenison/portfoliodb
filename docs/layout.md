# Repository layout

Single reference for where each component of the PortfolioDB monorepo lives. The repo contains a Go backend, a TypeScript/Next.js front end, and protobuf-defined APIs shared by both.

## Root-level directories

| Directory | Purpose |
| --------- | ------- |
| **proto/** | API definitions (protobuf). Shared contract between server and client; no runtime code. |
| **client/** | Web front end. Next.js (TypeScript) SPA; consumes gRPC/HTTP API and displays portfolio UI. |
| **server/** | Back end. Go services, DB layer, plugins, and migrations. |
| **docs/** | Project documentation: spec, plan, style guides, UI specs, and this layout. |
| **docker/** | Dockerfiles and compose (or scripts) for local dev and QA (e.g. Postgres + PortfolioDB service). |

---

## proto/

Protobuf source only; generated `.pb.go` files under `proto/` are build outputs (see `.gitignore`).

- **proto/**  
  `.proto` files, organized by package path.  
  Example: `proto/api/v1/api.proto`, `proto/ingestion/v1/ingestion.proto`.  
  These define the gRPC services used by the front end and by transaction ingestion.

Generated bindings are produced by buf/protoc: Go code under **proto/** (e.g. `proto/api/v1/*.pb.go`), TypeScript under **client/gen**. Those outputs are in `.gitignore`. See docs/style/protobuf.md for generation rules.

---

## client/

- **client/** (root)  
  Next.js application (TypeScript, Tailwind). Single place for all web UI and API calls to the backend.
- **client/gen/**  
  Generated TypeScript/JavaScript from protobuf (gRPC client stubs, message types). Do not edit; do not commit.

---

## server/

Go code for the PortfolioDB backend: one main service binary, DB abstraction, and pluggable datasource integrations.


- **cmd/**
  Go command entrypoint for the server.
- **server/service/**  
  Main PortfolioDB service: wiring, config, and request routing. The runnable service that speaks gRPC and uses the DB and plugins.
- **server/service/ingestion/**  
  Transaction ingestion handlers (gRPC). Receives bulk and single-transaction uploads from the web client and scripts.
- **server/service/api/**  
  Front-end API handlers (gRPC). Serves portfolio, instrument, and related data to the web client.
- **server/db/**  
  Database abstraction layer. All SQL and Postgres/TimescaleDB access lives here. Rest of the server uses this layer only (no raw SQL elsewhere), so that non-DB code can be unit tested with mocks.
- **server/identifier/**  
  Instrument identification plugin API: interface (e.g. `Identify(ctx, config, broker, instrument_description)`), canonical types (Instrument, Identifier), and plugin registry. Plugin implementations live under `server/plugins/<datasource>/identifier`.
- **server/migrations/**  
  SQL migrations for the Postgres/TimescaleDB datamodel. Industry-standard migrations pattern. A **version** file in this directory holds the numerical index of the latest migration; only human editors update it. See docs/portfoliodb-spec.md (Datamodel Migration). Plugin-owned migrations (e.g. reference tables) live in the plugin directory (eg. `server/plugins/<datasource>/identifier/migrations`).
- **server/plugins/&lt;datasource&gt;/identifier**  
  Go libraries (compiled into the service binary) that identify instruments from broker data for a given datasource (e.g. `local`, IBKR). One subdir per datasource under `server/plugins/`. Each implements the interface in `server/identifier`.
- **server/plugins/&lt;datasource&gt;/price**  
  Plugins that fetch current and historical prices for a datasource.
- **server/plugins/&lt;datasource&gt;/corp**  
  Plugins that fetch corporate events (splits, mergers, delistings, etc.) for a datasource.
- **server/gen/**  
  Generated Go code from protobuf (gRPC server stubs, message types). Do not edit; do not commit.

Other server packages (e.g. **server/auth** for auth helpers) live under **server/** as needed; business logic should go under **server/service** or the DB layer.

---

## docs/

- **docs/portfoliodb-spec.md**  
  Full product and system specification. Consult before features or architectural decisions.
- **docs/plan.md**  
  Project plan, milestones, and tasks.
- **docs/layout.md**  
  This file; single source of truth for directory layout.
- **docs/testing.md**  
  Guidance for unit, functional, and integration testing.
- **docs/style/&lt;language&gt;.md**  
  Style guides (e.g. `docs/style/go.md`, `docs/style/protobuf.md`).
- **docs/ui/*.md**  
  User interface specifications (screens, flows, placeholders).

---

## docker/

- **docker/server/**  
  Development/QA Docker setup: PortfolioDB service and Postgres (with datamodel) for local and human QA testing.
