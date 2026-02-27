## PortfolioDB Spec

PortfolioDB is portfolio tracking software which consists of backend services hosted in docker containers, and which serve a web based front end.

PortfolioDBs purpose is to track the holdings (the quantity held) of equities, options and futures for users portfolios.  In addition, PortfolioDB tries to automatically identify the instruments held in the portfolio and, if successful, it can fetch current and historical prices for those instruments in order to provide current and historical portfolio values.  It can also calculate performance metrics such as the time weighted return and the money weighted return.

The challenge of identifying instruments is made complicated because of incomplete and imprecise data.  Typically users will provide data about trades based on information from their brokers.  Their brokers will usually not provide standard IDs for the instruments being traded (CUSIP, ISIN, etc).  The system uses external APIs and datasources to identify instruments based on the information provided by the broker.  The system should degrade gracefully when an instrument cannot be identified successfully (more details in the sections below).

In the future PortfolioDB will be extended to track cash accounts, large assets (eg. real estate) and debt (including mortgages) to provide an overall financial status beyond any particular portfolio.

## User Model

PortfolioDB is a multi-user service.  Users create accounts and their data remains separate from other users.  Each user account can create multiple portfolios.

In a portfolio the holdings information is owned by the user.  Instrument identities and price information is shared across all users.

## Authentication

The service will run behind an OAuth2 proxy, so the service should assume that credentials will be included in an Authorization header for authenticated requests \- and that any request with an Authorization header has been successfully authenticated.

Account creation will be handled by an explicit create user endpoint.  Any request authenticated with an unknown user should return an error.  User's name and email address will be extracted from an ID token provided to the create user endpoint.

## Authorization

The service supports two roles: "user" and "admin".  Users own and can update their own portfolio data.  Admin users can manage other users and can update shared instrument and price data.

## Data Ingestion

Users can ingest transaction data in bulk or as single transactions.

Typically a bulk upload will result from a user uploading a CSV of transactions obtained from their broker.  The web client will convert from the broker specific format to the PortfolioDB API.  Bulk uploads will be processed asynchronously and validation errors reported through the web interface.  Bulk upload should be tolerant of errors that can be easily corrected in the web interface (eg. an unknown currency for an instrument) but should reject the entire upload in more serious cases (eg. the same instrument providing contradictory identifiers).  Bulk uploads should specify the period of transactions that they cover for a given broker.  Idempotency is ensured because the system should assume that transactions for the given broker should be entirely replaced with the uploaded transactions.  Transactions for a given broker are never merged.

Typically a single transaction upload will result from a user forwarding transaction notifications from their broker.  The user is assumed to have implemented their own script to receive broker notifications and convert them to the PortfolioDB API.  Their script will create a transaction using a credential obtained previously through the web interface.  Calls to the API should be idempotent by treating the timestamp, broker and instrument description as a natural key for the transaction.  This implies that PortfolioDB does not allow identical transactions to occur at the exact same moment in time.  Since we expect single transactions to be invoked by a script they are also processed asynchronously with validation errors reported through the web interface.

## Identifying Instruments

Identifying an instrument means associating broker-supplied data with a canonical **instrument** (security master) and zero or more **identifiers** (opaque type + value, e.g. ISIN, CUSIP, or broker description). Every valid transaction has a broker and an instrument description; missing either is a **validation error**. Every valid transaction ends up with an **instrument_id**: either from plugin resolution or from a **broker-description-only** instrument (an instrument whose only identifier is that broker’s description). Truly unidentified transactions do not exist.

An **instrument** holds canonical data (id, asset class, exchange, currency, name, etc.) independent of how it was identified. An **identifier** is an opaque pair: `identifier_type` (e.g. `"CUSIP"`, `"ISIN"`, or a broker/source name such as `"IBKR"`, `"SCHB"`, or an embellished type like `"IBKR:<client>:statement"`) and `value`. Broker descriptions are stored as identifiers: `identifier_type` = broker or source identifier, `value` = full instrument description. The pair **(identifier_type, value) is unique** in the system; the server does not allow duplicates.

