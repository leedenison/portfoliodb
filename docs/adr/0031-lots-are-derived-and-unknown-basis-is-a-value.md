# Lots are derived, and an unknown cost basis is a value

Lot identity and disposal matching (0044) settle four things that the cost basis
methods built on top of them (0045) cannot revise later, because each one decides
the key or the columns of the lot record.

## A lot is opened by sign, not by transaction type

A `USER` posting whose quantity has the same sign as the running position opens or
augments a lot; one whose sign opposes it reduces lots. Quantities are signed with
no type-based sign flip ([0020](0020-double-entry-postings.md)), so this needs no
enumeration over the 24 `TxType` values and covers `REINVEST`, `CLOSUREOPT`,
inbound transfers, pads and short positions alike. A type-driven rule would also
call a `BUY` that covers a short an acquisition, which is the one case the sign
rule gets right for free. It is the same objection
[0022](0022-typed-per-account-cash-flow-boundary.md) raised to classifying cash
flows by transaction type.

Lot identity is therefore the acquiring posting's `txs.id`, and the cost is a read
over the group that posting already belongs to: its `weight`, exact and stored
([0029](0029-posting-weight-is-stored.md)), plus the group's `EXPENSE` legs.

## Whether the basis is known is read off the counterparty, not flagged

Nothing records it, because the acquisition's counterparty leg already says it: a
`USER` cash leg means known, part `IMBALANCE` means partially known, `EQUITY`
means a pad and so unknown by construction, and `TRANSFER_CLEARING` means pending
until the pair is matched. This is the same read the cash-flow boundary performs,
and a separate flag would be a second place to say it that can disagree.

## An unknown basis stays unknown

`cost` is NULL where it is not known, and the gain figures report a known/unknown
split rather than a number that papers over the gap. This is what lets a portfolio
with incomplete history be usable without its gains being fiction.

### Considered options

- **Zero cost.** Rejected: it reports the whole proceeds as gain.
- **The market price on the pad or transfer date.** Rejected, and it is the
  tempting one, because the price series can supply it and the result looks like a
  fact. [0026](0026-exact-decimals-bounded-by-closure.md) already ruled on this
  shape -- encoding an estimate as an exact decimal misrepresents its provenance --
  and [0024](0024-group-balance-is-checked-on-weight.md) recorded the asymmetry
  that decides it: a residual left visible is attributable and fixable, while
  converting wrongly deletes a holding and puts cash in its place, silently. A
  fabricated basis is the second kind of error. An estimate remains available as
  something the user invokes explicitly and that carries its provenance.
- **Refusing to compute gains until the user declares a basis.** Rejected: it
  blocks reporting entirely on exactly the portfolios with incomplete history that
  pads exist to serve.

The acquisition **date** does take a default, the pad or transfer date, but for
ordering only. It must not drive the 30-day rule or a long/short-term split; being
the only date available does not make it an acquisition date.

## The cost basis method parameterises a re-derivation

The method is an input to the derivation, not a column on a lot. Changing it
rebuilds the lot set, in the manner of the `split_adjusted_*` recompute.

Lots are the journal and a Section 104 pool is a view over them, so per-lot and
pooled are not competing models. The fork dissolves by separating the two jobs a
lot does: **quantity** is per `(user, broker, account, instrument)` because
holdings are, while **cost** is drawn from a scope the method chooses -- the
individual lot for specific identification, FIFO and LIFO, and a
`(user, instrument)` pool for Section 104, because the UK pool belongs to the
person rather than the account. A Section 104 disposal removes quantity from its
own account and draws cost from the user-level pool, so the two never collide and
one table shape serves both.

A pool is consequently a `lots` row with no acquiring posting and no account,
which is why a disposal links to a lot rather than to an acquiring `txs.id`.

- **Considered and rejected: fanning a pool disposal pro rata across every
  remaining lot.** It keeps the link pointed at a transaction, but writes one row
  per surviving acquisition for every disposal and attributes a per-lot split that
  is arbitrary and means nothing under a method that has no per-lot identity.

## Lots are stored, unlike the declaration check

[0030](0030-declarations-are-padded-then-asserted.md) reached the opposite
conclusion for an assertion's verdict, and computed it on read so that nothing
has to invalidate it. Lots face the same invalidation problem -- an ingestion, an
instrument merge and a `RecomputeSplitAdjustments` pass all move the inputs -- and
still get stored, because the two differ in size. A declaration check is a small
aggregate over a handful of rows per user, so recomputing it per read is
free. Matching a disposal is a replay of the whole transaction history for an
instrument, under a method with three ordered stages, one of which looks forward
30 days.

Storing it therefore risks drift where the read-computed check cannot. The
answer is not a stored verdict trusted on faith but a periodic re-derivation that
asserts equality against the stored set -- the reconciliation pattern 0043 uses
for the same reason.

## The matching rule is recorded on each match, not inferred from the setting

`matched_by` names the rule that produced each row -- `NOMINATED`, `FIFO`, `LIFO`,
`HIFO`, `SAME_DAY`, `THIRTY_DAY`, `POOL` -- following the shape 0068 uses for its
transfer link. One Section 104 disposal splits across three rules, so a single
per-user method cannot describe what happened. Nominated matches are user or
broker input rather than output and cannot be re-derived, so a rebuild has to keep
them and regenerate only the rest; without the discriminator there is nothing to
tell input from output and a rebuild destroys the user's choices silently. And the
setting is one mutable row, so a historical match that does not say how it was
made becomes unexplainable the moment it changes.

## Consequences

**A gain cannot be a leg of a balanced group.** Beancount's disposal leg weighs at
cost, so the gain is the residual and falls out of the zero-sum rule. Ours weighs
at price ([0024](0024-group-balance-is-checked-on-weight.md)), so a `SELLSTOCK`
group already balances against its cash row and there is no residual for a gain to
occupy. Making one available would require the disposal to weigh at cost, which
would make the exact `check_tx_group_balance()` trigger depend on lot matching,
which depends on a user-configurable method -- a database constraint that moves
when a setting changes. Gains are a derived layer over postings, computed as
`proceeds - SUM(cost_out)` and not stored.

**A pad's declared basis stays out of the postings.** A user-declared cost is an
input to the lot derivation, not a leg: the `INITIALIZE` posting and its `EQUITY`
counterparty go on weighing in shares. Putting cost into them would need the pad to
weigh in currency, which is the shape 0024 rejected for transfer legs and for the
same reason. It is declared as a total rather than a per-share price, because a
total is split-invariant and so needs no `share_count_basis` of its own, and
because deriving a total from a per-share price divides. See 0075.

**Transfer matching gates coverage, not correctness of the model.** Without 0068
an inbound transfer yields an unknown-basis lot, which the rules above handle.
Under Section 104 it does not matter at all, since the pool spans the person and an
intra-user transfer never touches it; under the per-account methods it is the
difference between a basis that arrives and one that stays unknown.

**A pool has no acquisition to inherit a share count basis from**, so the rule in
[spec/bitemporality.md](../spec/bitemporality.md) that a lot inherits the
acquisition's `share_count_basis` does not reach it and a pool declares its own.
