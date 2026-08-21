# A transfer match is a link between groups, not a posting

Amended by [0049](0049-a-human-assertion-is-a-correlation.md), which makes a
hand-made match derived from a person's correlation rather than an insert
preserved by hand.

The two sides of a journal are balanced against `TRANSFER_CLEARING` and are
deliberately not paired at ingest
([0022](0022-typed-per-account-cash-flow-boundary.md)), because a broker reports
them in separate statements and sometimes in separate imports. Pairing them
afterwards is a correctness requirement rather than housekeeping: until a pair is
matched, a transfer between two accounts of one portfolio reads as a withdrawal
followed by an unrelated deposit, and money-weighted return is wrong for any
multi-account portfolio. It is also what makes a residual clearing balance mean an
unmatched transfer rather than any transfer at all.

**A match is a row in `transfer_matches` naming the two tx groups, the commodity,
how they were matched and when.** It records which account holds the other side,
not merely that a match exists: the portfolio membership test in
[0022](0022-typed-per-account-cash-flow-boundary.md) asks whether *both* accounts
are members before it nets a pair or values what is in transit, and only the
identity of the other group answers that.

## A link, not a status column, not a third group, not a merge

A status column on the posting could say a side was matched but not what it matched
with, which is the one thing the consumer needs. A synthetic third group closing
both sides out would fabricate an economic event that never happened and have to be
unpicked on every rematch. Both are also unreachable rather than merely undesirable:
`check_tx_group_balance()` enforces the group invariant at COMMIT, so a side cannot
be cleared by mutating or deleting its clearing leg. The invariant that makes
ingestion trustworthy is the same one that rules out every representation except a
link beside the ledger.

The choice of a *link* over a *merge* is narrower and turns on something only
transfers have. Merging two transfer groups would erase the account boundary
money-weighted return depends on, which is the whole reason the pairing exists. An
`IMBALANCE` residual carries no such boundary -- both halves of a split trade sit in
one account, and the residual is an artefact of the split rather than an economic
fact -- so the right repair there is a merge: reassign `group_id` and delete both
residuals. `check_tx_group_balance()` is `DEFERRABLE INITIALLY DEFERRED`, so a merge
performed in one transaction is evaluated at COMMIT, where the merged group
balances. See issue
[0095](../issues/0095-match-imbalanced-groups-that-should-be-one.md).

## Keyed per (group, commodity), and directed

Balancing emits one residual per commodity, so an unpaired `JRNLSEC` group can have
a security side and a cash side in flight independently, each pairing with a
different group. "The two group ids" is therefore not a key; `(group, instrument)`
is, which is what the two unique indexes -- one per direction -- state.

The link is directed: `from_group_id` is the side the value left, whose clearing
residual is positive because the group's own leg is negative. The direction costs
nothing to record, being the sign of the residual, and a link without it would read
identically for a transfer in either direction.

## Derived and disposable

The link is never authoritative and is always cheap to rebuild, which is what
licenses a heuristic to write one at all. A re-upload replaces one side's groups
([0002](0002-transaction-ingestion-model.md)), the cascade takes the link, the
surviving side reappears as unmatched, and the matcher pairs it again. A regroup
does the same: the partition is recomputed under
[0041](0041-server-owns-transaction-grouping.md), and `applyChanges` drops every
`transfer_matches` row naming a group a posting left or the engine created.

A `MANUAL` match is disposable in the same way and for the same reason, because it
is derived too: a person's pairing is a `SCOPE_USER` correlation on the two sides'
postings, and the matcher has a pass that reads it
([0049](0049-a-human-assertion-is-a-correlation.md)). `MANUAL` means "derived from a
person's assertion", not "inserted by hand and preserved" -- which puts every method
on the same footing. An id-keyed hand-made link would not have survived in any case:
nothing rebuilt one, so a genuine repartition of either side destroyed it silently.

## Ambiguity is left unmatched

Two transfers of the same amount between the same pair of accounts in the same
window are not distinguishable, and surfacing both as unmatched is better than
pairing them arbitrarily. A wrong pair looks authoritative, it points at the wrong
account, and the report that exists to find missing sides stops finding them.

So a pair is matched only on evidence that identifies the *occurrence*: the source
naming the other account, or the proximity of the two sides' broker references.
There is no amount-and-window-alone rule. Every transfer in the sample data is
intra-broker with both sides in one import; the cross-broker case that
[0022](0022-typed-per-account-cash-flow-boundary.md) designed `TRANSFER_CLEARING`
for is real in principle but has no sample, so it cannot be calibrated and stays
unmatched and visible until one exists.

An explicit pointer is narrowing evidence, not a decision. It names the counterparty
*account*, not the occurrence, so it still needs the window and the ambiguity test:
the sample's monthly fee transfers move the same two amounts between the same
account pair every month, which a pointer plus an amount matches twelve times a
year. It is also thinner than it looks -- in the sample the field is populated on 40
rows, of which 38 are service fees naming the account a fee was charged for, which
is attribution and not a counterparty. The reference is what carries the data.

## The server may pair groups, though it may not derive a leg

[0021](0021-converters-own-transaction-grouping.md) says the server never pairs
rows, but it is about the server fabricating *ledger content*: a derived cash leg
that would double-count against a row the broker already reported. A match creates
no posting and no group, and corrupts no balance if it is wrong -- it degrades a
netting decision, and it is disposable. That asymmetry is what makes one acceptable
server-side and the other not, and it is the first case of the generalisation
[0041](0041-server-owns-transaction-grouping.md) later made outright.

## Consequences

`TRANSFER_CLEARING` becomes a signal. An unmatched balance means a side whose pair
has not arrived, its age is the age of something missing, and the transfers report
can drop the caveat that it lists every imported transfer.

The two consumers -- netting matched pairs in portfolio cash flows, and including
matched in-flight value in valuation -- read the link through the counterpart
group's own `TRANSFER_CLEARING` leg rather than through the group. That leg carries
the counterpart account and the commodity the match is keyed on, so testing it
against a portfolio's filters asks the same question from either side of a pair,
and the pair is admitted whole or not at all. Both read one view,
`portfolio_in_flight_txs`, so valuation and the flow query cannot disagree about
which pairs net.

Matching is a post-ingest job rather than part of ingestion, because the second side
can arrive in a later import: it is a function of all stored state, not of one job's
payload. Two things signal it, on one buffered channel. An admin RPC is what an
external cron job or CLI calls, and is how matching runs on a cadence -- there is no
clock in the process. Ingestion fires the same trigger once an import commits.
Neither is redundant: a cadence alone would leave a just-imported transfer reported
as unmatched until it came round, and the ingestion nudge alone would never retry a
cycle that failed. Running it more often than needed is cheap, since a cycle reads
every side no match names and writes nothing when there is nothing new.