A broker may supply **multiple descriptions for the same stock** (e.g. from a statement, a trade confirmation email, or a tax document). To preserve uniqueness, the **client** must **embellish identifier_type** when the same description value can come from more than one source. For example, if the description was taken from a statement, the client might use `identifier_type` = `"IBKR:<client>:statement"`; from a confirmation, `"IBKR:<client>:confirmation"`. The server does not interpret these strings; it only requires that (identifier_type, value) be unique. Lookup by (broker/source, instrument_description) is a single lookup on (identifier_type, value). **Normalization** of broker descriptions (e.g. avoiding two descriptions for the same instrument when they refer to the same thing) is the **client’s responsibility**; the server stores values as received.

Broker-description-only instruments are first-class: they appear in holdings and the UI by that description. If no plugin resolves a given (broker, instrument_description), the system ensures an instrument exists with at least that broker identifier and attaches it to the transaction; optionally it records an identification warning/error for job status and UI. Plugin failures (e.g. timeout, unavailable) are handled the same way: persist the transaction with the broker-description-only instrument and record an identification error; do not fail the whole job.

PortfolioDB resolves instruments during asynchronous ingestion. Resolution order: (1) DB lookup by (broker, instrument_description) or by existing identifiers; (2) within the current batch, use a cache so the same (broker, description) is resolved once; (3) only if still unresolved, call enabled plugins. This avoids unnecessary or duplicate calls to expensive (e.g. quota-managed) plugins.

Broker strings and canonical identifiers are unique once processed. Two users with the same brokerage and description, or two broker descriptions resolving to the same ISIN, refer to the same instrument so updates are reflected globally.

**Instrument merge**: When a new identifier would link two previously distinct instruments (e.g. same ISIN), the system must merge them: choose a survivor, update all transaction references, move identifiers to the survivor, delete the merged-away instrument. When **multiple** identifiers returned for the same logical security resolve to **more than one** existing instrument (e.g. instrument A has ISIN 1, instrument B has CUSIP 1, and a plugin returns both identifiers for one security), the system **detects** this and **merges** those instruments eagerly during resolution. After merge, a single canonical instrument remains; the survivor is chosen **deterministically** (the instrument with more identifiers wins; if tied, the one with older `created_at`). All updates (transaction references, identifier moves, deletion of the merged-away instrument) happen in one transaction. Merge runs **eagerly** when such a conflict is detected during the resolution step (ingestion path). The same merge logic can be reused by a future periodic job; the job’s scheduling and implementation are out of scope for the initial milestone. When a plugin returns identifiers that match an **existing** instrument, the **identifier is the source of truth**: attach the transaction to that instrument and do not overwrite its canonical fields with the plugin’s output.

**Duplicate (broker, instrument_description) in same batch with different plugin results**: Resolve each (broker, instrument_description) once per batch and cache the result. All transactions in the batch with that key receive the same instrument_id. No per-transaction plugin call for the same key—ensures consistency and avoids extra plugin cost.

**Plugin is unavailable or times out (e.g. external API down)**: Create or find the broker-description-only instrument, set instrument_id, persist the transaction, and record an identification error/warning (e.g. plugin timeout) for GetJob and UI. Do not fail the whole job. Optional: retry the plugin once with backoff before falling back.

**No fixed “complete” set of identifiers**: What identifiers exist for an instrument depends on enabled plugins and instrument type (e.g. some instruments have no CUSIP). The system does not treat “only one standard identifier known” as a hard error for that reason. Merge-on-conflict (above) handles the case where the same security was previously stored under two different instruments (e.g. one had only ISIN, another only CUSIP) by merging them when resolution later sees both.

PortfolioDB should periodically attempt to identify instruments in case datasources have been updated. Admin users can manually force a refresh for a given instrument or set of instruments.

A user may believe the system has mis-identified an instrument. It should be possible for a user to override the identity for their portfolio; that data is user-owned. Admin users can correct shared instrument identity.

### Plugins

Plugins implement a single interface (e.g. `Identify(ctx, broker, instrument_description) → (*Instrument, []Identifier, error)`). Implementations live under `server/plugins/<datasource>/identifier` (e.g. `server/plugins/local/identifier`, `server/plugins/ibkr/identifier`). The shared interface and canonical types (Instrument, Identifier) live in `server/identifier`. Plugins are compiled in and enabled at runtime; configuration is stored in the database.

