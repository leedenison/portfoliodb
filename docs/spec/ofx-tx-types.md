# OFX Transaction Types

PortfolioDB uses OFX-style transaction types to classify investment transactions. This document describes each type and the assumptions the system makes about it during ingestion and resolution.

## Transaction Type Reference

| TxType | Category | InstrumentKind | AssetClass | Stored |
|---|---|---|---|---|
| BUYSTOCK | Buy | SECURITY | STOCK | Yes |
| SELLSTOCK | Sell | SECURITY | STOCK | Yes |
| BUYDEBT | Buy | SECURITY | FIXED_INCOME | Yes |
| SELLDEBT | Sell | SECURITY | FIXED_INCOME | Yes |
| BUYMF | Buy | SECURITY | MUTUAL_FUND | Yes |
| SELLMF | Sell | SECURITY | MUTUAL_FUND | Yes |
| BUYOPT | Buy | SECURITY | OPTION | Yes |
| SELLOPT | Sell | SECURITY | OPTION | Yes |
| CLOSUREOPT | Other | SECURITY | OPTION | Yes |
| BUYFUTURE | Buy | SECURITY | FUTURE | Yes |
| SELLFUTURE | Sell | SECURITY | FUTURE | Yes |
| BUYOTHER | Buy | SECURITY | UNKNOWN | Yes |
| SELLOTHER | Sell | SECURITY | UNKNOWN | Yes |
| INCOME | Other | CASH | CASH | Yes |
| INVEXPENSE | Other | CASH | CASH | Yes |
| MARGININTEREST | Other | CASH | CASH | Yes |
| RETOFCAP | Other | CASH | CASH | Yes |
| JRNLFUND | Other | CASH | CASH | Yes |
| CASHFLOW | Other | CASH | CASH | Yes |
| TRANSFER | Other | SECURITY | UNKNOWN | Yes |
| REINVEST | Other | SECURITY | UNKNOWN | Yes |
| JRNLSEC | Other | SECURITY | UNKNOWN | Yes |
| SPLIT | Other | SECURITY | UNKNOWN | No |

## Key Rules

### INCOME is cash-only

INCOME transactions have InstrumentKind=CASH and resolve to a currency instrument (e.g. GBP), not the security that generated the income. The instrument_description may reference the source security (e.g. "INVESCO EQQQ NASDAQ 100 UCITS ETF") but the transaction itself represents a cash movement (dividend, distribution, etc.). Only the cash description and identifier plugins are consulted during resolution.

### TRANSFER is security-only

TRANSFER transactions have InstrumentKind=SECURITY. They represent security position transfers between accounts and are never used for cash movements. The asset class is UNKNOWN and is determined during identification by the identifier plugins.

### REINVEST is security with unknown asset class

REINVEST transactions represent reinvested dividends (e.g. dividend paid in additional shares). They have InstrumentKind=SECURITY but asset class UNKNOWN, since the actual asset class is inferred during identification.

### JRNLFUND vs JRNLSEC

JRNLFUND is a cash journal entry (InstrumentKind=CASH, AssetClass=CASH). JRNLSEC is a security journal entry (InstrumentKind=SECURITY, AssetClass=UNKNOWN). The actual asset class of JRNLSEC transactions is determined during identification.

### SPLIT is not stored

SPLIT is the only transaction type that is dropped before resolution. No resolution or database insert is performed.

### CLOSUREOPT

CLOSUREOPT represents option closures (expiration, assignment, exercise). It has AssetClass=OPTION.

### CASHFLOW

CASHFLOW represents any inflow or outflow of cash. This includes but is not limited to the cash legs of security trades. When a broker reports a trade as a single row containing both the security quantity change and the cash amount, uploaders may split this into two transactions: the security transaction (e.g. BUYSTOCK) and a CASHFLOW transaction for the corresponding cash movement.

## InstrumentKind and Plugin Routing

InstrumentKind is a coarse classification (CASH or SECURITY) derived from TxType. It controls which plugins are consulted during instrument resolution:

- **CASH**: Only cash description and identifier plugins run. These extract a currency code from the transaction's trading_currency and resolve to a seeded currency instrument.
- **SECURITY**: Only security description and identifier plugins run (OpenFIGI, EODHD, Massive, etc.). These identify the security and store canonical identifiers.

## Security Type Hint

The security type hint is derived from TxType and uses the same vocabulary as asset class (STOCK, ETF, FIXED_INCOME, MUTUAL_FUND, OPTION, FUTURE, CASH, UNKNOWN). It is passed to plugins for routing only. TxType cannot distinguish stock from ETF, so stock-like types (BUYSTOCK, SELLSTOCK) map to STOCK, never ETF. The canonical asset class stored on the instrument is always determined by identifier plugins, not the hint.

## Source Code

The mappings documented here are implemented in:

- `server/db/db.go`: `TxTypeToAssetClass`, `TxTypeToInstrumentKind`
- `server/service/ingestion/hints.go`: `TxTypeStored`, `HintsFromTx`
