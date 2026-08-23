# A posting names a security and a line

A posting carries `instrument_id` and a nullable `listing_id`. It names the
security always, and the currency line within it when something said which. A
null line means nothing did -- a first-class state, not a gap.

This supersedes [0070](0070-a-posting-names-a-listing.md), which had a posting
always name a line and used each security's currency-unknown listing to stand for
"we do not know which". A sentinel row makes partial knowledge sayable only by
overloading a row that already says something else: under
[0068](0068-a-listing-is-a-currency-of-a-security.md) the null-currency listing
says *how many lines this security has is unknown*, which is a claim about the
security, not about a posting that failed to name one of several known lines.
Two different unknowns then share one representation and neither can be read back.

## The two columns cannot disagree

0070's objection to carrying both was that two correlated columns on the hottest
table in the schema are a chance to drift. The answer is a foreign key:

    FOREIGN KEY (instrument_id, listing_id)
      REFERENCES instrument_listings (instrument_id, id) MATCH SIMPLE

`MATCH SIMPLE` skips the check when either column is null, which is exactly what
makes the partial state representable, and a `CHECK (instrument_id IS NOT NULL OR
listing_id IS NULL)` closes the case it leaves open. A line belonging to some
other security is then unrepresentable rather than merely unintended, which is a
stronger guarantee than not having the column at all -- a join to
`instrument_listings` proves nothing about the row that was written.

Keeping the security also means the passes that read at security grain --
portfolio filters, split adjustment, residual balances, external flows, the
transfer matcher -- go on reading one column rather than acquiring a join each.

## What a posting balances in is the security

`weight_commodity` keeps `cur:<code>`, `inst:<uuid>` and `desc:<text>`. 0070's
`lst:<uuid>` is not introduced.

0070's balance argument stands and is what decides this. A group balances on an
exact sum per commodity ([0024](0024-group-balance-is-checked-on-weight.md)) and
the weight is computed once at ingest and stored
([0029](0029-posting-weight-is-stored.md)), so a group's legs have to be weighed
at one grain or a residual appears that nothing put there. The line is nullable by
design, so listing grain is not available for every posting; the security is the
only grain that is.

The cost is that the balance check no longer double-checks the line a posting was
put on. For it to have caught anything a group would need two or more
source-supplied postings in one security resolving to different lines -- a routed
leg does not count, since its line is inferred from the legs it balances -- and
the one realistic instance of that is a currency conversion, where two lines are
correct rather than a mismatch. What is really given up is that a routed
residual's line has to be inferred rather than derived: the sum is per
security-grain commodity, so it does not say which line the leftover is on. The
residual takes the line every leg it balances shares, and none where they differ.

What this buys is that `weight_commodity` is not a third correlated column. The
merge and the listing split rewrite `listing_id` alone; at listing grain each
would have to rewrite the weight in the same statement and get it right or
unbalance groups -- which is 0070's own objection, one level down. It also means
a conversion between two lines of one security inside a single group balances and
routes nothing, where at listing grain it would route two `TRANSFER_CLEARING`
residuals into the same group, which `transfer_matches` can never pair.

## Naming the line

A posting's line is settled at ingest, from what is already in hand: the stated
`trading_currency`, then the line identification itself named, then the security's
sole line where it has exactly one with a currency, then none. Every rung reads
what the security already has; none mints a line, because a broker states a
currency to say what its own figures are in, and a security is quoted in a
currency whether or not anyone traded it. Lines come into existence when a
provider or a listing-grain identifier asserts one
([0069](0069-a-listing-is-named-by-a-security-identifier-and-a-currency.md)).

`settlement_currency` is not a rung. It is what the record settled in, which for
every source the repository reads is the account's own currency, so on a security
quoted in two it says nothing about which.

## Consequences

A holding is per line: two lines of one security are two holdings an FX rate
apart, and adding them would report a number in no currency at all. A holding
whose postings named no line is reported unpriced, which is the same answer 0068
gives for a line with no currency and for the same reason.

Price fetching makes the opposite trade from valuation. A posting on a known line
fetches that line; one on no line fetches every priceable line of its security,
because the history has to be there for whichever it turns out to be. Fetching
too much costs requests; valuing at a currency nobody stated costs correctness.

A merge moves its loser's postings onto the survivor's line of the same currency
family, and onto no line where the survivor has none to match. Nulling degrades a
posting to "this security, line not known", which is exactly what is true of it
once the line it named is gone -- and it is recoverable, where under a sentinel
model there was nowhere for those postings to go. The split in
[0071](0071-listings-merge-by-currency-and-an-unknown-one-splits.md) becomes a
fill-in rather than a move: postings on no line acquire one when something names
it, and nothing has to be taken off a sentinel row first.
