# Corporate events: admin-owned, per-instrument fetch, daily scheduler

Corporate events (stock splits, cash dividends) are admin-owned shared reference
data and are never derived from user transactions: the ingestion path continues
to drop `TX_TYPE=SPLIT` rows. Deriving events from user statements would produce
inconsistent, per-user event data for what is fundamentally shared market data;
broker-parsed splits instead flow through the `ImportCorporateEvents` admin path.

Fetching is **per-instrument, not bulk-by-exchange**, because both providers
(Massive, EODHD) support per-symbol filtering, making a per-ticker loop the
natural, provider-symmetric fit. A plugin returning an **empty** result is
treated as authoritative coverage ("nothing happened" is the normal answer for
most ticker/date windows), so we do not re-query covered ranges. A 30-day
lookahead is fetched for dividends only, to hold an upcoming-dividends calendar;
future-dated splits are ignored by the recompute until their ex_date passes.

A daily fixed-cadence scheduler (planned) is needed because nothing otherwise
fires on the day a stored future-dated split's ex_date crosses, and newly
announced events would go unseen until an admin manually triggers a fetch. We
chose fixed-cadence over smart "next-event-date" scheduling, admin-UI cron
configuration, and startup backfill: the simple model is sufficient for v1, and
because gaps are computed against `current_date`, a missed day is caught by the
next tick without special catch-up logic. Its config lives in a top-level
scheduler config, not any individual plugin's JSON.

Split adjustment is scoped to STOCK and ETF; options need underlying-driven
adjustment of strike and contract terms, deferred as separate work. Cash
dividends are stored for calendar/reporting use but **not** applied to
`split_adjusted_*` prices: raw close prices already answer "what would I get by
selling now", and broker-imported INCOME/REINVEST transactions already capture
the cash side, so a dividend-adjusted price view would be redundant.
