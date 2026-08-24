# A listing has a lifecycle and a security does not

A listing carries the half-open interval it was tradeable in
([0018](0018-half-open-date-intervals.md)), and the events that move its bounds are
events about a line rather than about the security above it. A security has no
interval of its own: its window is the hull of its lines'.

**A delisting closes a listing.** The line stops trading and the bound records when.
It is not represented as a new instrument and a merge, which would state the end of
a line as a change of identity -- and identity is exactly what does not change when
a security stops trading in one of its currencies. The names, prices, coverage and
dividends already filed against the line stay where they are, which is what makes
the closed interval the whole of the fact.

**A redenomination closes one listing and merges into another.** A security requoted
into a different currency is trading on a different line, because a line is keyed on
its currency ([0068](0068-a-listing-is-a-currency-of-a-security.md)). The old line
closes at the changeover and what it holds merges into the line taking over from it,
by the rule a merge already unions listing sets with
([0071](0071-listings-merge-by-currency-and-an-unknown-one-splits.md)).

**GBX to GBP is not one.** The two are one currency under a different unit prefix and
so one currency family, which is what listing uniqueness is on. A provider switching
from pence to pounds is quoting the same line in a different unit, and there is no
second line for it to close.

**A venue migration is not an event at all.** It is a change to the set
`listing_venues` holds, which 0068 settled when it made venue an attribute of a line
rather than part of its identity.

## Consequences

`instruments.valid_from` and `instruments.valid_before` are deleted rather than moved
down to the line. Nothing filtered or ordered on them, no plugin supplied them, and
since the archive stopped carrying them nothing wrote them at all, so there is no
value to move -- the interval is minted at listing grain instead.

Nothing closes a line yet. Archive import is the only writer of the bounds, which it
fills from a file that states them; no corporate-event source produces a delisting or
a redenomination, and no code path narrows an open interval. What this record settles
is where such a fact belongs when one arrives.
