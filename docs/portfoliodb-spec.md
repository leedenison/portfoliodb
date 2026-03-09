## PortfolioDB Spec

PortfolioDB is portfolio tracking software which consists of backend services hosted in docker containers, and which serve a web based front end.

PortfolioDBs purpose is to track the holdings (the quantity held) of equities, options and futures for users portfolios.  In addition, PortfolioDB tries to automatically identify the instruments held in the portfolio and, if successful, it can fetch current and historical prices for those instruments in order to provide current and historical portfolio values.  It can also calculate performance metrics such as the time weighted return and the money weighted return.

The challenge of identifying instruments is made complicated because of incomplete and imprecise data.  Typically users will provide data about trades based on information from their brokers.  Their brokers will usually not provide standard IDs for the instruments being traded (CUSIP, ISIN, etc).  The system uses external APIs and datasources to identify instruments based on the information provided by the broker.  The system should degrade gracefully when an instrument cannot be identified successfully (more details in the sections below).

In the future PortfolioDB will be extended to track cash accounts, large assets (eg. real estate) and debt (including mortgages) to provide an overall financial status beyond any particular portfolio.

## User Model

PortfolioDB is a multi-user service.  Users create accounts and their data remains separate from other users.  Each user account can create multiple portfolios.

In a portfolio the holdings information is owned by the user.  Instrument identities and price information is shared across all users.

## Authentication and authorization

See [docs/auth.md](docs/auth.md) for authentication, session bootstrap, and authorization.

## Data Ingestion

Users can ingest transaction data in bulk or as single transactions.

Typically a bulk upload will result from a user uploading a CSV of transactions obtained from their broker.  The web client will convert from the broker specific format to the PortfolioDB API.  Bulk uploads will be processed asynchronously and validation errors reported through the web interface.  Bulk upload should be tolerant of errors that can be easily corrected in the web interface (eg. an unknown currency for an instrument) but should reject the entire upload in more serious cases (eg. the same logical instrument in the upload providing contradictory identifiers).  Bulk uploads should specify the period of transactions that they cover for a given broker.  Idempotency is ensured because the system should assume that transactions for the given broker should be entirely replaced with the uploaded transactions.  Transactions for a given broker are never merged.

Typically a single transaction upload will result from a user forwarding transaction notifications from their broker.  The user is assumed to have implemented their own script to receive broker notifications and convert them to the PortfolioDB API.  Their script will create a transaction using a credential obtained previously through the web interface.  Single-transaction uploads use the **CreateTx** API and are **append-only**: each call inserts a new transaction.  Duplicate submissions (e.g. the same notification sent twice) will create duplicate rows and double-count in holdings; scripts should avoid sending the same transaction more than once.  Single-transaction requests are processed asynchronously with validation errors reported through the web interface.

**Recovery from CreateTx failure:** When a client uses CreateTx and the job fails (e.g. validation error, identification error, or other failure), the client cannot retry CreateTx to fix that single transaction in place.  The client should instead query all transactions in a period that covers the failed transaction (via the front-end API) and correct the error by re-ingesting the corrected set using the bulk upload API (UpsertTxs) for that broker and period, which replaces all transactions in the period with the supplied list.

Ingestion requests must include a **source** (opaque string; expected format `<broker>:<client>:<source>`). Source is used for instrument resolution.

Broker statements often supply only the date (not a full timestamp) for transactions, so transactions do not have a reliable natural key; the system does not enforce uniqueness on transactions.  

## Upload Formats

The client supports uploading transactions in several formats:

* 'Standard' format - A CSV format that directly represents the fields expected by the API.  Users can convert to this format outside of the client if their broker specific format is not supported.
* Broker formats - Brokers use a range of custom schemas and formats (CSV, JSON, OFX, etc) for their transaction downloads.  The portfoliodb client supplies several packages within its own codebase that convert from the broker schema/format to the standard CSV format before upload.  Portfoliodb also allows npm packages to be installed and used for the same purpose.

## Associating Instruments with Transactions

Each transaction modifies holding data for a specific canonical instrument (and possibly modifies a cash holding).  So an instrument must be associated with every transaction.

Every valid transaction must end up with an **instrument_id**: either from plugin resolution or from a **broker-description-only** instrument (an instrument whose only identifier is that source’s description). Truly unidentified transactions must not exist and are considered a fatal validation error.  See docs/identifiers.md.

## Identifying Instruments

