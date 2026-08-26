# A user-mediated claim is a lead, not a write

[0060](0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md)
makes a broker the authority for claims about its own contract numbers, and
[0061](0061-transitivity-needs-a-non-reassigned-identifier.md) permits chaining
through an identifier type its issuer does not reassign. On those two alone a
broker file would merge two instruments that each carry registered identifiers:
IBKR
says `CONID-X` is `CUSIP-1` and later that `CONID-X` is `ISIN-2`, and if the
conid is never reassigned then `CUSIP-1` is `ISIN-2`.

It does not follow, because IBKR is not who we heard it from. This is the
reasoning behind the third of 0061's conditions -- that a chain runs through a
fact and never through a claim -- and the reason the type property is not
sufficient by itself.

## The level of authority is the channel's, not the subject matter's

A broker file reaches us through the user. It is unauthenticated, so nothing
attests it came from the account it claims; it is a single artefact, so a
misgrab, a stale export or a hand edit is invisible; and it cannot be
re-interrogated, so a doubt raised later has nothing to ask. An identifier plugin
call is none of those things -- it is authenticated to the provider, repeatable,
and can be asked again when the answer starts to look wrong.

Instruments are instance-global. A merge driven by one user's upload rewrites
reference data for every user of the instance. So a user-mediated claim carries
the blast radius of a plugin claim on strictly weaker provenance, which is the
combination worth refusing.

So brokers are trusted with exactly what nothing else can supply -- their own
contract identifiers, and their own description strings -- and not with claims
that lie wholly inside the identifier plugins' domain. `CONID-X` to `ISIN-2` is
trusted, because no provider can be asked it. `CUSIP-1` to `ISIN-2`, derived
through it, is not, because a provider can.

Trusted here means kept and acted on for the user who supplied it, not settled.
Everything arriving this way is a **claim** in the sense
[0060](0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md)
gives the word: possibly true, and not yet corroborated by anything carrying
system authority.

## What the refused claim becomes

Neither a merge nor nothing. The broker edge is the only thing in the system that
knew where to look, and discarding it wastes the one useful part of an untrusted
message. The derived claim is a hypothesis: two instruments that may be one, with
an identifier plugin able to settle it.

That is better than the alternatives it replaces. Merging on it spreads a
possible error into shared data with no way back. Refusing it leaves two
instruments we have reason to believe are one and no record of the reason.
Verifying it turns the untrusted channel into a source of leads, which is what an
unauthenticated message is good for.

## Consequences

- Trusting a broker for its own identifiers is not a concession to convenience.
  It is forced: there is no second opinion available, so refusing the claim means
  the identifier is useless and refusing the file means the transactions are
  lost. What that forced trust needs is a bound on its blast radius, which is
  [0063](0063-identity-claims-are-owned-until-users-corroborate-them.md).
- An admin's archive is not a user-mediated claim in this sense and an admin's
  broker file is. See 0063 for why the distinction is about the artefact and the
  act rather than about the person.
- A hypothesis needs somewhere to live between being raised and being settled.
  That is the same surface a contradiction needs
  ([0064](0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md)), and it
  should not be a second one.
