# Decimal values cross the wire as strings

A protobuf `double` cannot carry an exact decimal, so the values
[0026](0026-exact-decimals-bounded-by-closure.md) puts on the exact side of the
boundary are encoded as canonical decimal strings in `api.proto` -- plain
`string` fields, not `google.type.Decimal`. This is the convention Google's own
API types follow (`google.type.Decimal` is itself a string; `google.type.Money`
is an integer unit count) and that FIX and Stripe follow for the same reason.

`google.type.Decimal` was the obvious alternative and was rejected because it is
a message wrapping a single string field: it buys a self-documenting type name
and nothing else, at the cost of `.value` indirection in Go and `{value: string}`
in TypeScript on every access. `optional string` already gives the presence
semantics the current `optional double` fields rely on. Since a bare `string`
does not announce what it holds, two things stand in for the type name: a
uniform field-comment convention, and protovalidate CEL patterns constraining
the format. The validation is a net gain -- not one of the 26 `double` fields in
`api.proto` today carries any constraint at all, so the string form is the first
time these values are checked at the wire.

A scaled integer pair (units plus scale, as `google.type.Money` uses) was also
considered and rejected: it is compact but the scale is easy to mismatch across
languages, and the compactness does not matter for the message sizes here.

## Consequences

Almost the whole API moves. Of the 26 `double` fields, 25 are on the exact side:
the `txs` quantities and prices, `Holding` quantities, `Instrument.strike` and
`contract_multiplier`, all three OHLC message variants,
`InflationIndexProto.index_value` and `ResidualBalance.balance`. Two cases carry
most of the weight. `ExportPriceRow` and `ImportPriceRow` are a round-trip pair,
and an export that does not reimport identically is a bug -- today both sides
downgrade a `NUMERIC` column through a `double`. And `strike` is denormalised
from the OCC identifier, so it is a component of option identity; comparing
identity components as floats is a latent bug that the option-identity work
would eventually have found.

`ValuationPoint.total_value` stays `double`, and any later performance metric
joins it. It is the chart series, computed through an FX division, so by
[0026](0026-exact-decimals-bounded-by-closure.md) it is an estimate before it
reaches the wire and a decimal string would overstate it. Recharts consumes
`number`, so a string would also be parsed back on arrival for no gain.

Client display paths get simpler rather than harder: rendering a decimal string
directly removes the `parseFloat(x.toFixed(n))` idiom that exists only to hide
float artifacts. Arithmetic on the client is confined to the CSV and OFX
converters, which author facts and need a decimal library; nothing else on the
client computes with these values.
