# Upload transactions UI

Uploads are **user-level** (transactions are associated with the user, not a portfolio). The user reaches the upload flow from the Holdings page.

## Flow

1. **Step 1 — Select broker**  
   The user selects the broker for the transactions (e.g. IBKR, Charles Schwab). No source or period input is shown.

2. **Step 2 — Format and file**  
   The user chooses between broker-specific options (if any) and **Standard** format. For the initial implementation only **Standard** is available.  
   Then the user selects a CSV file. The client parses the file; if there are parse errors, they are shown and upload is disabled. If parsing succeeds, a summary (row count, date range) is shown.  
   **Period** (from/to) is derived from the CSV data only (min and max transaction dates); the user does not enter period.  
   **Source** is not shown; the client derives it (e.g. `{Broker}:web:standard`) and sends it with the request.

3. **Submit and job status**  
   On Upload, the client sends the transactions via the bulk ingestion API and receives a job id. The UI polls job status until the job completes.  
   - **PENDING / RUNNING**: Show “Processing…”.  
   - **SUCCESS**: Message and link back to Holdings.  
   - **FAILED**: Show validation errors and identification errors (row index, field or instrument description, message) so the user can fix the CSV and try again.

## Access

- **Upload flow**: From the Holdings page (`/holdings`), an "Upload transactions" button goes to `/upload`.
- **Upload history**: From the user menu dropdown in the top navigation bar, an "Uploads" link goes to `/uploads`.  This page shows a paginated list of past uploads with their status and any errors.
- **Auth**: The user must be signed in.

---

# Standard CSV format

The **Standard** format is a CSV that directly represents the transaction fields expected by the API. Users can produce this CSV manually or use a broker-specific converter (when available) that outputs Standard format.

## Columns

Header names are case-insensitive. Supported column names:

| Column                   | Required | Description |
| ------------------------ | -------- | ----------- |
| `date` or `timestamp`    | Yes      | Transaction date/time. ISO 8601 (e.g. `2024-01-15` or `2024-01-15T14:30:00Z`) or `YYYY-MM-DD`. |
| `instrument_description`| Yes      | Broker’s instrument description (e.g. symbol, name, or broker-specific text). |
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
`JRNLFUND`, `JRNLSEC`, `MARGININTEREST`, `CLOSUREOPT`.

## Identifier hints

When identifier hint columns (`isin`, `ticker`, `openfigi_share_class`, `occ`) are present and non-empty, resolution can use them (e.g. ISIN/FIGI/TICKER) and may not store broker description on the instrument. Empty cells are ignored. For options, when no `occ` hint is supplied, the system may extract an OCC symbol from the instrument description so that option contracts can be resolved via OpenFIGI OCC_SYMBOL.

## Example

```csv
date,instrument_description,type,quantity,trading_currency,settlement_currency,unit_price,account,exchange_code_hint,isin
2024-01-15,AAPL - Apple Inc.,BUYSTOCK,10,USD,GBP,185.50,ACC-1,US,US0378331005
2024-01-16,MSFT - Microsoft Corp.,SELLSTOCK,-5,USD,GBP,398.20,,,
```

Any extra columns are ignored. Empty optional fields can be omitted or left blank.
