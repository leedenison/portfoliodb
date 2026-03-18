## Project Plan

### Milestones

| Milestone ID | Description                                                                                                          | Status |
| ------------ | -------------------------------------------------------------------------------------------------------------------- | ------ |
| M01          | Track holdings of instruments using broker-description only (no identification or prices, investment instruments).   | Done   |
| M02          | Google sign-in authentication and admin role.                                                                        | Done   |
| M03          | Add basic support for derivatives, multiple accounts, portfolios.                                                    | Done   |
| M04          | Implement transaction importing using in-codebase, broker specific converters.                                       | Done   |
| M05          | Add telemetry (counters, logging).                                                                                   | Done   |
| M06          | Import / export of instrument identities                                                                             |        |
| M07          | Instruments can be identified from broker descriptions.                                                              | Done   |
| M08          | Historical prices can be fetched for identified instruments.                                                         |        |


### Tasks

| Task ID | Description                                                                         | Depends on         | Milestone | Status |
| ------- | ----------------------------------------------------------------------------------- | ------------------ | --------- | ------ |
| T01     | Create datamodel, gRPC ingestion API, gRPC client API.                              |                    | M01       | Done   |
| T02     | Basic backend service with dummy auth.                                              | T01                | M01       | Done   |
| T03     | Frontend client - SPA + gRPC-Web.                                                   | T01, T02           | M01       | Done   |
| T04     | Docker-compose orchestration - Redis, postgres, Envoy.                              | T01, T02, T03      | M01       | Done   |
| T05     | Auto loading of server and front end changes in development.                        | T04                | M01       | Done   |
| T06     | Create Auth gRPC API using Google ID token to bootstrap session.                    |                    | M02       | Done   |
| T07     | Frontend Google sign-in button an authentication flow.                              | T06                | M02       | Done   |
| T08     | Support for derivative with underlying instruments.                                 |                    | M03       | Done   |
| T09     | Support for multiple accounts per broker.                                           |                    | M03       | Done   |
| T10     | Support for portfolios as views on user owned transactions.                         |                    | M03       | Done   |
| T11     | Define a standard, broker independent CSV upload format.                            |                    | M04       | Done   |
| T12     | Create UI to upload a CSV of transactions and select the 'standard' format.         | T11                | M04       | Done   |
| T13     | Upload and conversion from Fidelity CSV to the standard format.                     | T11                | M04       | Done   |
| T14     | Redis-backed counters for notable code paths; admin page to view counters.          |                    | M05       | Done   |
| T15     | Server logger (stdout, LOG_LEVEL env); log OpenFIGI invocations and outcomes.       |                    | M05       | Done   |
| T16     | Export/import API for instrument information.                                       |                    | M06       | Done   |
| T17     | Filter instrument export by broker and exchange.                                    | T16                | M06       |        |
| T18     | Instrument identification plugin Go API.                                            |                    | M07       | Done   |
| T19     | Implement network based identification plugin based on ChatGPT and OpenFigi data.   | T18                | M07       | Done   |
| T20     | Admin UI for configuring identification plugins.                                    | T19                | M07       | Done   |
| T21     | Implement UI for showing instrument identities and errors that occurred.            | T19                | M07       |        |
| T22     | Price storage schema and API for current and historical prices per instrument.      |                    | M08       |        |
| T23     | Price plugin Go API (e.g. FetchPrices) and at least one plugin implementation.      | T22                | M08       |        |
| T24     | Admin UI (or API) for manual price entry when no automatic source is available.     | T22                | M08       |        |
| T25     | Create CLI for importing / exporting instrument identities to and from CSV.         |                    | M06       |        |
| T26     | Create Admin UI for importing / exporting instrument identities to and from CSV.    |                    | M06       | Done   |


### Unscheduled Milestones

| Milestone ID | Description                                                                                                          | Status |
| ------------ | -------------------------------------------------------------------------------------------------------------------- | ------ |
|              | Scheduled exports / initial import of historic price data.                                                           |        |
|              | Corporate events can be fetched for know instruments and adjustments applied to user transactions idempotently.      |        |
|              | Portfolio composition UI.                                                                                            |        |
|              | Portfolio performance comparison to indices.                                                                         |        |
|              | Portfolio definition based on tagged instruments.                                                                    |        |
|              | Implement portfolio sharing between users and aggregates which combine portfolios (incl. shared portfolios).         |        |
|              | Transaction importer for IBKR.                                                                                       |        |
|              | Transaction importer for SCHB.                                                                                       |        |
|              | Exchange and listing currency: identify and store per transaction/instrument (and support multiple listings per instrument if needed). |        |
|              | User override of instrument identity (user-owned data); admin correction of shared instrument identity.              |        |
|              | Support for loading index instrument metadata and price data for performance comparison.                             |        |
|              | Portfolio performance metrics: time-weighted return (TWR) and money-weighted return (MWR).                           |        |

### Unscheduled Tasks

| Task ID | Description                                                                                                                                  | Depends on |
| ------- | -------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
|         | ListTxs: optional filter by broker (and optionally account) for CreateTx recovery.                                                          |            |
|         | UI: recovery flow for failed CreateTx (list txs for broker+period, edit and re-upload via bulk).                                            | Above      |
|         | ListTxs (and/or export): optional filter by transaction type (OFX types).                                                                   |            |
|         | Identify exchange and listing currency during instrument resolution; persist on instrument/transaction as specified.                         |            |
|         | Corporate events datamodel and API (fetch/store events per instrument).                                                                     |            |
|         | Corporate events plugin API and at least one plugin; apply adjustments to user transactions idempotently.                                   | Above      |
|         | Admin UI or API for manual entry of corporate events when no automatic source is available.                                                 | Above      |
|         | Periodic job to re-attempt instrument identification (e.g. broker-description-only or stale).                                              |            |
|         | Admin API and UI: manually force identification refresh for a given instrument or set of instruments.                                       |            |
|         | Datamodel and API for user-level instrument override (e.g. portfolio-level mapping).                                                        |            |
|         | Admin API and UI to correct shared instrument identity (e.g. merge, edit, reassign identifiers).                                           |            |
|         | Populate and use instrument valid_from / valid_to (from plugins or admin); expose in API/UI where relevant.                                |            |
|         | Compute and expose TWR and MWR for a portfolio over a period (requires price data).                                                         | T22        |
|         | Instrument tags (tag type / tag value); store OpenFIGI market sector and related fields as tags when identification plugins provide them.   |            |
|         | Move Uploads to user dropdown menu; add ListJobs API and paginated upload errors UI.                                                        |            |
|         | Security review: move DB credentials out of Dockerfile/docker-compose into env vars; make Envoy CORS origins configurable; make session TTL and cookie attributes configurable via env vars. | |
|         | Make frontend SSR API base URL configurable via env var instead of hard-coded localhost:8080 fallback.                                     |            |