**Plugin config** (in DB): plugin id, enabled flag, **precedence** (integer, required, unique across plugins; used to resolve conflicts), and optional config JSONB. Scope is global for the initial milestone. No two plugins may share the same precedence.

**Resolution flow**: If the DB or in-batch cache already has an instrument for (broker, instrument_description), do not call plugins. Otherwise call **all enabled plugins in parallel**, then merge results: instrument metadata (name, asset class, etc.) from the highest-precedence plugin that succeeded; **identifiers merged** from all successful plugins—for each identifier **type**, the value from the highest-precedence plugin that returned that type is used (non-overlapping types are combined; same-type conflicts resolved by precedence). Then find or create an instrument (with at least the broker identifier) and set the transaction’s instrument_id. If no plugin resolves (e.g. returns a “not identified” sentinel), the service still ensures a broker-description-only instrument exists and attaches it. Record identification errors/warnings (e.g. plugin timeout, broker-description-only) for GetJob and UI.

Plugins can own database migrations (e.g. reference tables). Plugin migrations live in the plugin directory (eg. `server/plugins/<datasource>/identifier/migrations`). Example: the local reference-data plugin uses a Postgres reference table.

## Fetching Prices

The system should support the ability to fetch current and historical prices for identified instruments.  Actual API / datasource integrations should be implemented as plugins.  The system should support manual entry by admin users if no automatic data source is available.

## Calculating Holdings

PortfolioDB calculates holdings for a particular point in time from the transaction data.  It does not materialise the holdings in the database.

## Exchanges

Instruments should record which exchanges they are traded on and the currencies that they are traded in on those exchanges.  The system should attempt to identify the exchange and the listing currency of a given transaction.  

## Derivatives

Options and futures should be related to their underlying instrument.

## Valid From and To

Stocks, Options and Futures should have valid from and to dates which specify when the instrument was available to trade on the exchange.

## Corporate Events

The system should support the ability to fetch data on stock splits, mergers, delistings, etc.  Actual API / datasource integrations should be implemented as plugins.  The system should support manual entry by admin users if no automatic data source is available.

## Transaction Types

Portfoliodb should support OFX style transaction types.  For investments:

* Buys: BUYDEBT, BUYMF, BUYOPT, BUYOTHER, BUYSTOCK
* Sells: SELLDEBT, SELLMF, SELLOPT, SELLOTHER, SELLSTOCK
* Other actions: INCOME, INVEXPENSE, REINVEST, RETOFCAP, SPLIT, TRANSFER, JRNLFUND, JRNLSEC, MARGININTEREST, CLOSUREOPT

For cash accounts (when support is added):

* CREDIT, DEBIT, INT, DIV, FEE, SRVCHG, DEP, ATM, POS, XFER, CHECK, PAYMENT, CASH, DIRECTDEP, DIRECTDEBIT, REPEATPMT, OTHER

These transaction types need only be interpreted enough to determine the change to the users holdings.  The supplied transaction type should be stored so that transactions can be filtered by type in the future.

## User Interface

The user interface is specified in separate files \- see docs/ui/\*.md

## Security

PortfolioDB should adopt security best practices for web development including, but not limited to:

1. Do not trust user input \- use bind variables when inserting into the database, sanitize any input which will be displayed to the user.  
2. Implement an appropriate CORS policy for a single domain site.

## Performance

API integrations are likely to be paid for and have quota limits.  PortfolioDB should be efficient when calling external APIs, avoid calls for duplicate information and should implement appropriate backoff algorithms when services are interrupted.

## Datamodel Migration

Datamodel definitions should follow the industry standard migrations pattern.

A 'version' file stored in the migrations directory (see docs/layout.md) should contain the numerical index of the latest migration file being edited.  Agents should not update this file; it will only be updated by human editors.  Agents should only create a new migration file when the numerical index has been updated to a non-existant file.  Otherwise changes should be made in place to the existing file identified by the index.