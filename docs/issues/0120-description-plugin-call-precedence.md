---
status: closed
title: Record which description plugin ran first
milestone: M20
dependencies: [0116]
---

Description plugins run in precedence order and each sees only the items its
predecessors failed on, which is why `batch_size` is a different population per plugin
and their hit rates are not comparable. Nothing records that order, so the chain cannot
be reconstructed from the rows and a panel can only guess it from batch size.

Record the plugin's precedence rather than a loop index: a plugin whose filtered batch is
empty writes no row at all, so an index would not be the ordinal of the rows that exist.
A gap in the sequence then means that plugin was skipped, which is how a filtered-out
identifier plugin already reads.
