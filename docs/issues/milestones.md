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
- **M12** - Move transactions to grouped double-entry postings with exact decimal amounts and an enforced balance constraint.
- **M13** - Automated broker transaction import via a browser extension.
- **M14** - Complete the archive: system and user archives carry everything a rebuild needs apart from portfolio definitions, and the transaction CSV is retired.
- **M15** - The server owns transaction grouping: converters emit evidence and the server derives groups across the whole dataset rather than within one upload.
- **M16** - A person can repair a grouping or a transfer pairing the engine cannot derive.
- **M17** - Operational correctness: scheduled corporate-event recompute, single-sourced instrument data, current planner statistics, and configurable deployment.
- **M18** - One place that says what needs a person's attention, replacing the per-problem admin cards.
- **M19** - Lots, cost basis and realised gains.
- **M20** - Telemetry is run-scoped event rows in Postgres read by Grafana, replacing the Redis counters.

## Unscheduled

- Scheduled exports / initial import of historic price data.
- Corporate events fetched for known instruments and adjustments applied to user transactions idempotently.
- Portfolio performance comparison to indices.
- Portfolio definition based on tagged instruments.
- Carrying portfolio definitions in the user archive, once the definition is settled.
- Portfolio sharing between users and aggregates that combine portfolios (including shared portfolios).
- Transaction importer for IBKR.
- Transaction importer for SCHB.
- Browser extension recipes for further brokers.
- Exchange and listing currency: identify and store per transaction/instrument (and support multiple listings per instrument if needed).
- Modular ingestion workflow with distinct tasks and explicit dependency modelling.
- Alerting for data that needs human review.
- User override of instrument identity (user-owned data); admin correction of shared instrument identity.
- Loading index instrument metadata and price data for performance comparison.
- Portfolio performance metrics: time-weighted return (TWR) and money-weighted return (MWR).
