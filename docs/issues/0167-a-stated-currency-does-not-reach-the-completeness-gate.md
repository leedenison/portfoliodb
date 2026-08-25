---
status: open
title: A stated currency does not reach the completeness gate
milestone: M24
---

The spec says a currency hint completes an identity: "an upload stating an ISIN
and a trading currency, which is the common broker case, does not reach this
stage at all." adr/0058's amendment header says the same, and it is the whole
point of adr/0068 reaching back into that decision -- a listing is a currency of
a security, so a source that named the currency named the line.

The code does not do it. `tx.currency` becomes `identifier.Hints.Currency` via
`HintsFromTx` and never a `CURRENCY` identifier, and the gate is asked only of
`identifierHintsFromTx`'s output. So an ISIN plus a trading currency is judged
incomplete and pays a candidate plugin to close a choice the source had already
closed -- the IBKR QFX case adr/0058 is written around.

`identifier.ReachesOneLine` answers this correctly for a stated `CURRENCY`
identifier; what is missing is that a currency stated as a hint never becomes
one. Either the gate reads `Hints.Currency` beside the identifiers, or the
conversion mints the identifier -- and the second changes what gets stored, so it
is not the smaller change it looks.

Whichever way, it needs a telemetry decision: whether a key refused here records
`candidate_not_attempted_identity_complete` or an outcome of its own. The two
move for different reasons and a rate that blends them says less.
