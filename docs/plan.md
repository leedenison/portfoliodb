## Project Plan

### Milestones

| Milestone ID | Description                                                                                                          | Status |
| ------------ | -------------------------------------------------------------------------------------------------------------------- | ------ |
| M01          | Track holdings of instruments using broker-description only (no identification or prices, investment instruments).   | Done   |
| M02          | Google sign-in authentication and admin role.                                                                        | Done   |
| M03          | Add basic support for derivatives, multiple accounts, portfolios.                                                    |        |
| M04          | Implement transaction importing using in-codebase, broker specific converters.                                       |        |
| M05          | Import / export of instrument identities                                                                             |        |
| M06          | Instruments can be identified from broker descriptions.                                                              |        |
| M07          | Historical prices can be fetched for identified instruments.                                                         |        |


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
| T13     | Upload and conversion from Fidelity CSV to the standard format.                     | T11                | M04       |        |
| T14     | Export/import API for instrument information.                                       |                    | M05       | Done   |
| T15     | Filter instrument export by broker and exchange.                                    | T14                | M05       |        |
| T16     | Instrument identification plugin Go API.                                            |                    | M06       | Done   |
| T17     | Implement network based identification plugin based on ChatGPT and OpenFigi data.   | T16                | M06       |        |
| T18     | Admin UI for configuring identification plugins.                                    | T17                | M06       |        |
| T19     | Implement UI for showing instrument identities and errors that occurred.            | T17                | M06       |        |
| T20     | Price storage (table/schema) and API for current and historical prices per instrument (and date). |                    | M07       |        |
| T21     | Price plugin Go API (e.g. FetchPrices) and at least one plugin implementation.    | T20                | M07       |        |
| T22     | Admin UI (or API) for manual price entry when no automatic source is available.    | T20                | M07       |        |

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
|              | User override of instrument identity per portfolio (user-owned data); admin correction of shared instrument identity. |        |
|              | Portfolio performance metrics: time-weighted return (TWR) and money-weighted return (MWR).                           |        |

### Unscheduled Tasks

| Task ID | Description                                                                         | Depends on         | Milestone | Status |
| ------- | ----------------------------------------------------------------------------------- | ------------------ | --------- | ------ |
| T23     | ListTxs: optional filter by broker (and optionally account) for CreateTx recovery.   |                    |           |        |
| T24     | UI: recovery flow for failed CreateTx (list txs for broker+period, edit and re-upload via bulk). | T23                |           |        |
| T25     | ListTxs (and/or export): optional filter by transaction type (OFX types).           |                    |           |        |
| T26     | Identify exchange and listing currency during instrument resolution; persist on instrument/transaction as specified. |                    |           |        |
| T27     | Corporate events datamodel and API (fetch/store events per instrument).              |                    |           |        |
| T28     | Corporate events plugin API and at least one plugin; apply adjustments to user transactions idempotently. | T27                |           |        |
| T29     | Admin UI or API for manual entry of corporate events when no automatic source is available. | T27                |           |        |
| T30     | Periodic job to re-attempt instrument identification (e.g. broker-description-only or stale). |                    |           |        |
| T31     | Admin API and UI: manually force identification refresh for a given instrument or set of instruments. |                    |           |        |
| T32     | Datamodel and API for user-level instrument override (e.g. portfolio-level mapping). |                    |           |        |
| T33     | Admin API and UI to correct shared instrument identity (e.g. merge, edit, reassign identifiers). |                    |           |        |
| T34     | Populate and use instrument valid_from / valid_to (from plugins or admin); expose in API/UI where relevant. |                    |           |        |
| T35     | Compute and expose TWR and MWR for a portfolio over a period (requires price data). | T20                |           |        |
