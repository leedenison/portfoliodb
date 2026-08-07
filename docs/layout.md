# Repository layout

Single reference for where each component of the PortfolioDB monorepo lives. The repo contains a Go backend, a TypeScript/Next.js front end, and protobuf-defined APIs shared by both.

## Root-level directories

| Directory | Purpose |
| --------- | ------- |
| **proto/** | API definitions (protobuf). Shared contract between server and client; no runtime code. |
| **client/** | Web front end. Next.js (TypeScript) SPA; consumes gRPC/HTTP API and displays portfolio UI. |
| **extension/** | Chrome MV3 browser extension. Automates broker transaction import; consumes the gRPC-Web API and reuses the client's CSV converters. |
| **server/** | Back end. Go services, DB layer, plugins, and migrations. |
| **cli/** | Go command line tools that drive the server gRPC interface (data import/export, price loading). |
| **docs/** | Project documentation: spec, plan, UI specs, and this layout. Language style and testing guidance live in `.claude/skills/`. |
| **docker/** | Dockerfiles and compose (or scripts) for local dev and QA (e.g. Postgres + PortfolioDB service). |
| **e2e/** | Playwright end-to-end suites run against the full Docker stack. Governed by the `e2e-testing` skill. |

**client/** and **extension/** are separate npm projects and each carries its own
`eslint.config.mjs` and ESLint install. The client's uses `eslint-config-next`,
which needs `next` resolvable and locates the app relative to the config, so it
only works from inside the project. Nothing is linted twice: the client modules
the extension imports through its path alias live in **client/** and are linted
there. Run both with `make lint-ts`.

No npm project sits at the repo root, deliberately. A root `package-lock.json`
makes Next infer the repo root as its workspace root, which pointed Turbopack's
module graph and file watching at the whole monorepo.

---

## proto/

Protobuf source only; generated `.pb.go` files under `proto/` are build outputs (see `.gitignore`).

- **proto/**  
  `.proto` files, organized by package path.  
  Example: `proto/api/v1/api.proto`, `proto/ingestion/v1/ingestion.proto`.  
  These define the gRPC services used by the front end and by transaction ingestion.  
  `proto/type/v1/type.proto` holds the controlled vocabularies shared by every
  other package; unlike the rest of `proto/` it is not free to break, and
  `docs/adr/0038-controlled-vocabularies-are-shared.md` says why.

Generated bindings are produced by buf/protoc: Go code under **proto/** (e.g. `proto/api/v1/*.pb.go`), TypeScript under **client/gen**. Those outputs are in `.gitignore`. See the `protobuf` skill (`.claude/skills/protobuf/`) for generation rules.

---

## client/

- **client/** (root)  
  Next.js application (TypeScript, Tailwind). Single place for all web UI and API calls to the backend.
- **client/gen/**  
  Generated TypeScript/JavaScript from protobuf (gRPC client stubs, message types). Do not edit; do not commit.

---

## extension/

Chrome MV3 extension (TypeScript, Vite, Vitest) that automates transaction import from broker websites. Behaviour is specified in `docs/spec/broker-import-extension.md`.

It is a client of the same gRPC-Web API the SPA uses, and imports the client's CSV converters and generated protobuf types through a tsconfig path alias rather than duplicating them -- so converters under **client/lib/csv/converters/** must not depend on React.

- **extension/src/background/**  
  Service worker: sync orchestration, API clients, storage, and the run log.
- **extension/src/content/**  
  Content scripts. One on the PortfolioDB origin for session bootstrap; one on the broker origin that interprets recipes.
- **extension/src/brokers/**  
  Per-broker recipes: the site-specific data (selectors, export request, date format) executed by a generic interpreter. Repairing a broken broker integration should be an edit here, not to logic.
- **extension/src/popup/**  
  Popup UI: Sync, dry run, configuration, and the run log.
- **extension/dist/**  
  Build output, loaded unpacked in Chrome. Do not commit.

Built with `make extension` (and `make extension-dev`, `make extension-test`), which reuse the client container.

---

## server/

Go code for the PortfolioDB backend: one main service binary, DB abstraction, and pluggable datasource integrations.


- **server/cmd/**
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
  Instrument identification plugin API: interface (e.g. `Identify(ctx, config, broker, source, instrument_description, hints...)`), canonical types (Instrument, Identifier with optional domain), and plugin registry. Plugin implementations live under `server/plugins/<datasource>/identifier`. The **server/identifier/description** subpackage defines the description plugin interface (`Extract(...)` returning identifier hints) and registry; config is stored in the **description_plugin_config** table (enabled, precedence, config JSONB).
- **server/migrations/**  
  SQL migrations for the Postgres/TimescaleDB datamodel. Industry-standard migrations pattern. Plugin-owned migrations (e.g. reference tables) live in the plugin directory (eg. `server/plugins/<datasource>/identifier/migrations`).
- **server/plugins/&lt;datasource&gt;/identifier**  
  Go libraries (compiled into the service binary) that identify instruments from broker data for a given datasource (e.g. OpenFIGI, IBKR). One subdir per datasource under `server/plugins/`. Each implements the interface in `server/identifier`.
- **server/plugins/&lt;datasource&gt;/description**  
  Description plugins that extract identifier hints (type, domain, value) from raw broker instrument descriptions. They run in series by precedence (from **description_plugin_config**); the first that returns ≥1 hint is used for resolution. Example: `server/plugins/openai/description` (plugin id **openai**).
- **server/plugins/&lt;datasource&gt;/price**  
  Plugins that fetch current and historical prices for a datasource.
- **server/plugins/&lt;datasource&gt;/corp**  
  Plugins that fetch corporate events (splits, mergers, delistings, etc.) for a datasource.
- **server/gen/**  
  Generated Go code from protobuf (gRPC server stubs, message types). Do not edit; do not commit.

Other server packages (e.g. **server/auth** for auth helpers) live under **server/** as needed; business logic should go under **server/service** or the DB layer.

---

## cli/

Go code for the PortfolioDB command line tools.


- **cli/dbio**
  Go cli for importing and exporting instrument identities, price data and transaction data via the server gRPC interface.
- **cli/google**
  Go cli that imports prices from Google Finance via Google Sheets GOOGLEFINANCE formulas. Authenticates to Google (OAuth, Sheets API) and PortfolioDB (gRPC), queries price gaps, creates a spreadsheet with formulas, and imports evaluated results.
  
---

## docs/

- **docs/spec/**  
  Product and system specification (behaviour only). `docs/spec/portfoliodb-spec.md` is the top-level spec; consult before features or architectural decisions. Sub-specs cover auth, identifiers, prices, corporate events, UI information architecture, etc.
- **docs/issues/**  
  Issue tracker: `milestones.md` (milestone labels) plus one `NNNN-slug.md` file per issue. Governed by the `issue-tracker` skill.
- **docs/adr/**  
  Architecture decision records (`NNNN-slug.md`): why decisions were made, kept out of the spec. Governed by the `adr` skill.
- **docs/layout.md**  
  This file; single source of truth for directory layout.

---

## docker/

- **docker/**
  Docker Compose files (dev, test, e2e), Envoy configs, and per-component Dockerfiles in `docker/server/` and `docker/client/`.
