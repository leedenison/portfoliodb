# One identifier triple, restated only across an import boundary

An identifier is a `(type, domain, value)` triple, and `identifier.Identifier`
is the one declaration of it. A package that needs the triple imports it. The
triple had accumulated some thirty declarations across the server -- three of
them named `Identifier`, the rest inlining it as `IdentifierType`,
`IdentifierValue` and `IdentifierDomain` fields -- because each was a defensible
local call and nobody could see the whole. Two plugin packages carried the same
comment, *"Defined here to avoid importing server/identifier"*: avoiding an
import is not a reason to restate a domain type, and it is explicitly rejected
here.

Two restatements stand, and both exist because a Go import cannot cross the
boundary rather than because someone preferred a local copy.

`db.InstrumentRef` and `db.ProviderIdentifierInput` are the persistence-layer
counterparts of `identifier.Identifier` and `identifier.ProviderIdentifier`.
`server/identifier` imports `server/db`, so `server/db` cannot import
`server/identifier` without a cycle. The direction is the right one: the db layer
is the lower of the two, and inverting it to share a struct would put the
database abstraction downstream of the domain package that queries it.
`pluginutil.ToIdentifiers` is where the two meet, and one conversion at a known
seam is cheaper than the cycle.

The sqlx row structs in `server/db/postgres` restate the `db` types they scan
into. They are not the same shape with tags added: `residualBalanceRow` scans
enums as `string` where `ResidualBalance` holds `typev1.AccountType`, and
nullable columns as `*string` where the domain type holds `string`;
`exportPosting` scans `broker_tx_type` as `pq.StringArray` and omits
`Correlations`, which a second pass attaches. Collapsing them would push
`pq.StringArray`, `[]byte` JSON blobs and stringly-typed enums into the
interface that exists to keep SQL out of the rest of the server. Each row struct
converts through a `toDomain` method, and each embeds `db.InstrumentRef` rather
than spelling the three columns again.

## Consequences

A new plugin package takes `identifier.Identifier` by import. A new export type
names its instrument with a `db.InstrumentRef` field rather than three flattened
ones, which is what makes `bestIdentifierJoin` able to promise that every export
naming one identifier per instrument agrees which one.

`db.InstrumentRef` carries `db:"..."` tags even though it lives in the interface
package, because the postgres row structs embed it to scan the three columns
flat. This is the one place a persistence detail sits on a `db` type, and it is
the price of not declaring the triple a fourth time.

`transferMatchRow` is the one row struct that differs from its `db` counterpart
only by tags. It stays as it is: the pattern is worth more than the exception,
and a reader who finds one row struct following a different rule has to work out
which rule applies to the next one.

The vocabulary this triple's `type` field draws on is shared with the archive by
[0038](0038-controlled-vocabularies-are-shared.md). What a given type says about
scope and reassignment, and so what the triple may be used to conclude, is
[0061](0061-transitivity-needs-a-non-reassigned-identifier.md).
