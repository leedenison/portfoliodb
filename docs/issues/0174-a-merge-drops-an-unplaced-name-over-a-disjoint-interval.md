---
status: open
title: A merge drops an unplaced name over a disjoint interval
milestone: M24
---

`mergeInstruments` moves the loser's listing-grain names that name no line by
security alone, guarded by a `NOT EXISTS` on the triple:

```sql
UPDATE instrument_listing_identifiers SET instrument_id = $1
WHERE instrument_id = $2 AND listing_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM instrument_listing_identifiers o
                  WHERE o.instrument_id = $1 AND o.identifier_type = ...
                    AND o.value = ... AND COALESCE(o.domain, '') = ...)
```

The guard compares the triple and not the interval. A name the survivor holds
over `[Jan, Mar)` therefore blocks the loser's row for the same triple over
`[Apr, Jun)`, which is left behind and cascades away with the instrument.

Those two rows are not duplicates. Identifier validity is an interval and the
triple denotes one instrument at a time (adr/0055), so disjoint rows are two
periods of one name and the exclusion constraint admits both. The comment
justifies the drop as "the survivor holding it already is the two rows saying the
same thing", which is true of an overlapping row and false of a disjoint one.

What is lost is history rather than a contradiction: the name was correct over a
window nothing now records, so an archive exported from either side of it
resolves to nothing. That is the same loss adr/0055 built the interval to prevent,
and it is why the security-grain move above carries `valid_from` and
`valid_before` across rather than dropping the bounds.

Both tables the loop covers have it, though `provider_listing_identifiers` carries
no interval at all, so only the canonical one can be wrong this way.

Found while auditing the merge for 0141, which changed the neighbouring code and
deliberately left this alone: it is lost history rather than a claim that cannot
hold, and the two want different answers.
