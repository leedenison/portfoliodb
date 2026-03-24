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
| `exchange_code_hint` or `exchange` | No | Exchange code (e.g. "US"). Used only for resolution; not stored as canonical. |
| `mic_hint` or `mic`       | No       | Market identifier code. Used only for resolution; not stored as canonical. |
| `isin`                   | No       | ISIN identifier hint for resolution. |
| `ticker`                 | No       | Ticker identifier hint; optional `ticker_exchange` or `ticker_domain` for TICKER domain (e.g. "US"). |
| `openfigi_share_class`   | No       | OpenFIGI share-class identifier hint. |
| `occ`                    | No       | OCC identifier hint (options). |

## Transaction types (type column)

Allowed values for `type` (OFX-style):
`BUYDEBT`, `BUYMF`, `BUYOPT`, `BUYOTHER`, `BUYSTOCK`,
`SELLDEBT`, `SELLMF`, `SELLOPT`, `SELLOTHER`, `SELLSTOCK`,
`INCOME`, `INVEXPENSE`, `REINVEST`, `RETOFCAP`, `SPLIT`, `TRANSFER`,
`JRNLFUND`, `JRNLSEC`, `MARGININTEREST`, `CLOSUREOPT`, `CASHFLOW`.

## Identifier hints

When identifier hint columns (`isin`, `ticker`, `openfigi_share_class`, `occ`) are present and non-empty, resolution can use them (e.g. ISIN/FIGI/TICKER) and may not store broker description on the instrument. Empty cells are ignored. For options, when no `occ` hint is supplied, the system may extract an OCC symbol from the instrument description so that option contracts can be resolved via OpenFIGI OCC_SYMBOL.

## Example

```csv
date,instrument_description,type,quantity,trading_currency,settlement_currency,unit_price,account,exchange_code_hint,isin
2024-01-15,AAPL - Apple Inc.,BUYSTOCK,10,USD,GBP,185.50,ACC-1,US,US0378331005
2024-01-16,MSFT - Microsoft Corp.,SELLSTOCK,-5,USD,GBP,398.20,,,
```

Any extra columns are ignored. Empty optional fields can be omitted or left blank.
