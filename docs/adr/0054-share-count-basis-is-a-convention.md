---
status: superseded by ADR-0056
---

# Share count basis is a convention, not a field

Superseded by [0056](0056-a-relaying-source-cannot-convert-back.md), which puts
`share_count_basis` back on `txs`, `eod_prices` and `holding_declarations`. The
decision here fixed each row kind's basis to its own date and required a
restating source to convert before uploading, on the premise that a source which
restates knows the ratio it used. That is false for a source relaying someone
else's restatement.

The identifier half of the convention stands and is used by
[0055](0055-identifier-validity-is-an-interval.md): a file names an instrument as
of its `exported_at`, so an export cannot precede the purchase it describes.
