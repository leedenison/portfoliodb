# Archives preserve inputs, not derived state

An archive exists to rebuild an instance on a new server without losing data and
without re-running expensive external operations -- identifier lookup above all. It
is therefore neither a general interchange format nor a full database backup: it
carries what cannot be recovered and what is costly to reacquire, and leaves
everything the server can recompute to be recomputed on restore.

**Tier 1, irreplaceable.** External or human input that exists nowhere else: `txs`,
`holding_declarations`, `portfolios` and `portfolio_filters`,
`users.display_currency`, `plugin_config`, the `reason` on `price_fetch_blocks` and
`corporate_event_fetch_blocks`, `unhandled_corporate_events.resolved`, and
hand-recovered price rows for instruments no provider still carries.

**Tier 2, expensive to reacquire.** Refetchable in principle, but only through paid,
rate-limited or slow external calls: `instruments`, `instrument_identifiers`,
`provider_instrument_identifiers`, `eod_prices` with `price_coverage`,
`stock_splits` with `corporate_event_coverage`, and `inflation_indices`.

**Tier 3, cheap, excluded.** Anything the server derives from tiers 1 and 2:
`split_adjusted_quantity` and `split_adjusted_unit_price`, posting `weight` and
`weight_commodity` ([0024](0024-group-balance-is-checked-on-weight.md)), synthetic
INITIALIZE postings ([0011](0011-synthetic-initialize-transactions.md)), `tx_groups`
and the routed residual postings that balance them
([0043](0043-grouping-does-not-travel-in-the-archive.md)), lots and realised gains
([0031](0031-lots-are-derived-and-unknown-basis-is-a-value.md)),
`identification_errors`, `validation_errors`, `ingestion_jobs`, and computed
holdings and valuations. Exporting these would also invite the round trip to carry a
rounding or mix share counts, which [bitemporality.md](../spec/bitemporality.md)
forbids.

## Consequences

**The tier test is about the cost of rebuilding, not about who produced it.**
Transaction grouping was tier 1 while it was converter output that nothing could
reconstruct. [0041](0041-server-owns-transaction-grouping.md) built the rule, and
grouping is now derived from stored evidence with no external call, which is the
tier 3 test. See [0043](0043-grouping-does-not-travel-in-the-archive.md), which
moves the routed residuals with it and keeps a grouping a person asserted by hand in
tier 1, where an input belongs.

**A rebuild does not exercise every ingest path.** Replaying broker files would, but
replay is not idempotent across versions -- converters change, and identification
would run again at cost. Determinism is chosen over coverage, and re-running the
broker masters stays available as a separate exercise.
