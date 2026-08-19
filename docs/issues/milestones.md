# Milestones

Milestones carry no status of their own -- completion is read from the `status`
of the issues assigned to them.

There are two label schemes. **M** is functionality: the system does something
it did not do before. **P** is productionisation: the same functionality running
somewhere other than a dev laptop -- credentials, origins, tuning, compatibility
guarantees. The split exists so that productionisation is deferred deliberately
rather than by always losing to the next feature; P01 is the gate on the first
real deployment, not a list that never comes due.

The M numbers currently run in rough priority order, which for the open work is
also dependency order: no open issue depends on an issue in a higher-numbered
milestone. Neither property is promised to hold as milestones are added.

Numbers are not reused. A gap is a milestone that was retired.

- **M01** - Track holdings of instruments using broker-description only (no identification or prices; investment instruments).
- **M02** - Google sign-in authentication and admin role.
- **M03** - Add basic support for derivatives, multiple accounts, and portfolios.
- **M04** - Implement transaction importing using in-codebase, broker-specific converters.
- **M05** - Add telemetry (counters, logging).
- **M06** - Import / export of instrument identities.
- **M07** - Instruments can be identified from broker descriptions.
- **M08** - Historical prices can be fetched for identified instruments.
- **M10** - Allow per-user fixed points to define initial holdings.
- **M11** - Allow users to set a display currency.
- **M12** - Move transactions to grouped double-entry postings with exact decimal amounts and an enforced balance constraint.
- **M13** - Automated broker transaction import via a browser extension.
- **M14** - Complete the archive: system and user archives carry everything a rebuild needs apart from portfolio definitions, and the transaction CSV is retired.
- **M15** - The server owns transaction grouping: converters emit evidence and the server derives groups across the whole dataset rather than within one upload.
- **M16** - Instrument identity is time-varying: an identifier carries a validity interval, so a split mints a name rather than rewriting one, and resolution asks what a name denoted on a date rather than what it denotes now.
- **M17** - An instrument can be identified from a description alone, for the sources that carry no identifier -- and how well that works is measured rather than assumed.
- **M18** - A complete history reaches the system without hand work: opening balances entered in bulk, a converter for every broker held, and income attributed to the security that paid it.
- **M19** - Lots, cost basis, realised gains, and what the portfolio costs to run.
- **M20** - Telemetry is run-scoped event rows in Postgres read by Grafana, replacing the Redis counters.
- **M21** - One place that says what needs a person's attention, and the repairs reachable from it, replacing the per-problem admin cards.
- **M22** - What a portfolio selects is settled, and a portfolio definition survives a rebuild.
- **M23** - Everything that has to happen on a cadence has something running it.

## Productionisation

- **P01** - PortfolioDB can be deployed and left running: configuration and credentials out of the image, planner statistics current under bulk writes, and wire compatibility checked at the first release.

## Unscheduled

- Scheduled exports / initial import of historic price data.
- Corporate events fetched for known instruments and adjustments applied to user transactions idempotently.
- Portfolio performance comparison to indices.
- Portfolio definition based on tagged instruments.
- Portfolio sharing between users and aggregates that combine portfolios (including shared portfolios).
- Browser extension recipes for further brokers.
- Exchange and listing currency: identify and store per transaction/instrument (and support multiple listings per instrument if needed).
- Modular ingestion workflow with distinct tasks and explicit dependency modelling.
- Loading index instrument metadata and price data for performance comparison.
- Portfolio performance metrics: time-weighted return (TWR) and money-weighted return (MWR).
