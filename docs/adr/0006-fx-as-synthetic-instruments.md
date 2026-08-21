# FX rates as synthetic instruments with USD-pivot storage

Foreign-exchange rates are modelled as synthetic instruments (`asset_class =
'FX'`, identifier type `FX_PAIR`) whose daily rates live in `eod_prices`. This
lets the entire existing price-cache pipeline -- gap detection, coverage tracking,
gapfilling, plugin fetching, and the range utilities -- work on FX data without
modification: an FX pair is just another instrument with prices, and a price
plugin like Massive gains FX support by adding `'FX'`/`'FX_PAIR'` to the asset
classes and identifier types it accepts. The alternative, a bespoke FX rate table
with its own fetch/cache/gap machinery, would duplicate a large amount of tested
logic.

All FX pairs are stored with USD as the quote currency (BASE/USD), so only one
rate per foreign currency is stored regardless of how many display currencies
users pick. Non-USD conversions are computed as cross-rates from two stored
USD-quoted pairs (e.g. GBP shown in EUR uses `GBPUSD / EURUSD`). Pivoting on USD
keeps the stored data O(currencies) rather than O(currencies^2) and means adding
a new display currency requires no new stored pairs.
