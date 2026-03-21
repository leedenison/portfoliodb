## Project Plan

### Milestones

| Milestone ID | Description                                                                                                          | Status |
| ------------ | -------------------------------------------------------------------------------------------------------------------- | ------ |
| M01          | Track holdings of instruments using broker-description only (no identification or prices, investment instruments).   | Done   |
| M02          | Google sign-in authentication and admin role.                                                                        | Done   |
| M03          | Add basic support for derivatives, multiple accounts, portfolios.                                                    | Done   |
| M04          | Implement transaction importing using in-codebase, broker specific converters.                                       | Done   |
| M05          | Add telemetry (counters, logging).                                                                                   | Done   |
| M06          | Import / export of instrument identities                                                                             | Done   |
| M07          | Instruments can be identified from broker descriptions.                                                              | Done   |
| M08          | Historical prices can be fetched for identified instruments.                                                         | Done   |
| M09          | Portfolio filtering UI.                                                                                              | Done   |
| M10          | Allow per user fixed points to define initial holdings.                                                              |        |
| M11          | Allow users to set a display currency.                                                                               |        |

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
| T22     | Price storage schema and API for current and historical prices per instrument.      |                    | M08       | Done   |
| T23     | Price plugin Go API (e.g. FetchPrices) and at least one plugin implementation.      | T22                | M08       | Done   |
| T24     | Admin UI (or API) for manual price entry when no automatic source is available.     | T22                | M08       |        |
| T25     | Create CLI for importing / exporting instrument identities to and from CSV.         |                    | M06       |        |
| T26     | Create Admin UI for importing / exporting instrument identities to and from CSV.    |                    | M06       | Done   |
| T27     | Schema changes for display currency: add FX asset class, FX_PAIR identifier type, display_currency column on users, seed FX instruments. |                    | M11       |        |
| T28     | Add display_currency user preference gRPC API and settings UI.                      | T27                | M11       |        |
| T29     | Implement FXGaps in the price cache DB layer per docs/prices.md.                    | T27                | M11       |        |
| T30     | Extend Massive price plugin for FX rate fetching per docs/prices.md.                | T27                | M11       |        |
| T31     | Extend price fetcher worker to call FXGaps and fetch FX rates.                      | T29, T30           | M11       |        |
| T32     | Update valuation queries to convert holdings to display currency per docs/performance.md. | T27, T29      | M11       |        |
| T33     | Update performance chart UI to pass display currency and show currency label.        | T28, T32           | M11       |        |


### Unscheduled Milestones

| Milestone ID | Description                                                                                                          | Status |
| ------------ | -------------------------------------------------------------------------------------------------------------------- | ------ |
|              | Scheduled exports / initial import of historic price data.                                                           |        |
|              | Corporate events can be fetched for known instruments and adjustments applied to user transactions idempotently.     |        |
|              | Portfolio performance comparison to indices.                                                                         |        |
|              | Portfolio definition based on tagged instruments.                                                                    |        |
|              | Implement portfolio sharing between users and aggregates which combine portfolios (incl. shared portfolios).         |        |
|              | Transaction importer for IBKR.                                                                                       |        |
|              | Transaction importer for SCHB.                                                                                       |        |
|              | Exchange and listing currency: identify and store per transaction/instrument (and support multiple listings per instrument if needed). |        |
|              | Investigate and implement a modular ingestion workflow with distinct tasks and explicit dependency modelling. |        |
|              | User override of instrument identity (user-owned data); admin correction of shared instrument identity.              |        |
|              | Support for loading index instrument metadata and price data for performance comparison.                             |        |
|              | Portfolio performance metrics: time-weighted return (TWR) and money-weighted return (MWR).                           |        |

### Unscheduled Tasks

| Task ID | Description                                                                                                                                  | Depends on |
| ------- | -------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
|         | Security review: move DB credentials out of Dockerfile/docker-compose into env vars; make Envoy CORS origins configurable; make session TTL and cookie attributes configurable via env vars. | |
|         | Make frontend SSR API base URL configurable via env var instead of hard-coded localhost:8080 fallback.                                     |            |
