---
status: open
title: The canonical flag retires in favour of ownership and injectivity
milestone: M24
dependencies: [0142]
---

`instrument_identifiers.canonical` and its listing-grain twin answer one question
-- is this row a broker description -- and answer it by convention rather than by
derivation: the schema comment says `canonical = false only for
BROKER_DESCRIPTION`, and every writer honours that by hand.

It is a type property stored beside the type, which is the drift 0165 closed for
grain, reassignment and the rest. The vocabulary declares injectivity -- one
value denotes at most one security at a time, which a broker description does not
-- so the column carries nothing the vocabulary does not.

It also answers the wrong question for what comes next. Its two real readers --
`db.Identified`, which is the only statement of what "identified" means and is
rendered in the UI, and `holdsNoCanonicalIdentifier`, which decides whether an
instrument is completed in place -- both want to know whether an authority has
named this instrument. Once 0142 lands that is `owner IS NULL`, and it is not
what the flag says: a contract identifier from 0123 arrives on an unvetted row
and defaults to `canonical = true`, while a broker description restored from an
admin's archive is written by an authority and reads as `canonical = false`.

## Scope

Replace the readers first, and note they ask two different questions that the one
flag has been answering. Authority is the owner column. Whether a value names one
security is `identifier.Injective` in Go, or the type outright in SQL, where
`holdsNoCanonicalIdentifier` and the `listing_venues` trigger ask it.
The trigger's `li.canonical` predicate is inert today -- no `MIC_TICKER` is ever
written non-canonical -- so it goes rather than being translated.

Then the column, and the reach is what makes this its own issue rather than a
line in 0142: the flag is on the wire as
`apiv1.InstrumentIdentifier.canonical`, it is in the archive format and read back
by the importer, and the client displays it. An archive that wants to state where
a name came from should carry ownership, which is a different field with a
different meaning, so this is a format change and not a rename.

See adr/0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md.
