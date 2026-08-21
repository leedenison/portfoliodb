# Security type hint is routing-only, distinct from canonical asset class

Amended by [0045](0045-tx-type-does-not-encode-asset-class.md): the hint is
stated on the posting rather than derived from the transaction type. Only where
the coarse value comes from changes.

A security type hint is passed to plugins for routing only (e.g. so the cash
plugin only handles CASH). It shares the asset-class vocabulary but is not
authoritative: it cannot distinguish a stock from an ETF, so stock-like rows map
to STOCK and never ETF. The canonical asset class stored on an instrument is
always determined by the identifier plugins during resolution. Keeping the
routing hint and the canonical asset class as separate layers prevents the coarse
guess from being mistaken for confirmed identity data.
