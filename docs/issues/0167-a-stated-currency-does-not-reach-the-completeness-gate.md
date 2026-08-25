---
status: closed
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

## Outcome

The gate reads the currency beside the identifiers. `statedIdentityComplete`
takes `quotedIn(tx)` -- the currency the source stated the security is quoted in,
with no settlement fallback -- rather than the conversion minting a `CURRENCY`
identifier, which would have filed USD as an identifier of Apple.

**A currency completes half a line, not a whole one.** adr/0068's consequence
bullet read as though a stated currency completed any identity, and that the
stage narrowed to sources stating no currency at all. That overreaches: a line is
a security and a currency, so a currency names one line of a security something
else named. Beside an ISIN it completes the identity; beside a bare ticker it
names the line of no particular security, tickers being reused across venues, and
choosing among those is what adr/0058 built the stage for. Both ADR sentences now
say the narrower thing.

No source in hand exercises the difference: the converters state ISIN, CUSIP,
SEDOL, OCC and CURRENCY, all of which either reach a line already or name the
security. The narrowing is about what the rule says rather than what any file
does today.

**`LinesMany` split in two.** 0165 made it one member covering "reaches every
line the security trades in" and "names no security to count the lines of", on
the table's own doctrine that a member changing no answer would read as a
distinction the rules make. This is the rule that makes it: a currency closes the
first gap and cannot close the second, so `BROKER_DESCRIPTION` is now
`LinesNone`. `identifier.NamesTheSecurity` is `LinesMany` exactly.

**Telemetry is unchanged.** A key refused here still records
`candidate_not_attempted_identity_complete`. The skips each name a different
thing to act on, and this is not a different thing: the gate did its job and the
source had already named the line, whether by one name or by two halves.
