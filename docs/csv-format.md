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
| `exchange_code`          | No       | Bloomberg/OpenFIGI exchange code (e.g. "US"). Pairs with `ticker` to create an OPENFIGI_TICKER identifier hint. |
| `mic`                    | No       | ISO 10383 MIC code (e.g. "XNAS"). Pairs with `ticker` to create a MIC_TICKER identifier hint. |
| `ticker`                 | No       | Ticker symbol. Creates MIC_TICKER (with `mic` domain) and/or OPENFIGI_TICKER (with `exchange_code` domain). If neither `mic` nor `exchange_code` is present, creates MIC_TICKER with empty domain. |
| `isin`                   | No       | ISIN identifier hint for resolution. |
| `openfigi_share_class`   | No       | OpenFIGI share-class identifier hint. |
| `occ`                    | No       | OCC identifier hint (options). |

## Transaction types (type column)

Allowed values for `type` (OFX-style):
`BUYDEBT`, `BUYFUTURE`, `BUYMF`, `BUYOPT`, `BUYOTHER`, `BUYSTOCK`,
`SELLDEBT`, `SELLFUTURE`, `SELLMF`, `SELLOPT`, `SELLOTHER`, `SELLSTOCK`,
`INCOME`, `INVEXPENSE`, `REINVEST`, `RETOFCAP`, `SPLIT`, `TRANSFER`,
`JRNLFUND`, `JRNLSEC`, `MARGININTEREST`, `CLOSUREOPT`, `CASHFLOW`.

## Identifier hints

When identifier hint columns (`exchange_code`, `mic`, `ticker`, `isin`, `openfigi_share_class`, `occ`) are present and non-empty, resolution can use them and may not store broker description on the instrument. Empty cells are ignored. For options, when no `occ` hint is supplied, the system may extract an OCC symbol from the instrument description so that option contracts can be resolved via OpenFIGI OCC_SYMBOL.

The `ticker` column interacts with `exchange_code` and `mic`:
- `exchange_code` + `ticker` creates an OPENFIGI_TICKER identifier hint (domain = exchange code)
- `mic` + `ticker` creates a MIC_TICKER identifier hint (domain = MIC)
- `ticker` alone creates a MIC_TICKER identifier hint with empty domain
- If both `exchange_code` and `mic` are present, both identifier hints are created

## Example

```csv
date,instrument_description,type,quantity,trading_currency,settlement_currency,unit_price,account,exchange_code,ticker,isin
2024-01-15,AAPL - Apple Inc.,BUYSTOCK,10,USD,GBP,185.50,ACC-1,US,AAPL,US0378331005
2024-01-16,MSFT - Microsoft Corp.,SELLSTOCK,-5,USD,GBP,398.20,,,,
```

Any extra columns are ignored. Empty optional fields can be omitted or left blank.
