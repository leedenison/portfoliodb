# A holding's earliest declaration pads; the rest are checked

A holding declaration is a user's statement that they held N units on a date. The
first one for a holding generates an INITIALIZE transaction to make that true --
beancount's `pad` -- which means it is true by construction and can never catch an
error. Every later declaration is checked against what the transactions add up to
instead, and a disagreement is the only signal the system has that a broker CSV
was misparsed, a transfer missed, or a row silently dropped by a converter. The
pair is beancount's `pad` and `balance`, and the safety comes entirely from the
second half.

## Why the discriminator is derived, not stored

A `kind` column would be a second statement of something the dates already say,
and the two could disagree. Adding a declaration earlier than the current pad, or
deleting the pad, changes which declaration seeds the opening balance -- with a
stored column each of those becomes a write that can be missed. Derived from
`MIN(as_of_date)` per holding there is nothing to maintain and no state to
reconcile, at the cost of one indexed subquery per row read.

The corollary is that the unit of recalculation is the holding rather than the
declaration: which declaration pads a holding depends on the others, so a single
declaration is not enough to know whether it is the pad.

## Why the check is computed on read

The three existing surfaces for data problems -- `validation_errors`,
`identification_errors`, `unhandled_corporate_events` -- are stored rows written
when a problem is detected. An assertion is not like them. Its verdict is a
function of the current transaction set, which moves under an ingestion, an
instrument merge, an option split application and a `RecomputeSplitAdjustments`
pass alike. There is no one place all of those funnel through, so a stored verdict
would need invalidating from each and would be wrong the moment one was missed.

Computed in the read query it is current by construction, with no triggers at
all, and it closes on its own when the data comes back into agreement, which
none of the stored surfaces can do. The cost is recomputing a small aggregate per
declaration on each read; declarations are a handful per user.

## Why the tolerance is not zero, and not a constant

The comparison crosses a rounding. Both sides are exact decimals, but converting a
posting from its own `share_count_basis` into the declaration's divides by the
cumulative split factor, and a reverse split in an awkward ratio has no finite
decimal form (see [0028](0028-cumulative-split-factor-is-an-exact-rational.md) and
[0026](0026-exact-decimals-bounded-by-closure.md)).

Postings are therefore grouped by basis and summed before conversion, so the
division happens once per denomination rather than once per posting, and the
tolerance is the number of contributing bases whose factor is not `1/1`, in units
of the last place of the declared scale. That is a bound that can be stated,
unlike a fixed epsilon, and it collapses to exactly zero when no split falls in
the window -- which is the common case, so most assertions are checked exactly.

An absolute constant was rejected for the reason
[0026](0026-exact-decimals-bounded-by-closure.md) gives: it is silently
scale-dependent, too loose for a small holding and too tight for a large one.

## Why a pad reports no verdict

A pad's own check can only ever pass, because the INITIALIZE transaction is what
makes it pass. Reporting that as a result would suggest it had found something
out. It is labelled as the opening balance instead.
