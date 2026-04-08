# Standard CSV format

The **Standard** format is a CSV that directly represents the transaction fields expected by the API. Users can produce this CSV manually or use a broker-specific converter (when available) that outputs Standard format.

## Columns

Header names are case-insensitive. Supported column names:

| Column                   | Required | Description |
| ------------------------ | -------- | ----------- |
| `date` or `timestamp`    | Yes      | Transaction date/time. ISO 8601 (e.g. `2024-01-15` or `2024-01-15T14:30:00Z`) or `YYYY-MM-DD`. |
| `instrument_description`| Yes      | Broker's instrument description (e.g. symbol, name, or broker-specific text). |
| `type`                  | Yes      | OFX-style transaction type: see allowed values below. |
| `quantity`              | Yes      | Signed number: positive for buys/adds, negative for sells/reductions. |
| `trading_currency`       | No       | Instrument trading currency (e.g. EUR); used as plugin hint. |
| `settlement_currency`    | No       | Settlement/payment currency (e.g. GBP). |
| `unit_price`             | No       | Unit price as reported by broker (optional). |
| `account`                | No       | Opaque account identifier. |
| `symbol_type`            | No       | Identifier type name matching the IdentifierType enum. See allowed values below. |
| `symbol`                 | No       | Identifier value (e.g. "AAPL", "US0378331005", "AAPL  240119C00185000"). Required when `symbol_type` is present. |
| `exchange_type`          | No       | Exchange code system: `MIC` (ISO 10383) or `OPENFIGI` (Bloomberg exchange code). Required when `exchange` is present. |
| `exchange`               | No       | Exchange code value (e.g. "XNAS" for MIC, "US" for OPENFIGI). Populates the domain field on the identifier hint. Required when `exchange_type` is present. |

## Transaction types (type column)

Allowed values for `type` (OFX-style):
`BUYDEBT`, `BUYFUTURE`, `BUYMF`, `BUYOPT`, `BUYOTHER`, `BUYSTOCK`,
`SELLDEBT`, `SELLFUTURE`, `SELLMF`, `SELLOPT`, `SELLOTHER`, `SELLSTOCK`,
`INCOME`, `INVEXPENSE`, `REINVEST`, `RETOFCAP`, `SPLIT`, `TRANSFER`,
`JRNLFUND`, `JRNLSEC`, `MARGININTEREST`, `CLOSUREOPT`, `CASHFLOW`.

## Identifier hints

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

## Example

```csv
date,instrument_description,type,quantity,trading_currency,unit_price,account,symbol_type,symbol,exchange_type,exchange
2024-01-15,AAPL - Apple Inc.,BUYSTOCK,10,USD,185.50,ACC-1,MIC_TICKER,AAPL,MIC,XNAS
2024-01-16,MSFT Option,BUYOPT,1,USD,12.50,ACC-1,OCC,MSFT  250117P00385000,,
```

Any extra columns are ignored. Empty optional fields can be omitted or left blank.

# Corporate event CSV format

A separate CSV is used to import stock splits and cash dividends via the `ImportCorporateEvents` API. Splits and dividends share one file; the `event` column distinguishes them.

## Columns

| Column | Required | Description |
| ------ | -------- | ----------- |
| `event` | Yes | `split` or `dividend`. Determines which event-specific columns are read. |
| `identifier_type` | Yes | Identifier type used to resolve the instrument (`MIC_TICKER`, `OPENFIGI_TICKER`, `ISIN`, etc.). |
| `identifier_value` | Yes | Identifier value (e.g. `AAPL`, `US0378331005`). |
| `identifier_domain` | No | Domain for the identifier (MIC for `MIC_TICKER`, exchange code for `OPENFIGI_TICKER`). |
| `asset_class` | No | `STOCK` or `ETF`. Used as the security type hint when the instrument is unknown and identifier plugins must resolve it. |
| `ex_date` | Yes | `YYYY-MM-DD`. Effective/execution date for splits, ex-dividend date for dividends. |
| `split_from` | Splits only | Decimal numerator of the pre-split ratio (e.g. `1` for a 2:1 split). |
| `split_to` | Splits only | Decimal numerator of the post-split ratio (e.g. `2` for a 2:1 split). The factor is `split_to / split_from`. |
| `amount` | Dividends only | Cash amount per share, denominated in `currency`. |
| `currency` | Dividends only | ISO 4217 currency of the cash dividend. |
| `pay_date` | No | `YYYY-MM-DD` payment date (dividends). |
| `record_date` | No | `YYYY-MM-DD` record date (dividends). |
| `declaration_date` | No | `YYYY-MM-DD` declaration date (dividends). |
| `frequency` | No | `annual`, `semi-annual`, `quarterly`, `monthly`, or empty (dividends). |

## Coverage rows

The importer also accepts coverage declarations in a separate CSV (or section). Each row records that the caller has authoritative coverage for the closed `[from, to]` interval; the server stores a `corporate_event_coverage` row tagged `data_provider = "import"` so the background fetcher does not refetch the same range from a plugin.

| Column | Required | Description |
| ------ | -------- | ----------- |
| `identifier_type` | Yes | As above. |
| `identifier_value` | Yes | As above. |
| `identifier_domain` | No | As above. |
| `from` | Yes | `YYYY-MM-DD` inclusive. |
| `to` | Yes | `YYYY-MM-DD` inclusive. |

## Example

```csv
event,identifier_type,identifier_domain,identifier_value,asset_class,ex_date,split_from,split_to,amount,currency,pay_date,record_date,declaration_date,frequency
split,MIC_TICKER,XNAS,AAPL,STOCK,2020-08-31,1,4,,,,,,
split,MIC_TICKER,XNAS,AAPL,STOCK,2014-06-09,1,7,,,,,,
dividend,MIC_TICKER,XNAS,AAPL,STOCK,2024-02-09,,,0.24,USD,2024-02-15,2024-02-12,2024-02-01,quarterly
```

When the importer sees an unknown `(identifier_type, identifier_domain, identifier_value)` triple, it routes through the same identifier plugin flow used by price imports: the supplied `asset_class` becomes the security-type hint and the resolved instrument is created with the supplied identifier as canonical.
