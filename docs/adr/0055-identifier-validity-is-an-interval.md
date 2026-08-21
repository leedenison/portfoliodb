# Identifier validity is an interval on the identifier

Supersedes [0017](0017-option-identity-reflects-ex-date.md).

`instruments.identity_as_of` was one point in market time per instrument, compared
against `stock_splits.ex_date` to decide whether an option's OCC symbol and strike
still needed restating. One stamp covers every identifier the row holds, and they do
not move together: an OCC encodes a strike and is restated by a split, an ISIN is not,
a `BROKER_DESCRIPTION` never was. The stamp is a claim about the instrument when the
fact it states belongs to one name.

So the validity interval moves onto the name. `instrument_identifiers` gains a
half-open `[valid_from, valid_before)` interval
([0018](0018-half-open-date-intervals.md)) and `instruments.identity_as_of` is retired.
`valid_from` is the point in market time the name became correct for the instrument --
the vintage of the source that supplied it, or the `ex_date` of the split that minted
it -- and a NULL `valid_before` means it is the name the instrument wears now. An OCC
still needs restating when its `valid_from` precedes the `ex_date` of a split that
reached it and its `valid_before` is NULL. The two bounds that decide which splits
reach it are unchanged: `ex_date <= expiry`
([0036](0036-expired-options-are-not-restated.md)) and `ex_date <= today`.

## A split mints a name rather than rewriting one

Deleting the option's OCC identifier and inserting the adjusted one makes the symbol
the contract traded under before the ex_date stop existing, so a broker file exported
before the split has nothing left to resolve to. Minting instead -- close the old row
at the `ex_date`, insert the new one from it -- makes both exports resolve to the same
instrument, which is the whole reason to carry an interval rather than a stamp.

It also makes the guard honest under partial knowledge. Rebasing an OCC hint across
the splits stored at the time and stamping `now()` claims a currency the symbol does
not have: a split learned of afterwards has an `ex_date` before the stamp, so it can
never select the option again and the stored strike stays wrong forever. A minted
row's `valid_from` is the `ex_date` it was minted from, which is a market fact rather
than a knowledge one, so a split learned later with a greater `ex_date` still selects
it. 0017 identified this failure and fixed only the case where no rebasing happened at
all; a scalar cannot express the rest of it.

## Uniqueness has to become interval-aware

The global unique index on `(identifier_type, value)` is what makes an identifier
lookup a single-row read. It cannot survive retained history, and the case that breaks
it is not ticker reuse -- it is two options on the same underlying.

A 2:1 split halves every strike. A portfolio holding the 100 and the 50 call at one
expiry holds `XYZ 250117C00100000` and `XYZ 250117C00050000` before the ex_date, and
`XYZ 250117C00050000` and `XYZ 250117C00025000` after it. The 100-strike's new name is
character-for-character the 50-strike's old one. Whole forward splits are the only kind
this pass applies, and OCC does not suffix the symbol root for them, so the collision
is reachable on any split whose strike ladder overlaps itself -- which is most of them.
It was transient only because the old row was deleted; retaining history makes it
permanent.

So `(identifier_type, COALESCE(domain,''), [valid_from, valid_before))` is excluded on
overlap rather than made unique, which needs `btree_gist`. The `COALESCE` is
load-bearing: a GIST `WITH =` on a NULL never conflicts, which is the opposite of what
the two partial unique indexes it replaces exist to achieve.

## Consequences

- **Rebasing an OCC hint stops being necessary.** With both names stored, a post-split
  symbol matches the row minted for it and a pre-split symbol matches the row closed at
  the ex_date, by value. `AdjustOCCForKnownSplits` existed to make a hint of one vintage
  match a row of another, and that is the problem the interval removes. What survives is
  the pass that mints the new name.
- **A hint now reaches a provider at its stated vintage.** Rebasing carried a hint
  forward before any lookup, so a plugin was always asked about a symbol as it stands
  today; it is now asked about the symbol the file spelled. For a contract already
  stored that costs nothing, because the DB short circuit matches the retained row
  before any plugin is called. For one that is not, the provider is asked about a symbol
  that a split may since have handed to another strike on the same ladder -- which is
  the same question as choosing between two holders of one value, issue
  [0122](../issues/0122-resolve-identity-as-of-a-date.md). Rebasing only ever masked it
  for splits that were already known.
- **A vintage is still needed, for a narrower reason.** Two instruments can hold the
  same value over disjoint intervals, so a lookup by value alone can be ambiguous. A
  reused ticker is the obvious case, and the strike ladder is the other -- the two names
  of a single contract are never ambiguous, but the name one contract gives up is the
  name its neighbour takes.
- **A NULL `valid_from` is the old NULL stamp.** It means the name predates every split
  and every split reaches it, which over-restates an option for splits effective before
  it was bought. The floor that holds without a recorded vintage is the option's own
  first trade date, because a source states an option under the name current at its
  export and an export cannot precede the purchase it describes
  ([0054](0054-share-count-basis-is-a-convention.md)).
- **The derived name needs a validity filter.** `recompute_instrument_name()` picks one
  identifier by type priority and then by value, so two OCC rows leave it choosing the
  lexicographically smaller symbol. Filtering to `valid_before IS NULL` is what makes it
  correct, and removes the explicit name write `ApplyOptionSplit` used to override it
  with.
- **The archive states the interval, not the stamp.** The bounds travel per identifier
  rather than per instrument.
- **Identity stops being current state.** Ticker reuse is now representable, though
  [0004](0004-instrument-resolution-and-merge.md) still deletes the loser of a merge
  outright, which this does not change.
