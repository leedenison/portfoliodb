# Archive files nest by aggregate root

An archive file has three levels -- file, group and row -- and each states its
own scope in full. The group is the entity's aggregate root: the **instrument**
for prices and corporate events, the **tx group** for transactions, the
statement for holding declarations. Coverage, asset class and currency sit on
the group; the file envelope carries only `format_version`, `exported_at` and
the source instance; rows carry only what varies per row.

A field belongs at the file level only if it **cannot** differ between two rows
of a valid file. Being constant in practice is not enough, and the flat formats
got this wrong in both directions. `prices-recovered.csv` carries ninety
coverage declarations and every one is instrument-scoped -- the file-wide slot
the format was designed around is unused, because coverage diverges exactly when
instruments have different lifetimes, which is the case it exists to record. In
the other direction `ExportPriceRow.exported_at` stamps one value onto every row
because the stream has nowhere else to put it.

**There is no inheritance or override between levels.** Repetition is cheap in a
machine format and compresses away; precedence rules are expensive, because
every reader must implement them identically and a disagreement is silent. The
current price CSV needs four rules to rebuild nesting from flatness -- at most
one global declaration, a specific one overrides rather than adds, several
specifics all apply, a partial identifier is an error -- plus two cases the
export must always write out in full. The cost of getting that wrong is not an
error but a wrong number: a missing share count basis reads as as-traded and
back-adjusted prices are adjusted a second time.

The same test decides between the group and the row. `share_count_basis` reads
like a property of a series and sat on the group at first, but `eod_prices`
stores it per bar, so one instrument can hold a restated stretch beside an
as-traded one and the field belongs on the row -- where
`Declaration.share_count_basis` already sits, for the same reason. Keeping it on
the group would have split an instrument into one group per basis, repeated its
identifier, asset class and currency across them, and left coverage -- stored
per instrument, with no basis dimension -- belonging to no one group.

Nesting is free to produce. `ListPricesForExport` already orders by identifier
then date, and `ExportPricesResponse` is already a `oneof` of coverage and row,
so the stream carries an ad-hoc envelope today; this makes it typed and scoped
instead.
