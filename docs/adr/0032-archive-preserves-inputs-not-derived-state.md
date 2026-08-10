# Archives preserve inputs, not derived state

An archive exists to rebuild an instance on a new server without losing data and
without re-running expensive external operations -- identifier lookup above all.
It is therefore neither a general interchange format nor a full database backup:
it carries what cannot be recovered and what is costly to reacquire, and leaves
everything the server can recompute to be recomputed on restore.

Data falls into three tiers.

**Tier 1, irreplaceable.** External or human input that exists nowhere else:
`txs`, `holding_declarations`, `portfolios` and `portfolio_filters`,
`ignored_asset_classes` and `users.display_currency`,
`plugin_config`, the `reason` on `price_fetch_blocks` and
`corporate_event_fetch_blocks`, `unhandled_corporate_events.resolved`, and
hand-recovered price rows for instruments no provider still carries.

**Tier 2, expensive to reacquire.** Refetchable in principle, but only through
paid, rate-limited or slow external calls: `instruments`,
`instrument_identifiers`, `provider_instrument_identifiers`, `eod_prices` with
`price_coverage`, `stock_splits` with `corporate_event_coverage`, and
`inflation_indices`.

**Tier 3, cheap, excluded.** Anything the server derives from tiers 1 and 2:
`split_adjusted_quantity` and `split_adjusted_unit_price`, posting `weight` and
`weight_commodity` (adr/0024-group-balance-is-checked-on-weight.md), synthetic
INITIALIZE postings (adr/0011-synthetic-initialize-transactions.md), `tx_groups`
and the routed residual postings that balance them
(adr/0043-grouping-does-not-travel-in-the-archive.md), lots and
realised gains (adr/0031-lots-are-derived-and-unknown-basis-is-a-value.md),
`identification_errors`, `validation_errors`, `ingestion_jobs`, and computed
holdings and valuations. Exporting these would also invite the round trip to
carry a rounding or mix share counts, which docs/spec/bitemporality.md forbids.

## Consequences

**Transaction grouping was tier 1 until a rule existed.** It was converter
output, and adr/0021-converters-own-transaction-grouping.md had the server
explicitly not pairing rows or inferring a missing leg, so nothing could rebuild
it and an archive that dropped it lost the balance invariant, residual
attribution and fee association permanently. That premise is the whole of the
argument, and adr/0041-server-owns-transaction-grouping.md removed it by building
the rule. Grouping is derived from stored evidence, over data the server already
holds and with no external call, which meets the tier 3 test rather than the tier 1
one. See adr/0043-grouping-does-not-travel-in-the-archive.md, which moves the routed
residuals with it and keeps a grouping a person asserted by hand in tier 1, where an
input belongs.

**A rebuild does not exercise every ingest path.** Replaying broker files would,
but replay is not idempotent across versions -- converters change, and
identification would run again at cost. Determinism is chosen over coverage, and
re-running the broker masters stays available as a separate exercise on whatever
schedule is wanted.