Identifying an instrument means associating the canonical **instrument** (security master) with zero or more **identifiers** (opaque type + domain + value, e.g. ISIN, CUSIP, EXCHANGE + TICKER, FIGI, broker description, etc).  The process of identifying instruments happens during transaction upload processing and periodically (see docs/identifiers.md).

### Transaction ingestion: resolution cases

The following diagram illustrates how each transaction is resolved to an instrument during upload. Optional client **hints** (exchange, currency, MIC, security type) are used only to narrow resolution; the decision tree is driven by whether the client supplies **identifier hints** (e.g. ISIN, TICKER) and by the outcomes of the **description** and **identifier** plugins. Transactions whose type maps to security type **None** (e.g. SPLIT) are not persisted.

```mermaid
flowchart TD
    Start([Tx upload: source + description + optional hints])
    Start --> SecurityNone{Type maps to SecurityType None?}
    SecurityNone -->|Yes| Drop[Drop tx, do not store]
    SecurityNone -->|No| HasIdHints{Client supplied identifier hints?}

    HasIdHints -->|Yes| LookupByHints[DB lookup by identifier hints]
    LookupByHints --> HintsOneId{Exactly one instrument?}
    HintsOneId -->|Yes| DoneNoStore[Use instrument (do not store source, description)]
    HintsOneId -->|No, 0 or >1| IdPluginsWithHints[Call identifier plugins with hints]
    IdPluginsWithHints --> IdResolveClient{Plugin(s) resolved?}
    IdResolveClient -->|Yes| CanonicalFromClient[Canonical instrument (no source/desc stored)]
    IdResolveClient -->|No / timeout / unavailable| BrokerOnlyFromClient[Broker-description-only instrument + identification error]

    HasIdHints -->|No| LookupByDesc[DB lookup by source + description]
    LookupByDesc --> DescHit{Cache hit?}
    DescHit -->|Yes| DoneCached[Use existing instrument]

    DescHit -->|No| RunDescPlugins[Run description plugins in series by precedence]
    RunDescPlugins --> DescReturned{Description plugin(s) return identifiers?}
    DescReturned -->|No| NoExtraction[Broker-description-only instrument + description extraction failed (identifier plugins not called)]
    DescReturned -->|Yes| MaybeDbByHints[DB lookup by extracted hints]
    MaybeDbByHints --> ExtHintsOne{Exactly one instrument?}
    ExtHintsOne -->|Yes| DoneWithStore[Use instrument, store source and description on instrument]
    ExtHintsOne -->|No| IdPluginsExtracted[Call identifier plugins with extracted hints]
    IdPluginsExtracted --> IdResolveExt{Plugin(s) resolved?}
    IdResolveExt -->|Yes| CanonicalFromExt[Canonical instrument + store source, description]
    IdResolveExt -->|No / timeout / unavailable| BrokerOnlyFromExt[Broker-description-only instrument + broker description only or timeout/unavailable]

    DoneNoStore --> End([Tx linked to instrument])
    CanonicalFromClient --> End
    BrokerOnlyFromClient --> End
    DoneCached --> End
    NoExtraction --> End
    DoneWithStore --> End
    CanonicalFromExt --> End
    BrokerOnlyFromExt --> End
    Drop --> EndDropped([Tx not stored])
```

**Cases summarised:**

| Upload shape | Description plugins | Identifier plugins | Outcome |
|--------------|--------------------|--------------------|---------|
| Txs with **identifiers** (no description-only path) | Not used | Resolve by hints (DB or plugins) | Canonical instrument or broker-description-only; source/description not stored when resolved by client hints. |
| Txs with **description + hints only** | Not run if DB hit by (source, description) | Not run if no extracted hints | Re-upload: use cached instrument. |
| Txs with **description + hints only** | Return **no** identifiers | Not called | Broker-description-only; error "description extraction failed". |
| Txs with **description + hints only** | Return identifiers | Resolve (DB or plugins) | Canonical instrument; (source, description) stored. |
| Txs with **description + hints only** | Return identifiers | Do **not** resolve / timeout / unavailable | Broker-description-only; error "broker description only" or "plugin timeout" / "plugin unavailable". |

Other behaviours (see docs/identifiers.md): conflicting client identifiers → validation error; same (source, description) in batch → resolved once and cached; instrument merge when plugins link two existing instruments.

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

For instrument resolution, JRNLFUND is treated as security type Cash and JRNLSEC as Equity. Transaction types that map to security type **None** (e.g. SPLIT) are not stored on upload; they are dropped.

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