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

Account creation will be handled by an explicit create user endpoint.  Any request authenticated with an unknown user should return an error.

## Authorization

The service supports two roles: "user" and "admin".  Users own and can update their own portfolio data.  Admin users can manage other users and can update shared instrument and price data.

## Data Ingestion

Users can ingest transaction data in bulk or as single transactions.

Typically a bulk upload will result from a user uploading a CSV of transactions obtained from their broker.  The web client will convert from the broker specific format to the PortfolioDB API.  Bulk uploads will be processed asynchronously and validation errors reported through the web interface.  Bulk upload should be tolerant of errors that can be easily corrected in the web interface (eg. an unknown currency for an instrument) but should reject the entire upload in more serious cases (eg. the same instrument providing contradictory identifiers).  Bulk uploads should specify the period of transactions that they cover for a given broker.  Idempotency is ensured because the system should assume that transactions for the given broker should be entirely replaced with the uploaded transactions.  Transactions for a given broker are never merged.

Typically a single transaction upload will result from a user forwarding transaction notifications from their broker.  The user is assumed to have implemented their own script to receive broker notifications and convert them to the PortfolioDB API.  Their script will create a transaction using a credential obtained previously through the web interface.  Calls to the API should be idempotent by treating the timestamp, broker and instrument description as a natural key for the transaction.  This implies that PortfolioDB does not allow identical transactions to occur at the exact same moment in time.  Since we expect single transactions to be invoked by a script they are also processed asynchronously with validation errors reported through the web interface.

## Identifying Instruments

Identifying an instrument means associating it with one or more external, canonical identifiers (ie. ISIN, CUSIP, etc).  The goal is to have as many of the imprecise strings used by brokers to identify instruments associated with corresponding canonical identifiers as possible.

PortfolioDB should attempt to identify any instruments uploaded via the API during asynchronous processing.  The system should implement a pluggable architecture in which an extensible set of APIs and datasources can be used to identify instruments based on the information provided by the broker.  The system administrators can enable plugins at runtime depending what APIs they have access to.  Plugins will likely have configuration that the administrator must supply, so the system should provide a way for plugins to present this user interface.

PortfolioDB should periodically attempt to identify unidentified instruments in case its datasources have been updated.  Admin users can manually force a refresh of data for a given instrument or set of instruments.

Broker strings and canonical identifiers should be unique once they are successfully asynchronously processed.  Two users who use the same brokerage might upload transactions that use the same description string.  Similarly two different brokerage strings might resolve to the same ISIN.  These should refer to the same data so that updates are reflected globally.

The system should tolerate instruments which could not be identified which are instead presented simply using the broker string provided.  If an instrument could not be identified, nor a price fetched for it, then any summaries of portfolio performance containing the instrument should warn the user that the value of some instruments are not included.

The system should also handle the edge case in which two instruments must be merged and dependent transactions updated.  This can happen if two instruments exist for some time without any common identifiers, transactions are added to each and then subsequently a common identifier is found.

A user may believe that the system has miss identified an instrument in their portfolio.  In that case it should be possible for a user to override the identity of a given instrument.  This data is also owned by the user and affects only their portfolios.  Admin users are able to correct shared instrument identity information.

### Plugin Precedence

Except in the case of a forced refresh, the first source of data for instrument identities is always the PortfolioDB database.  If an instrument is already identified in the database it should not attempt to fetch data for an instrument using plugins.

If an instrument is not already identified the system should attempt to fetch data using every available plugin.  Admins must configure the plugins to be in a complete ordering which defines precedence.  Any conflicts in data returned by plugins should be logged and resolved by taking the lowest precedence answer.

## Fetching Prices

The system should support the ability to fetch current and historical prices for identified instruments.  Actual API / datasource integrations should be implemented as plugins.  The system should support manual entry by admin users if no automatic data source is available.

## Calculating Holdings

PortfolioDB calculates holdings for a particular point in time from the transaction data.  It does not materialise the holdings in the database.

## Exchanges

Instruments should record which exchanges they are traded on and the currencies that they are traded in on those exchanges.  The system should attempt to identify the exchange and the listing currency of a given transaction.  

## Derivatives

Options and futures should be related to their underlying instrument.

## Valid From and To

Stocks, Options and Futures should have valid from and to dates which specify when the instrument was traded.

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