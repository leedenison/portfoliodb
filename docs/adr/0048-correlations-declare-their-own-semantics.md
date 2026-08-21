# Correlations declare their own comparison semantics

Supersedes [0042](0042-grouping-evidence-in-the-standard-format.md), which split
this into a `kind` from a shared vocabulary and a `namespace` that scoped what a
token was comparable with, and said nothing about what may be done with a token once
two postings hold one. The scope and the operator are two halves of one statement,
and neither is usable without the other.

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

`label` replaces `namespace`, and `kind` drops out: its only job was keeping two
identifier series apart so an order id is never compared against a settlement
reference, and `label` does that without a controlled vocabulary shared under
[0038](0038-controlled-vocabularies-are-shared.md).

## Scope

`FILE`, `ACCOUNT`, `BROKER` or `USER`, exactly one. Each narrows to as far as its
issuer's numbering is unique.

`ACCOUNT` is not redundant with `FILE`: an OFX `FITID` is unique within the account
rather than within the institution, and a Fidelity export spans accounts, so a
file-scoped reading of an account-scoped token produces false positives silently.
`BROKER` means this user's data for this broker -- a Fidelity reference means nothing
across users.

`FILE` resolves to the ingesting job, a file having no identity of its own once its
postings are rows. One consequence is worth stating: an archive import is one job, so
evidence re-imported from an archive is comparable across everything the archive
carried rather than within the uploads it was assembled from. The alternative -- the
archive carrying a per-file identity so an import could reconstruct the original
boundaries -- would make the archive preserve which upload a posting arrived in,
which nothing else about a posting does.

`USER` is for a person's own assertion ([0049](0049-a-human-assertion-is-a-correlation.md))
and reaches the whole of that user's data. A person's token is unique because they
minted it, and narrowing it would defeat the assertion in exactly the cases -- legs in
different accounts, legs from different brokers -- that make someone reach for it.

## Match

A set rather than a choice, because a Fidelity reference number is honestly both
equality-comparable and ordinally comparable, and declaring only one of those
understates what the source supplies.

**`MATCH_EXACT`** -- two postings hold the same token. The highest-precedence grouping
pass ([0047](0047-grouping-runs-as-precedence-ordered-passes.md)) is an exact token
match, rejected only on stated incompatibility: a different broker, a different
account, or a candidate set that does not admit the pass. A claim from an exact match
is a **must-link**: these rows belong together, and a later pass may add to the group
but may not split it. A source that names two rows of a three-row event would
otherwise exclude the third.

**`MATCH_ORDINAL`** -- the `ordinal` field is populated and comparable. It is a
separate field from the token because proximity is load-bearing and cannot be
recovered from the token: an IBKR `FITID` reads `20251015U10000018371888432`, which is
not an integer but plainly carries one, and only the converter knows how to take it.
Inferring distance from the strings instead -- an edit distance or anything like it --
was rejected: `1000000` and `0999999` are adjacent references four edits apart while
`1000001` and `2000001` are one edit apart and a million references apart, so it would
group unrelated rows and miss related ones, differently for every broker, and
silently. Where a source's identifiers are genuinely opaque the converter emits no
ordinal and declares `MATCH_EXACT` alone; proximity is then unavailable for that
broker, which is honest and better than manufacturing an ordering the source does not
have.

**`MATCH_ACCOUNT`** -- the token names *another posting's account* rather than a token
that posting carries. A counterparty pointer is comparable but asymmetrically, so
`MATCH_EXACT` on it would never fire: each side's token names the other's account, and
the two are never equal. Making it a third operator rather than a second mechanism
keeps the pointer as evidence the engine weighs rather than a special case beside it.

**`MATCH_ATTACHES`** -- the token is a directed reference to another posting's
identifier, and the bearer joins whatever group that posting is placed in. See
[0052](0052-an-attaching-correlation-is-additive.md), which is what implements "a later
pass may add to the group but may not split it".

A hand-made *pairing* of two transfer sides declares `MATCH_EXACT` like a hand-made
grouping, rather than a fifth value meaning "opposite sides of one transfer". What the
two assertions mean differs, but the difference is already carried by the postings:
where every member must be a transfer the claim pairs their groups, where none is it
merges them, and a mixed set is refused when it is asserted. An operator would restate
what the type already says.

## It subsumes a broker-supplied group id

A source that states a grouping always states it as a shared token. OFX expresses
grouping by document containment, and the parser turns containment into a token by
stamping the containing `INVTRAN`'s `FITID` on every leg it produces. A separate
group-id field would be a second spelling of `SCOPE_FILE | MATCH_EXACT`, and only one
of the two should exist.

A source-stated correlation is evidence the engine consumes, not a partition it obeys,
so this is not the second decider
[0041](0041-server-owns-transaction-grouping.md) and
[0043](0043-grouping-does-not-travel-in-the-archive.md) both turned down.

## The transcription contract

A converter may synthesise a token from the source's own **structure** -- nesting,
containment, an explicit order column -- and never from amounts, dates or proximity.
Nothing on the wire distinguishes a transcribed token from an inferred one, so this is
the discipline that keeps converter grouping rules from being left in place under a new
name.

It binds converters, and says nothing about a person deliberately supplying evidence:
a `SCOPE_USER` assertion is neither inference nor transcription, and says so in its
scope. A `MATCH_ATTACHES` pointer a converter derived rather than transcribed is
visibly an inference *because the match value says so*, which is exactly what the
contract exists to protect.

**A record's derived legs carry the record's correlation.** A converter reads more
than one posting out of one source record: the commission netted into a trade's total
becomes a leg of its own, and a reinvestment's units are bought with income no row
reports. Neither can be recovered by the server -- the commission column and the fact
that a purchase was funded by income are both read by the converter and then discarded
-- so a leg read out of a record is another leg of that record and carries its
identifier. This is the transcription contract rather than an exception to it: the
record boundary is the source's own structure, and the claim is only that these
postings came out of one record. Where the source identifies the record by nothing,
the converter synthesises a file-scoped equality token under a label of its own. A
**boundary** leg is not covered and carries nothing: it is inferred from the declared
type alone -- a row that must be income has its other side in income -- so the server
derives it and recreates it on every regroup, like a routed residual.

## Consequences

`ordinal_span` -- how far apart two references can be and still belong to one event --
is carried by the source, because how densely a broker issues references is a fact
about its numbering rather than a grouping policy. The boundary is genuinely slippery:
a span is a tuning constant justified against measured exports, and "the converter
supplies a constant" is one step from "the converter supplies the rules". It is placed
here anyway because a server-side constant would be wrong for every broker but one.

`broker_ref` and `counterparty_account` fold in rather than sitting alongside. Every
`broker_ref` was exactly a correlation token, on exactly the postings a correlation
goes on, so keeping both would put the same string on the wire twice and leave two
places to improve. Transfer matching is what read them, and reading correlations
instead moved the one piece of broker knowledge it held back to the converter: it
parsed each reference with `strconv.ParseInt` to get a proximity signal, and an opaque
identifier merely happened to fail the parse. Now the correlation says whether it has
an ordinal, and a source with no numbering declares none rather than being discovered
to have none.

## Considered: a field per broker

Carrying each source's grouping fields verbatim was rejected in 0042 and stays
rejected. The format becomes the union of every broker's evidence, growing a nullable
field per source until no consumer can rely on any of it. Abstracting the shape of the
evidence keeps the format small and puts the translation where the broker knowledge
already is.
