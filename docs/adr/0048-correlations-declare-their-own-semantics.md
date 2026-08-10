# Correlations declare their own comparison semantics

Supersedes [0042](0042-grouping-evidence-in-the-standard-format.md), which split
this into a `kind` from a shared vocabulary and a `namespace` that "scopes what it
is comparable with", and said nothing about what may be done with a token once two
postings hold one. The scope and the operator are two halves of one statement, and
neither is usable without the other.

**A posting carries repeated correlations, each stating an identifier, what may be
compared about it, and over what set of postings.**

```
message Correlation {
  string label = 1;             // which series; the comparability partition
  string token = 2;             // compared by MATCH_EXACT
  optional int64 ordinal = 3;   // compared by MATCH_ORDINAL
  Scope scope = 4;              // exactly one
  repeated Match match = 5;     // at least one
  optional int64 ordinal_span = 6;
}
```

`Scope` is `FILE`, `ACCOUNT` or `BROKER`, exactly one, and `BROKER` means this
user's data for this broker -- a Fidelity reference means nothing across users.
`ACCOUNT` is not redundant with `FILE`: an OFX `FITID` is unique within the account
rather than within the institution, and a Fidelity export spans accounts, so a
file-scoped reading of an account-scoped token produces false positives silently.

`Match` is a set rather than a choice, because a Fidelity reference number is
honestly both equality-comparable and ordinally comparable, and declaring only one
of those understates what the source supplies.

The highest-precedence grouping pass ([0047](0047-grouping-runs-as-precedence-ordered-passes.md))
is an exact token match, rejected only on stated incompatibility: a different
broker, a different account, or a candidate set that does not admit the pass.

## Token and ordinal are separate fields

0042 got this right and the reasoning stands: proximity is load-bearing and cannot
be recovered from the token. An IBKR `FITID` reads `20251015U10000018371888432` --
a date, a literal `U`, then a sequence. It is not an integer, but it plainly
carries one, and only the converter knows how to take it. So `MATCH_ORDINAL` means
"the `ordinal` field is populated and comparable", not "parse the token as a
number".

0042's rejection of inferring distance from the identifier strings -- an edit
distance or any similar measure -- also stands. `1000000` and `0999999` are
adjacent references four edits apart while `1000001` and `2000001` are one edit
apart and a million references apart, so it would group unrelated rows and miss
related ones, differently for every broker, and silently.

Where a source's identifiers are genuinely opaque the converter emits no ordinal
and declares `MATCH_EXACT` alone. Proximity is then unavailable for that broker,
which is honest and better than manufacturing an ordering the source does not have.

## It subsumes a broker-supplied group id

A source that states a grouping always states it as a shared token. OFX expresses
grouping by document containment, and the parser already turns containment into a
token by stamping the containing `INVTRAN`'s `FITID` on every leg it produces. A
separate group-id field would therefore be a second spelling of
`SCOPE_FILE | MATCH_EXACT`, and only one of the two should exist.

This corrects an assumption in [0041](0041-server-owns-transaction-grouping.md).
0041 treats broker idiosyncrasy as "an argument about *translation*, not about
*decision*", and reads all converter grouping as inference. For OFX it is
transcription: the source states the grouping and the converter copies it. Under
0041 as written the server would re-derive by amount-and-date inference something
the file says outright, which is strictly worse. A source-stated correlation is
evidence the engine consumes, not a partition it obeys, so this is not the second
decider 0041 and [0043](0043-grouping-does-not-travel-in-the-archive.md) both
turned down.

A claim from an exact token match is a **must-link**: these rows belong together,
and a later pass may add to the group but may not split it. A source that names two
rows of a three-row event would otherwise exclude the third.

## The transcription contract

A converter may synthesise a token from the source's own **structure** -- nesting,
containment, an explicit order column -- and never from amounts, dates or
proximity. Nothing on the wire distinguishes a transcribed token from an inferred
one, so this is the discipline that keeps
[0098](../issues/0098-retire-converter-side-grouping.md) reachable rather than
leaving the converter rules in place under a new name. It is the same contract
`broker_ref` already carries.

## Consequences

`label` replaces `namespace` and `kind` drops out. `kind`'s only job was keeping
two identifier series apart so an order id is never compared against a settlement
reference, and `label` does that without a controlled vocabulary shared under
[0038](0038-controlled-vocabularies-are-shared.md).

`ordinal_span` -- how far apart two references can be and still belong to one
event -- is carried by the source, because how densely a broker issues references
is a fact about its numbering rather than a grouping policy. The boundary is
genuinely slippery: `DEPOSIT_REF_SPAN` is a tuning constant justified against
measured exports, and "the converter supplies a constant" is one step from "the
converter supplies the rules". It is placed here anyway because a server-side
constant would be wrong for every broker but one.

`broker_ref` is the equality half of this and is already carried and stored, and
`counterparty_account` is a pointer of the same kind. Whether they fold into
`Correlation` or stay alongside it is settled when the field lands.

## Considered: a field per broker

Carrying each source's grouping fields verbatim was rejected in 0042 and stays
rejected. The format becomes the union of every broker's evidence, growing a
nullable field per source until no consumer can rely on any of it. Abstracting the
shape of the evidence keeps the format small and puts the translation where the
broker knowledge already is.
