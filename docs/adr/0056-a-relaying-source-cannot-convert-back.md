# A relaying source cannot convert back

[0054](0054-share-count-basis-is-a-convention.md) retired `share_count_basis`
from `txs`, `eod_prices` and `holding_declarations` and fixed each row kind's
basis by convention: a posting on its `trade_date`, a price bar on its
`price_date`, a declaration on its `as_of_date`. A source holding data on any
other basis was to convert before it uploaded.

That rests on one premise: **a source that restates knows the ratio it used,
because that is how it restated.** The premise holds for a source that performs
its own restatement, and it is false for one that relays a third party's.

`cli/google` is the second kind. It reads GOOGLEFINANCE, which back-adjusts
historical closes, and it has neither the split ratios nor the split chain that
produced them. The API exposes no split history, and GOOGLEFINANCE offers no
unadjusted attribute to ask for instead. So the conversion the convention
requires has nowhere to happen, and the series cannot be uploaded correctly at
all: every bar before a split in the imported range is stored as though it were
as-traded, and `RecomputeSplitAdjustments` divides it by the split factor a
second time. An NVDA bar before 2024-06-10 lands 10x low.

The convention offered no way to state this, and no way to refuse it either. A
relaying source's only conforming options were to upload numbers it knew were
wrong, or not to exist.

So the field comes back on all three tables, exactly as it was: `NOT NULL` in
the schema, absent on the wire, defaulted by insert trigger to the row's own
date. `pricefetcher.ShareCountBasis` returns with `AsTraded` and `AsOfFetch`,
and the archive's `PriceRow`, `Posting` and `Declaration` carry an optional
basis again.

## Consequences

**0054's objection is real and is not answered.** A row that restates without
declaring it still reads exactly like one that did not, so a source can be
silently wrong and stay conforming. That is the cost of this decision and it is
paid knowingly. It is the smaller cost: the convention makes a relayed series
*unrepresentable*, while the field makes a mis-declared one *undetectable*, and
a format that cannot express the data at all fails sooner and worse than one
that can express it wrongly.

**The evidence 0054 cited still stands.** Across four brokers and 2,892 postings
nothing ever set the column, and no price plugin ever declared anything but
`AsTraded`. The column earns its place on the strength of the one source that
needs it rather than on how often it is set, because that source has no
alternative.

**The pad stops converting.** `ConvertQtyToBasis` existed only to carry a
declaration's quantity to the INITIALIZE pad's date, which is what a pad
denominated on its own `share_count_basis` does without arithmetic. It goes,
along with its call on the create and recalculate paths.

**The declaration form asks two questions again.** It offers "the share count on
the as of date" against "today's share count". 0054 was right that this is a
question some people cannot answer -- and the answer for anyone reading a
current holdings screen is the second one, which the convention gave them no way
to say.

**Identity is untouched.** A file naming an instrument as of its `exported_at`
is a different question with a different answer, decided in
[0055](0055-identifier-validity-is-an-interval.md) and unaffected either way.
