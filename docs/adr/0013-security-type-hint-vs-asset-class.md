# Security type hint is routing-only, distinct from canonical asset class

The ingestion layer derives a security type hint from a transaction's TxType and
passes it to plugins for routing only (e.g. so the cash plugin only handles CASH).
The hint shares the asset-class vocabulary but is not authoritative: TxType cannot
distinguish a stock from an ETF, so stock-like types (BUYSTOCK, SELLSTOCK) map to
STOCK and never ETF. The canonical asset class stored on an instrument is always
determined by the identifier plugins during resolution, not by the hint. Keeping
the routing hint and the canonical asset class as separate layers prevents the
coarse, TxType-derived guess from being mistaken for confirmed identity data.
