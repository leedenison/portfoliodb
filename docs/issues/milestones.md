# Milestones

- **M01** - Track holdings of instruments using broker-description only (no identification or prices; investment instruments).
- **M02** - Google sign-in authentication and admin role.
- **M03** - Add basic support for derivatives, multiple accounts, and portfolios.
- **M04** - Implement transaction importing using in-codebase, broker-specific converters.
- **M05** - Add telemetry (counters, logging).
- **M06** - Import / export of instrument identities.
- **M07** - Instruments can be identified from broker descriptions.
- **M08** - Historical prices can be fetched for identified instruments.
- **M09** - Portfolio filtering UI.
- **M10** - Allow per-user fixed points to define initial holdings.
- **M11** - Allow users to set a display currency.

## Unscheduled

- Scheduled exports / initial import of historic price data.
- Corporate events fetched for known instruments and adjustments applied to user transactions idempotently.
- Portfolio performance comparison to indices.
- Portfolio definition based on tagged instruments.
- Portfolio sharing between users and aggregates that combine portfolios (including shared portfolios).
- Transaction importer for IBKR.
- Transaction importer for SCHB.
- Exchange and listing currency: identify and store per transaction/instrument (and support multiple listings per instrument if needed).
- Modular ingestion workflow with distinct tasks and explicit dependency modelling.
- User override of instrument identity (user-owned data); admin correction of shared instrument identity.
- Loading index instrument metadata and price data for performance comparison.
- Portfolio performance metrics: time-weighted return (TWR) and money-weighted return (MWR).
