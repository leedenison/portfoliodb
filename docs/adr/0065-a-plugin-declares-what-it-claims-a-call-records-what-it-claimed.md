# An identifier plugin declares what it claims; a call records what it claimed

Two surfaces, and the temptation is to build one. They answer different questions and
only one of them can be trusted to gate anything.

**A declaration** says which claims an identifier plugin makes: which identifier types
it returns, which of them it returns together, and **which it strictly filters on**. It
is a static property of the plugin, alongside its precedence and config.

**A record** says which claims one call actually made. It is what
[0060](0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md) needs to
tell a corroborated association from a manufactured one.

The record is the enforcement surface because a declaration is an unenforced promise.
Nothing checks that a plugin returns what it said it would, and a provider changing its
response shape drifts the two apart silently. Gating a merge on a declaration means
gating it on a comment.

## A filter is a claim, so both surfaces have to carry it

A provider that answers "no identifier found" when its filter matches nothing has
asserted the filtered value denotes the security it described, whether or not that
value comes back in the payload
([0060](0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md)). So
*filtered on* is graded equally with *returned*.

Both surfaces need it and for different reasons. The declaration needs it because
"could anything have corroborated this?" is unanswerable otherwise: a plugin that never
returns ISINs may still be the only thing in the system able to confirm one. The record
needs it because whether a given call filtered on a given value is a fact about that
call, and the merge rule acts on the call.

The strictness is the whole of it. A filter a provider silently relaxes when it finds
nothing is a hint, and a response to one confirms nothing -- it is exactly the echo a
real filter resembles, and treating the two alike would let a guessed identifier confirm
itself, the failure [0059](0059-an-invented-identifier-round-trips.md) exists to
prevent. Since nothing can check a provider's strictness from outside, that declaration
carries the same caveat as everything else static here: it is a claim about behaviour,
useful for reasoning and not for gating on its own.

## What the record has to carry is less than it looks

Not which plugin returned which identifier. For deciding whether an association may be
admitted, plugin identity is irrelevant -- every identifier plugin is authoritative for
global identifiers, so an ISIN from one is exactly as admissible as an ISIN from
another. What matters is only which identifiers arrived **together** -- returned or
filtered on -- because that is what makes the association a claim somebody made rather
than a set the resolver assembled.

So the requirement is that results reach the merge site partitioned, not attributed.
Plugin identity is worth recording, but for the other surface.

## What declarations are for

Not gating. Answering questions about the system rather than about a row:

- **Could anything have corroborated this?** If no enabled plugin declares that it
  returns ISIN and CUSIP together, or returns one while filtering on the other, then
  every ISIN-to-CUSIP association in the database arrived some other way, and that is
  answerable without auditing rows.
- **Is a silence informative?** A plugin that declares it returns ISINs and returns none
  has said something about the security. A plugin that never returns ISINs has said
  nothing. Consistency checking cannot tell those apart, because it skips any comparison
  where either side is empty.
- **What errors can this configuration produce?** The reachable claim graph is a
  function of the declarations, so the shapes of erroneous merge a given set of enabled
  plugins can generate are computable before any data arrives.

## Consequences

- The identifier-plugin-call telemetry has to record identifiers -- what a result
  returned and what it was filtered on -- rather than only the plugin, an outcome,
  retries and a duration. Otherwise "which plugin corroborated this association?" cannot
  be answered for the one class of claim where it matters. Candidate-field telemetry
  already carries full attribution for claims that are not evidence and can never merge
  ([0057](0057-a-proposed-identifier-is-not-evidence.md)), which is the wrong way round.
- This is a plugin-level declaration. Whether an identifier type is ever reassigned is a
  property of the type, declared separately
  ([0061](0061-transitivity-needs-a-non-reassigned-identifier.md)); putting a fact about
  ISINs inside a provider's config would make it a matter of opinion per provider.
- Candidate plugins need no declaration. What they return is already recorded per field,
  and none of it can merge, so there is nothing a declaration would let anyone reason
  about that the rows do not already say.
