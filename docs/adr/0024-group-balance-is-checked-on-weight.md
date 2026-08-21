# Group balance is checked on weight

A tx group's postings are in different commodities, so a plain sum of `quantity`
cannot say whether the group balances: a buy is `+10 AAPL` and `-1855 USD`.
Balance is checked on **weight**, as in beancount, whose `get_weight` is
`cost > price > units`. A posting converts to the settlement currency at its
`unit_price` when the units its counter-leg is expected in differ from its own;
otherwise it weighs its own quantity in its own commodity. Weights accumulate per
commodity, and each commodity left over is routed to an explicit residual posting
rather than rejected ([0021](0021-converters-own-transaction-grouping.md) and
[0022](0022-typed-per-account-cash-flow-boundary.md) already settled that routing
beats rejecting).

Beancount makes the conversion an explicit annotation -- `{}` for a cost, `@` for
a price -- written by whoever wrote the entry. We have no such annotation, only a
`unit_price` column that converters populate indiscriminately, so the `tx_type`
stands in for it: it says what units the other side of the event is expected in.
`BUY*`, `SELL*`, `REINVEST` and `CLOSUREOPT` exchange a security for money, so the
security leg converts. Everything else moves a commodity without converting it --
a journal, a charge and a dividend all have their counter-leg in the units they
are already in -- except a movement across currencies, where
`trading_currency != settlement_currency` and the price is an FX rate. Two guards
complete it: a leg already denominated in the settlement currency never converts,
and a posting with no price cannot convert at all.

## Considered options

- **Summing per commodity with no conversion.** Rejected: a correctly grouped,
  complete trade would look unbalanced in both AAPL and USD, and routing would
  invent two residuals for it.
- **Deriving the cash leg as `-(quantity * unit_price)`.** Rejected in
  [0021](0021-converters-own-transaction-grouping.md): Fidelity already reports its
  own cash row, so this double counts. Weighing is not the same thing -- it is
  arithmetic on the legs supplied, and a group that already carries its cash row
  weighs to zero and routes nothing.
- **Converting whenever a `unit_price` is present.** Rejected: the Fidelity
  converter puts the broker's `Amount` in `quantity` for cash rows while still
  reading the `Price` column, so a `-1855 USD` cash row carrying `185.50` would
  weigh `-344,102 USD`. This is what the settlement-currency guard exists for; it
  is a property of the weight function, not a workaround for one broker.
- **Inferring the conversion from whether a leg found a match.** Rejected as
  circular -- which legs match cannot be known before choosing the units to balance
  in -- and because it gives the wrong answer on exactly the one-sided groups the
  mechanism exists for. A lone `JRNLSEC +10 AAPL` and a lone
  `BUYSTOCK +10 AAPL @ 185.50` are both unmatched, and they want opposite answers.
- **Carrying the cost on both legs of a securities transfer**, as beancount does,
  so a `JRNLSEC` weighs in currency. Rejected: it only balances when both sides are
  present, and ours arrive in separate statements. A currency-denominated clearing
  leg holds a frozen cash value rather than the shares, so the holding still
  vanishes in transit and the pair will not cancel if the two statements price it
  differently.
- **Adding an explicit annotation to the upload format.** Correct, and the option
  to keep open. Rejected for now because nothing balances until every converter has
  been taught to set it, whereas the tx type is already there.
  [0029](0029-posting-weight-is-stored.md) stores the weight this rule computes on
  the posting, which is the same annotation supplied by the server rather than by
  the upload.

The choice between these is decided by an asymmetry. **Failing to convert leaves a
residual in some commodity: visible in the imbalance report, attributable to a
broker, and fixable per converter. Converting wrongly deletes a holding and puts
cash in its place, silently.** So the rule converts only where the event type says
the other side is money, and lets everything else show up as a residual. The known
cost is that a cross-currency movement whose broker supplies no FX rate leaves a
residual rather than balancing.

## Which columns are weighed

