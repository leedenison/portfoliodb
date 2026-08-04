# Import formats

Three file formats feed the import APIs: a transaction CSV, a price CSV, and a corporate event JSON. They share the conventions for comment metadata and for [coverage declarations](#coverage-declarations).

## Standard transaction CSV

The **Standard** format is a CSV that directly represents the transaction fields expected by the API. Users can produce this CSV manually or use a broker-specific converter (when available) that outputs Standard format.

### Columns

Header names are case-insensitive. Supported column names:

| Column                   | Required | Description |
| ------------------------ | -------- | ----------- |
| `date` or `timestamp`    | Yes      | Transaction date/time. ISO 8601 (e.g. `2024-01-15` or `2024-01-15T14:30:00Z`) or `YYYY-MM-DD`. Also the share count the row's quantity and unit price are denominated in, unless the upload declares otherwise (see [bitemporality.md](bitemporality.md#share-count-basis)). |
| `instrument_description`| Yes      | Broker's instrument description (e.g. symbol, name, or broker-specific text). |
| `type`                  | Yes      | OFX-style transaction type: see allowed values below. |
| `quantity`              | Yes      | Signed number: positive for buys/adds, negative for sells/reductions. |
| `trading_currency`       | No       | Instrument trading currency (e.g. EUR); used as plugin hint. |
| `settlement_currency`    | No       | Settlement/payment currency (e.g. GBP). |
| `unit_price`             | No       | Unit price as reported by broker. An empty cell means the source gave no price; `0` is a price of zero, which is not the same thing: balancing a group converts a purchase or sale at its price, so an option expiring worthless converts at zero while a row with no price cannot convert at all. |
| `account`                | No       | Opaque account identifier. |
| `symbol_type`            | No       | Identifier type name matching the IdentifierType enum. See allowed values below. |
| `symbol`                 | No       | Identifier value (e.g. "AAPL", "US0378331005", "AAPL  240119C00185000"). Required when `symbol_type` is present. |
| `exchange_type`          | No       | Exchange code system: `MIC` (ISO 10383) or `OPENFIGI` (Bloomberg exchange code). Required when `exchange` is present. |
| `exchange`               | No       | Exchange code value (e.g. "XNAS" for MIC, "US" for OPENFIGI). Populates the domain field on the identifier hint. Required when `exchange_type` is present. |
| `group_ref`              | No       | Opaque grouping key. Rows sharing a non-empty value are postings of one economic event. See [Transaction groups](#transaction-groups). |
| `account_type`           | No       | What kind of leg the row is. Defaults to `USER`. See allowed values below. |

### Transaction groups

Each row is a **posting**: a signed amount of one commodity in one account (see [postings.md](postings.md)). The postings of a single economic event -- a trade and the cash that paid for it -- are grouped by giving them the same `group_ref`. A row with no `group_ref` is its own single-posting group.

`group_ref` is opaque and scoped to one upload. Any value works as long as it is distinct per event within the file; a broker's own order or reference number is the natural choice. It is not stored and carries no meaning across uploads, so re-uploading a period produces new groups.

Grouping is the converter's job. The server persists what it is given: it does not infer a missing leg, pair rows, or fold a fee into a cash amount (see adr/0021-converters-own-transaction-grouping.md).

**Fees are postings, not a column.** A commission, levy or duty is a row with `type=INVEXPENSE` and a negative `quantity` in the settlement currency, paired with an `account_type=EXPENSE` row for the same money. Put the pair in the trade's group when the broker charges it as part of the trade; give it a group of its own when the broker reports it as a separate cash event on its own date. Where a broker folds the commission into a single cash total, the converter splits that total into a consideration row and a fee row rather than posting it as one (see adr/0025-netted-cash-totals-are-split-into-legs.md).

A group whose postings do not sum to zero is accepted, not rejected. The server routes whatever is left over to an `IMBALANCE` posting -- or `TRANSFER_CLEARING` for a journal -- so the residual is made visible rather than silently absorbed. See [postings.md](postings.md#balancing).

### Transaction types (type column)

Allowed values for `type` (OFX-style):
`BUYDEBT`, `BUYFUTURE`, `BUYMF`, `BUYOPT`, `BUYOTHER`, `BUYSTOCK`,
`SELLDEBT`, `SELLFUTURE`, `SELLMF`, `SELLOPT`, `SELLOTHER`, `SELLSTOCK`,
`INCOME`, `INVEXPENSE`, `REINVEST`, `RETOFCAP`, `SPLIT`, `TRANSFER`,
`JRNLFUND`, `JRNLSEC`, `MARGININTEREST`, `CLOSUREOPT`, `CASHFLOW`.

### Account types (account_type column)

Allowed values for `account_type`:
`USER`, `EQUITY`, `INCOME`, `EXPENSE`, `IMBALANCE`, `TRANSFER_CLEARING`.

An absent column or an empty cell means `USER`, so an ordinary export needs no such
column. An unrecognised value is a row error rather than a silent fall back to `USER`.

`account_type` is what makes a one-sided event balance: the income side of a dividend or
the expense side of a charge is a second row in the same `group_ref`, carrying the same
`broker` and `account` as the cash row it balances. It is distinct from `type`, which
records what the broker called the event -- a dividend's cash row and its income row are
both `type=INCOME`, and differ by `account_type`. See [postings.md](postings.md#account-types).

### Identifier hints

Each row carries at most one identifier hint via `symbol_type` and `symbol`. Commonly used symbol types:

| symbol_type        | Description | Example symbol |
| ------------------ | ----------- | -------------- |
| `MIC_TICKER`       | Ticker symbol (use with `exchange_type=MIC`) | `AAPL` |
| `OPENFIGI_TICKER`  | OpenFIGI ticker (use with `exchange_type=OPENFIGI`) | `AAPL` |
| `ISIN`             | International Securities Identification Number | `US0378331005` |
| `CUSIP`            | CUSIP identifier | `037833100` |
| `SEDOL`            | SEDOL identifier | `2046251` |
| `OCC`              | OCC option symbol | `AAPL  240119C00185000` |
| `OPENFIGI_SHARE_CLASS` | OpenFIGI share-class FIGI | `BBG001S5N8V8` |

All IdentifierType enum values are accepted: `ISIN`, `CUSIP`, `SEDOL`, `CINS`, `WERTPAPIER`, `OCC`, `OPRA`, `FUT_OPT`, `OPENFIGI_GLOBAL`, `OPENFIGI_SHARE_CLASS`, `OPENFIGI_COMPOSITE`, `BROKER_DESCRIPTION`, `CURRENCY`, `FX_PAIR`, `MIC_TICKER`, `OPENFIGI_TICKER`.

The optional `exchange_type` and `exchange` columns provide a domain for resolution. They must both be present or both absent. For options, when no `symbol_type`/`symbol` hint is supplied, the system may extract an OCC symbol from the instrument description so that option contracts can be resolved via OpenFIGI OCC_SYMBOL.

### Example

```csv
date,instrument_description,type,quantity,trading_currency,unit_price,account,symbol_type,symbol,exchange_type,exchange
2024-01-15,AAPL - Apple Inc.,BUYSTOCK,10,USD,185.50,ACC-1,MIC_TICKER,AAPL,MIC,XNAS
2024-01-16,MSFT Option,BUYOPT,1,USD,12.50,ACC-1,OCC,MSFT  250117P00385000,,
```

A dividend as a balanced group: the cash arriving in the account, and the income it came
from. Both rows carry the same `account` and `group_ref`; only `account_type` differs.

```csv
date,instrument_description,type,quantity,settlement_currency,account,group_ref,account_type
2024-02-01,USD,INCOME,23.40,USD,ACC-1,div-8842,
2024-02-01,USD,INCOME,-23.40,USD,ACC-1,div-8842,INCOME
```

A trade whose broker charged 11.54 of commission and reported a single cash total
of -23092.22. The commission is split out of that total, so the two rows in the
user's own account still sum to what the broker reported and the third names the
expense it went to.

```csv
date,instrument_description,type,quantity,settlement_currency,unit_price,account,group_ref,account_type
2024-03-04,VUSA,BUYSTOCK,378,GBP,61.06,ACC-1,ord-4471,
2024-03-04,GBP,CASHFLOW,-23080.68,GBP,1,ACC-1,ord-4471,
2024-03-04,GBP,INVEXPENSE,-11.54,GBP,1,ACC-1,ord-4471,
2024-03-04,GBP,INVEXPENSE,11.54,GBP,1,ACC-1,ord-4471,EXPENSE
```

Any extra columns are ignored. Empty optional fields can be omitted or left blank.

### Comment lines

Lines beginning with `#` are metadata or commentary and are not parsed as rows. One key is recognised:

    # share_count_basis=2026-07-29

It declares the share count the file's quantities and unit prices are denominated in. Omit it for an ordinary export, where each row reflects the splits that happened before its own transaction date and nothing after -- the as-traded convention the server assumes. Set it only when the source has post-adjusted historical rows for splits that happened *after* the transaction, in which case it is the date those quantities are current as of. See [bitemporality.md](bitemporality.md#share-count-basis).

## Price CSV

A CSV of EOD prices imported via the `ImportPrices` API, and the format `ExportPrices` writes.

A file holds only the days its source actually reported. Its [coverage declarations](#coverage-declarations) say which ranges were answered for, which the rows cannot: a declared range holding no rows records that a provider was asked and had nothing. Dropping them loses that, and leaves valuation treating the days between reported bars as unpriced.

### Columns

Header names are case-insensitive. Column order is not significant.

| Column | Required | Description |
| ------ | -------- | ----------- |
| `identifier_type` | Yes | Identifier type used to resolve the instrument. Any IdentifierType enum value. |
| `identifier_value` | Yes | Identifier value (e.g. `AAPL`, `NVDA250620P00110000`). |
| `identifier_domain` | No | Domain for the identifier: MIC for `MIC_TICKER`, exchange code for `OPENFIGI_TICKER`, source for `BROKER_DESCRIPTION`, empty otherwise. |
| `price_date` | Yes | `YYYY-MM-DD` trading date. |
| `open` | No | Opening price. |
| `high` | No | High price. |
| `low` | No | Low price. |
| `close` | Yes | Closing price, as the source supplied it. |
| `adjusted_close` | No | The provider's own adjusted close. Never an input to valuation; it exists to cross-check the value PortfolioDB derives. See [prices.md](prices.md). |
| `volume` | No | Integer trading volume. |
| `asset_class` | No | Security type hint used to route identifier plugins when the instrument is unknown. |
| `currency` | No | ISO 4217 currency code, used as a validation hint. |

Prices are stored as supplied. `split_adjusted_close` is derived by PortfolioDB from `close` and the known splits, and is what performance math uses.

### Comment lines

Lines beginning with `#` are metadata or commentary and are not parsed as rows. Two keys are recognised:

    # exported_at=2024-05-01T00:00:00.000Z
    # coverage=2022-01-01,2025-07-07

`exported_at` is knowledge time: when the supplied data was current. OCC symbols are split-adjusted to this point during instrument resolution, and imported rows record it as `last_fetched_at`. It does **not** say which share count the prices are denominated in. See [bitemporality.md](bitemporality.md#knowledge-time).

`coverage` is described under [Coverage declarations](#coverage-declarations).

### Example

```csv
# exported_at=2026-07-30T00:00:00.000Z
# coverage=2022-01-01,2025-07-07
# coverage=MIC_TICKER,ATVI,XNAS,2021-12-31,2023-10-14
identifier_type,identifier_value,identifier_domain,price_date,open,high,low,close,adjusted_close,volume,asset_class,currency
MIC_TICKER,AAPL,XNAS,2024-01-15,,,,185.9,,,STOCK,USD
OCC,NVDA250620P00110000,,2024-06-11,,,,13.42,,,OPTION,USD
```

## Corporate event JSON

Stock splits are imported via the `ImportCorporateEvents` API as JSON, and `ExportCorporateEvents` writes the same shape. Cash dividends are part of the API but are not yet carried by this file format.

Coverage is stored per (instrument, plugin) for both prices and corporate events, but an import records every span against the `import` sentinel, so both exports merge spans across plugins rather than preserving a distinction that cannot survive the round trip.

The canonical shape is an object with an `events` array and an optional `coverage` array. A bare array is accepted as events-only.

### Event objects

| Key | Required | Description |
| --- | -------- | ----------- |
| `identifier_type` | Yes | Identifier type used to resolve the instrument (`MIC_TICKER`, `OPENFIGI_TICKER`, `ISIN`, etc.). |
| `identifier_value` | Yes | Identifier value (e.g. `AAPL`, `US0378331005`). |
| `identifier_domain` | No | Domain for the identifier (MIC for `MIC_TICKER`, exchange code for `OPENFIGI_TICKER`). |
| `asset_class` | No | `STOCK` or `ETF`. Used as the security type hint when the instrument is unknown and identifier plugins must resolve it. |
| `ex_date` | Yes | `YYYY-MM-DD`. Effective/execution date. This is valid time -- when the split took effect, not when it was announced or learned of (see [bitemporality.md](bitemporality.md)). |
| `split_from` | Yes | Decimal numerator of the pre-split ratio (e.g. `1` for a 2:1 split). |
| `split_to` | Yes | Decimal numerator of the post-split ratio (e.g. `2` for a 2:1 split). The factor is `split_to / split_from`. |
| `first_known_at` | No | ISO 8601 instant: when the exporting instance first learned of the split. Omit and the server falls back to the request's `exported_at`, then to storage time. A stored value only ever moves backwards. |

When the importer sees an unknown `(identifier_type, identifier_domain, identifier_value)` triple, it routes through the same identifier plugin flow used by price imports: the supplied `asset_class` becomes the security-type hint and the resolved instrument is created with the supplied identifier as canonical.

### Example

```json
{
  "events": [
    { "identifier_type": "MIC_TICKER", "identifier_domain": "XNAS", "identifier_value": "AAPL",
      "asset_class": "STOCK", "ex_date": "2020-08-31", "split_from": "1", "split_to": "4" },
    { "identifier_type": "MIC_TICKER", "identifier_domain": "XNAS", "identifier_value": "TSLA",
      "asset_class": "STOCK", "ex_date": "2022-08-25", "split_from": "1", "split_to": "3" }
  ],
  "coverage": [
    { "from": "2022-01-01", "before": "2026-07-30" }
  ]
}
```

## Coverage declarations

A coverage declaration records that the caller has authoritative coverage of a date interval. Both the price CSV and the corporate event JSON accept them, with the same semantics and the same fields; only the syntax differs.

Both store the declaration, tagged as coming from an import so the background fetcher does not re-query the same interval from a plugin and cannot overwrite hand-curated data with provider data.

- **Prices** -- a `price_coverage` row. Valuation carries the last close forward across the non-trading days inside the interval, so a file can carry only the days its source actually moved on. Without a declaration each row covers only its own date, so any gap between rows stays a gap.
- **Corporate events** -- a `corporate_event_coverage` row.

In both cases a declared interval containing nothing is meaningful, and is the only way a file can say the caller asked about those dates and there was nothing to report. See adr/0023-price-coverage-is-stored-not-inferred.md.

### Fields

| Field | Required | Description |
| ----- | -------- | ----------- |
| `identifier_type` | No | Identifier type. Present together with `identifier_value` to name one instrument; absent to declare a file-wide default. |
| `identifier_value` | No | Identifier value. See above. |
| `identifier_domain` | No | Domain for the identifier. Only meaningful alongside the other two. |
| `from` | Yes | `YYYY-MM-DD`, inclusive. |
| `before` | Yes | `YYYY-MM-DD`, exclusive; must be after `from`. To cover through 31 December 2024, write `2025-01-01`. |

The interval is half-open `[from, before)`, matching every other date interval on the wire (see [bitemporality.md](bitemporality.md) and adr/0018-half-open-date-intervals.md).

### Global and specific declarations

A declaration carrying no identifier at all is **global**: it applies to every instrument the file names. A declaration carrying one is **specific** to that instrument.

- At most one global declaration per file.
- A specific declaration **overrides** the global for its instrument rather than adding to it. An instrument with any specific declaration takes none of the global.
- Several specific declarations for one instrument are all applied, so an instrument can carry more than one interval.
- A partly-written identifier -- a type with no value, or a domain alone -- is an error rather than a global declaration.

Most files need one global and a handful of exceptions: instruments that started or stopped trading partway through the period the file covers. That is what the export writes -- the span most instruments share as the global, then the exceptions.

Two cases the export always writes out in full, both following from the override rule above. An instrument carrying more than one span writes all of them, since a specific declaration replaces the global rather than adding to it. An instrument that is covered but has no rows in the file also writes its own, since the global is expanded against the instruments the file names and would never reach it.

### Syntax

In the **price CSV**, a comment line in one of two forms, told apart by field count. The five-field form's order matches the data columns.

    # coverage=<from>,<before>
    # coverage=<identifier_type>,<identifier_value>,<identifier_domain>,<from>,<before>

In the **corporate event JSON**, an entry in the top-level `coverage` array. Identifier keys are omitted or empty for a global declaration.

```json
"coverage": [
  { "from": "2022-01-01", "before": "2026-07-30" },
  { "identifier_type": "MIC_TICKER", "identifier_value": "ATVI",
    "identifier_domain": "XNAS", "from": "2021-12-31", "before": "2023-10-14" }
]
```
