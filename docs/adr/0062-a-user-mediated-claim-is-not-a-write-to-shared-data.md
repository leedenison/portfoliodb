# A user-mediated claim is not a write to shared data

[0060](0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md) makes
authority a level a source carries. This says what follows for the one channel carrying
the weaker level, and why the level is the channel's rather than the subject matter's.

## The level of authority is the channel's, not the subject matter's

A broker file reaches us through the user. It is unauthenticated, so nothing attests it
came from the account it claims; it is a single artefact, so a misgrab, a stale export or
a hand edit is invisible; and it cannot be re-interrogated, so a doubt raised later has
nothing to ask. An identifier plugin call is none of those things -- it is authenticated
to the provider, repeatable, and can be asked again when the answer starts to look wrong.

Instruments are instance-global. A merge driven by one user's upload rewrites reference
data for every user of the instance. So a user-mediated claim carries the blast radius of
a plugin claim on strictly weaker provenance, which is the combination worth refusing.

## Scope is not the discriminator, though it looks like one

The line to draw here reads naturally as one about scope: a broker is trusted for its own
contract numbers, whose meaning no provider can be asked, and not for a claim derived
through one, which lies wholly inside the identifier plugins' domain. That is right about
the cases and wrong about the reason.

What separates them is not what the identifier is scoped to but what acting on the claim
would change. Writing a user-owned contract number on to the instrument a global
identifier already reached moves nothing; merging two instruments whose associations are
facts moves them for everybody. Scope resembles the discriminator only because the values
a broker alone can supply are also the ones that reach us only through a user
([0079](0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md)).

So the rule is stated over the consequence rather than over the value:

> What a user-mediated claim may do is bounded by what acting on it would have to touch.
> It may be stored owned by the user who supplied it. It may not settle anything the
> instance holds as a fact.

Whether such a claim is worth storing at all is a further question, answered by whether
anything could have adjudicated it rather than by its scope
([0065](0065-a-plugin-declares-what-it-claims-a-call-records-what-it-claimed.md), issue
[0175](../issues/0175-a-claim-is-owned-only-where-nothing-can-adjudicate-it.md)).

## What the refused claim becomes

Nothing durable. [0064](0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md)'s
record for a person became run-scoped telemetry
([0080](0080-a-contradiction-is-logged-not-queued.md)), and no functional path may read
that -- so a refusal cannot be parked anywhere that would later act on it, and there is
no surface on which a hypothesis could wait for a plugin to settle it.

The claim is re-derived instead, on every upload of the same file, and the two routes out
are unchanged: an identifier plugin corroborating the pair, or
[0063](0063-identity-claims-are-owned-until-users-corroborate-them.md)'s sweep promoting
the mapping. The cost is the quota spent asking again, which
[0080](0080-a-contradiction-is-logged-not-queued.md) priced and accepted.

What survives of the reason for keeping it is the premise: the broker edge is the only
thing that knew where to look, and discarding it wastes the one useful part of an
untrusted message. What that now buys is narrower than a lead for somebody to follow. The
claim is kept for the user who supplied it while being refused for the instance, and the
transaction resolves to one instrument rather than to none (issue
[0143](../issues/0143-a-claim-that-would-move-a-fact-is-refused.md)).

## Consequences

- Trusting a broker for its own identifiers is not a concession to convenience. It is
  forced: there is no second opinion available, so refusing the claim means the
  identifier is useless and refusing the file means the transactions are lost. What that
  forced trust needs is a bound on its blast radius, which is
  [0063](0063-identity-claims-are-owned-until-users-corroborate-them.md).
- An admin's archive is not a user-mediated claim in this sense and an admin's broker
  file is. See [0063](0063-identity-claims-are-owned-until-users-corroborate-them.md) for
  why the distinction is about the artefact and the act rather than about the person.
- Ownership alone does not bound the blast radius. Identifier rows are owner-scoped and
  instruments are not, so a chain drawn through an owned row still merges instance-global
  rows -- which is why
  [0061](0061-transitivity-needs-a-non-reassigned-identifier.md) makes being a fact its
  third condition.