Weight is computed from the raw `quantity` and `unit_price`, never from the
`split_adjusted_*` pair. A split adjusts a security leg's quantity while leaving
its cash counter-leg alone, and the pair only continues to balance because the
price is adjusted inversely -- a 3:1 split turns `+10 AAPL @ 185.50` into
`+30 @ 61.8333...` against the same `-1855 USD`. The product is preserved in exact
arithmetic, but `61.8333...` is not exactly representable, so an exact
`SUM(...) = 0` over adjusted values would reject correct groups on any instrument
that has had a split in an awkward ratio. The raw columns carry no such rounding;
see [0028](0028-cumulative-split-factor-is-an-exact-rational.md) for why the
adjusted ones do.

## The tolerance decides the account type, not whether a residual is routed

A residual is not a floating-point fudge. A trade of 37 shares at 12.3456 costs
456.7872 against a broker cash row of -456.79; the 0.0028 residual is exact and
real, and is entirely an artefact of the source having been written to 2dp.
Beancount infers a tolerance of half the last significant digit for this case and
ledger checks against the entry's local precision.

**Every non-zero residual gets a posting**: `IMBALANCE` or `TRANSFER_CLEARING` at
or above the tolerance, and `SOURCE_ROUNDING` below it. A sub-tolerance residual
on a journal is rounding too, so `SOURCE_ROUNDING` beats the transfer
classification rather than the other way round. Suppressing the sub-tolerance
residual instead leaves the group summing to a small non-zero value, which makes
the ordinary, well-formed, 2dp-rounded trade groups exactly the ones an exact
`SUM(...) = 0` constraint rejects.

Two alternatives were considered. **Absorbing the sub-tolerance residual into the
weight of the group's converting leg** needs no new type and writes no extra rows,
but it makes a leg's stored weight something other than
`quantity * unit_price * contract_size`, so a reader comparing the two sees a
discrepancy with nothing to explain it. **Folding the small residuals into
`IMBALANCE`** is simpler still, and is what dropping the tolerance outright amounts
to, but the point of a typed residual account is to classify what is left over: an
`IMBALANCE` balance is converter work to be done and a rounding difference is not,
so merging them makes the imbalance figure permanently non-zero for every broker
that rounds.

Once residuals are routed the group sums to zero by construction, which is what
lets the deferred balance constraint be an exact `SUM(...) = 0` with no tolerance
of its own.

### The bound has two terms

Exact decimals ([0026](0026-exact-decimals-bounded-by-closure.md)) let the
tolerance be inferred from the scale of the amounts rather than fixed as a
constant. It takes two terms, because a group holds two figures rounded
differently:

```
tolerance = half a cent + SUM over converted legs of |units| x half the last digit of the price
```

The first term is a cash row written to 2dp, out by half a cent however large it
is. The second is a price written to 2dp, out by half a penny *per unit*: a group
balances on weight, so a holding of 2,676 units at a printed 7.67 is 10.30 away
from the cash row the same statement carries. Covering only the first term reports
every large trade as an imbalance -- 70 of 70 in the sample data, the whole of one
broker's imbalance report.

The scale is floored at 2 decimal places rather than read off the figure, because
it is not recoverable: Fidelity strips trailing zeros in its own download, so
`47.1` and `47.11` appear in one instrument's series. That is an assumption about
how brokers quote rather than about any row, and a source genuinely quoting to 1dp
keeps reporting imbalances rather than silently absorbing them.

The bound is never below the fixed term, so the inference can only reclassify an
imbalance as rounding and never the reverse. What it costs is that a small missing
leg can hide inside a high-quantity trade's bound, since summing per-leg bounds
assumes every price erred the same way.

Exactness also removes the need for a floor against double rounding: a converted
weight of `100000 * 1234.5678` is around `1.2e8`, where a double's ULP is about
`1e-8`, so a `qty_is_zero`-style `1e-9` emitted spurious residuals on large trades.
