---
status: open
title: Evaluate the archive ergonomics of sharing a broker-description map
milestone: M17
---

Evaluate whether an admin can export the broker-description mappings that definitive
identification produced, and import them into an instance whose users have only CSV
to import from.

## Motivation

The Fidelity CSV names no venue and no ISIN. 0107 settled what the download actually
carries: a security is named `ISSUER, SECURITY DESCRIPTION (TICKER)` and that trailing
parenthetical is the whole of what the file says about the listing, so
client/lib/csv/converters/fidelity-csv.ts emits a `MIC_TICKER` with no domain rather
than guess a venue the source never named. The currency does not come from the file
either -- it comes from a dropdown the user picks in fidelity.tsx. An unlisted fund
ends in no ticker and offers no hint at all, leaving the description to resolve it
alone.

A bare ticker is not unique across venues, so this is how a CSV upload lands on the
wrong listing of the right company -- a silent error rather than a failed import.

A user running the extension does not have this problem.
extension/src/brokers/fidelity-json.ts reads the JSON behind the same export and
keeps the ISIN the CSV discards, so their descriptions resolve definitively. An
instance where those users import is therefore accumulating the mappings a
CSV-only instance is guessing at.

Moving them is an admin operation and should stay one. `INSTRUMENTS` is a
system-archive part and both `ExportSystemArchive` and `ImportSystemArchive` are
admin-only (proto/api/v1/api.proto), which is the right shape: an identifier is
instance-global, so importing a map rebinds those descriptions for everyone on the
instance, and that is a decision an admin takes rather than something a user does to
their own account. The question is not who is allowed to do it but whether the
archive makes doing it practical.

The mapping is not a thing of its own today. It is a row in `instrument_identifiers`
with `identifier_type = BROKER_DESCRIPTION`, `domain = source` and `value =
description` (server/db/postgres/instruments.go,
`FindInstrumentBySourceDescription`), and it travels in an archive as one more entry
in `Instrument.identifiers` (proto/archive/v1/common.proto and instruments.proto). So
there is no map to export -- only whole instrument records that happen to carry one.

## Scope

Five questions, each with an obstacle already visible in the code.

**Is the mapping recorded at all on the source instance?** Path A in
server/service/ingestion/resolve.go deliberately persists no `BROKER_DESCRIPTION`
identifier when the client supplies identifier hints, keeping the resolution only in
the batch's in-memory cache. The extension always supplies an ISIN, so the imports
best placed to build the map are the ones that skip building it. Since 0107 the CSV
supplies a ticker hint for every listed security too, so on both sides the only rows
that leave a stored mapping behind are the ones nothing could hint -- the unlisted
funds. That is close to the inverse of what a shared map wants to carry, and
everything below is moot if it holds.

**Can an export be scoped to the mappings?** `ExportSystemArchiveRequest` filters on
`exchange` and `asset_classes` alone, and `ListInstrumentsForExport` has no filter on
identifier type, on `canonical`, or on the domain that holds the source. A map is
therefore a full instrument export. 0017 surfaces the two filters that do exist, and
is the neighbour if a source filter is wanted.

**Does importing one behave sensibly?** Two hazards worth confirming. The payload
dedupe key in server/archiveimport/instruments.go is type and value with the domain
excluded, while the database uniqueness is over type, domain and value -- so the same
description arriving under two sources reads as a duplicate and the second instrument
is rejected, which a mapping-heavy file makes the ordinary case rather than the rare
one. And `MergeInstrumentFromArchive` inserts identifiers with `ON CONFLICT DO
NOTHING`, so a description already bound to a different instrument is silently
ignored and nothing says it was.

**Can an admin tell what a map would change before importing it?** The effect is
instance-wide and the archive import is asynchronous, so the admin's judgement rests
on what the job reports. Worth settling what they can see: which descriptions the file
would newly bind, which it would leave alone because they are already bound, and
whether that is visible before the import rather than only after. 0104 is the same
decision taken one row at a time, with the instrument in front of the person making
it.

**What does exporting one disclose?** A broker-description identifier exists only
because someone uploaded a file containing it, so a set of them names the instruments
those users traded and the sources they traded them under. That leaves the instance
with the export, which is a thing to weigh rather than a blocker.

## Deliverable

A judgement recorded here: either the archive is close enough and the gap is an export
filter plus a better import report, or a description-to-instrument map is its own
artifact and needs specifying as one. Either way the work that follows gets its own
issues.
