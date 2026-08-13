---
status: open
title: Surface the instrument export filters in the UI
milestone: M17
dependencies: [0016]
---

Let an admin narrow the instruments part of a system export from the archive page,
using the filters the server already applies.

## Motivation

`ExportSystemArchiveRequest` carries `exchange` and `asset_classes`, and both reach
the query: `ListInstrumentsForExport(ctx, exchangeFilter, assetClasses)` in
server/db/postgres/instruments.go, called from server/service/api/archive.go. No
caller sets them. `exportSystemArchive` in client/lib/portfolio-api.ts takes only
`parts`, and client/app/admin/archive/page.tsx offers the part menu and nothing
else, so an export of the instruments part is always every instrument.

## Scope

Client only. Pass the two filters through `exportSystemArchive`, and put the
controls on the archive page next to the part menu, shown only when INSTRUMENTS is
selected -- the server ignores them otherwise and the UI should say so by not
offering them.

## Broker is not one of the filters

This issue originally asked to filter by broker and exchange. An archived instrument
has no broker: it is reference data shared across users, and the broker only ever
described where a description came from, back when instruments were exported as
their own JSON. Asset class is the second axis in its place and is already on the
request. There is nothing to build for broker and nothing to store it in.
