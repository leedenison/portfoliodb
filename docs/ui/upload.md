# Upload transactions UI

Uploads are scoped to **one portfolio**. The user reaches the upload flow from that portfolio’s holdings page.

## Flow

1. **Step 1 — Select broker**  
   The user selects the broker for the transactions (e.g. IBKR, Charles Schwab). The portfolio is already fixed from the route. No source or period input is shown.

2. **Step 2 — Format and file**  
   The user chooses between broker-specific options (if any) and **Standard** format. For the initial implementation only **Standard** is available.  
   Then the user selects a CSV file. The client parses the file; if there are parse errors, they are shown and upload is disabled. If parsing succeeds, a summary (row count, date range) is shown.  
   **Period** (from/to) is derived from the CSV data only (min and max transaction dates); the user does not enter period.  
   **Source** is not shown; the client derives it (e.g. `{Broker}:web:standard`) and sends it with the request.

3. **Submit and job status**  
   On Upload, the client sends the transactions via the bulk ingestion API and receives a job id. The UI polls job status until the job completes.  
   - **PENDING / RUNNING**: Show “Processing…”.  
   - **SUCCESS**: Message and link back to portfolio holdings.  
   - **FAILED**: Show validation errors and identification errors (row index, field or instrument description, message) so the user can fix the CSV and try again.

## Access

- **Entry point**: From the portfolio holdings page (`/portfolios/{id}`), an “Upload transactions” link goes to `/portfolios/{id}/upload`.
- **Auth**: Same as the holdings page; the user must be signed in.

---

# Standard CSV format

The **Standard** format is a CSV that directly represents the transaction fields expected by the API. Users can produce this CSV manually or use a broker-specific converter (when available) that outputs Standard format.

## Columns

Header names are case-insensitive. Supported column names:

| Column                  | Required | Description |
| ----------------------- | -------- | ----------- |
| `date` or `timestamp`   | Yes      | Transaction date/time. ISO 8601 (e.g. `2024-01-15` or `2024-01-15T14:30:00Z`) or `YYYY-MM-DD`. |
| `instrument_description`| Yes      | Broker’s instrument description (e.g. symbol, name, or broker-specific text). |
| `type`                  | Yes      | OFX-style transaction type: see allowed values below. |
| `quantity`              | Yes      | Signed number: positive for buys/adds, negative for sells/reductions. |
| `currency`              | No       | Currency code (e.g. USD). |
| `unit_price`            | No       | Unit price as reported by broker (optional). |

## Transaction types (type column)

Allowed values for `type` (OFX-style):  
`BUYDEBT`, `BUYMF`, `BUYOPT`, `BUYOTHER`, `BUYSTOCK`,  
`SELLDEBT`, `SELLMF`, `SELLOPT`, `SELLOTHER`, `SELLSTOCK`,  
`INCOME`, `INVEXPENSE`, `REINVEST`, `RETOFCAP`, `SPLIT`, `TRANSFER`,  
`JRNLFUND`, `JRNLSEC`, `MARGININTEREST`, `CLOSUREOPT`.

## Example

```csv
date,instrument_description,type,quantity,currency,unit_price
2024-01-15,AAPL - Apple Inc.,BUYSTOCK,10,USD,185.50
2024-01-16,MSFT - Microsoft Corp.,SELLSTOCK,-5,USD,398.20
```

Any extra columns are ignored. Empty optional fields can be omitted or left blank.
