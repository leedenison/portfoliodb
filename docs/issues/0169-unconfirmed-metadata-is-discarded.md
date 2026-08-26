---
status: open
title: Unconfirmed metadata is discarded when an authority arrives
milestone: M24
dependencies: [0140]
---

An instrument named only by user-authoritative sources holds metadata nobody
verified, and it keeps it after an authority has identified it.

`instruments.name` is the live case. Both broker-description-only creation paths
pass the broker's own text as the instrument's name -- the fourth argument to
`EnsureInstrument` is `name`, and ingestion passes `instrumentDescription` there
in the extraction-failed path and in the resolution fallback. `InstrumentMerge`
has no `Name` field, `updateInstrumentOnMatch` writes none, and no statement
anywhere updates the column. So when a later resolution completes the instrument
in place, the identifier plugin's name is dropped on the floor and the broker's
text stays as the security's name for good, on a row that is now
system-authoritative in every other respect.

That contradicts what adr/0067 says a broker-description-only instrument is --
"every column is null" -- which is what made it easy to miss: the invariant reads
as maintained because the shape it describes is nearly right.

## What to do

At the moment an instrument stops being user-authoritative, its own metadata is
discarded and replaced by what the system authority supplied. Not merged with it,
and not defended by having been stored first: adr/0004 protects a value an
authority wrote, and this row holds none.

The class of the instrument is the provenance, so nothing has to be recorded per
column to know which values these are. That is the whole reason this is cheap,
and it is worth saying in the code, because the obvious reading -- confirm each
field against what the plugin returned -- is the expensive one and buys nothing
here.

One call site: the completion in place, where the instrument becomes
system-authoritative under its own id. There is no second one. Merging outright
needs a claim carrying system authority over associations that are already facts,
and a user-authoritative instrument holds none, so the survivor of a merge is
always system-authoritative before the merge begins and there is no unconfirmed
metadata on it to discard.

The name needs the trigger as well as the write. `recompute_instrument_name`
ranks `BROKER_DESCRIPTION` above the stored name, so a completion that inserts
only an ISIN re-derives the broker's text over whatever the write put there, and
the two halves have to land together. The description drops below the stored
name: an authority's identifier names the instrument, failing that the name an
authority supplied, failing that the broker's own text, failing that its id.

Ingestion should stop passing the description as the `name` argument at the same
time. The trigger supplies the same display name from the `BROKER_DESCRIPTION`
row it already holds, and the row is then the shape adr/0067 describes.

Scope is the metadata columns. Identifiers are not this: a user-authoritative
identifier is owned rather than discarded, which is 0142.

See adr/0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md.
