# Group balance is checked on weight

A tx group's postings are in different commodities, so a plain sum of `quantity`
cannot say whether the group balances: a buy is `+10 AAPL` and `-1855 USD`.
Balance is checked on **weight**, as in beancount, whose `get_weight` is
`cost > price > units`. A posting converts to the settlement currency at its
`unit_price` when the units its counter-leg is expected in differ from its own;
otherwise it weighs its own quantity in its own commodity. Weights accumulate per
commodity, and each commodity left over is routed to an explicit `IMBALANCE` or
`TRANSFER_CLEARING` posting rather than rejected (see
[0021](0021-converters-own-transaction-grouping.md) and
[0022](0022-typed-per-account-cash-flow-boundary.md), which already settled that
routing beats rejecting).

Beancount makes the conversion an explicit annotation -- `{}` for a cost, `@` for
a price -- written by whoever wrote the entry. We have no such annotation, only a
`unit_price` column that converters populate indiscriminately, so something has to
stand in for it. The `tx_type` does: it says what units the other side of the
event is expected in. `BUY*`, `SELL*`, `REINVEST` and `CLOSUREOPT` exchange a
security for money, so the security leg converts. Everything else moves a
commodity without converting it -- a journal, a charge and a dividend all have
their counter-leg in the units they are already in -- except a movement across
currencies, where `trading_currency != settlement_currency` and the price is an FX
rate. Two guards complete it: a leg already denominated in the settlement currency
never converts, because it is already in the units the group balances in; and a
posting with no price cannot convert at all.

## Considered options

- **Summing per commodity with no conversion.** Rejected: a correctly grouped,
  complete trade would look unbalanced in both AAPL and USD, and routing would
  invent two residuals for it.
- **Deriving the cash leg as `-(quantity * unit_price)`.** Rejected in
  [0021](0021-converters-own-transaction-grouping.md): Fidelity already reports
  its own cash row, so this double counts. Weighing is not the same thing -- it is
  arithmetic on the legs supplied, and a group that already carries its cash row
  weighs to zero and routes nothing.
- **Converting whenever a `unit_price` is present.** Rejected: the Fidelity
  converter puts the broker's `Amount` in `quantity` for cash rows while still
  reading the `Price` column, so a `-1855 USD` cash row carrying `185.50` would
  weigh `-344,102 USD`. This is what the settlement-currency guard exists for; it
  is a property of the weight function, not a workaround for one broker.
- **Inferring the conversion from whether a leg found a match.** Rejected as
  circular -- which legs match cannot be known before choosing the units to
  balance in -- and because it gives the wrong answer on exactly the one-sided
  groups the mechanism exists for. A lone `JRNLSEC +10 AAPL` and a lone
  `BUYSTOCK +10 AAPL @ 185.50` are both unmatched, and they want opposite answers.
- **Carrying the cost on both legs of a securities transfer**, as beancount does,
  so a `JRNLSEC` weighs in currency. Rejected: it only balances when both sides
  are present, and ours arrive in separate statements. A currency-denominated
  clearing leg holds a frozen cash value rather than the shares, so the holding
  still vanishes in transit and the pair will not cancel if the two statements
  price it differently.
- **Adding an explicit annotation to the upload format.** Correct, and the option
  to keep open. Rejected for now because nothing balances until every converter
  has been taught to set it, whereas the tx type is already there.

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
`+30 @ 61.8333...` against the same `-1855 USD`. The product is preserved in
exact arithmetic, but `61.8333...` is not exactly representable, so the adjusted
weight misses by the rounding. An exact `SUM(...) = 0` over adjusted values would
reject correct groups on any instrument that has had a split in an awkward ratio.
The raw columns carry no such rounding; see
[0028](0028-cumulative-split-factor-is-an-exact-rational.md) for why the adjusted
ones do.

## Tolerance

A residual is routed only above a tolerance: half a cent for money, `1e-6`
otherwise. This is not a floating-point fudge and it does not disappear when
[0042](../issues/0042-exact-decimal-quantities-prices.md) makes quantities exact.
A trade of 37 shares at 12.3456 costs 456.7872 against a broker cash row of
-456.79; the 0.0028 residual is exact and real, and is entirely an artefact of the
source having been written to 2dp. Beancount infers a tolerance of half the last
significant digit for this case and ledger checks against the entry's local
precision. Half a cent is what beancount would infer for 2dp money.

What 0042 changes is that the tolerance can then be inferred from the scale of the
amounts rather than fixed as a constant, which is only expressible once they carry
one. It also removes the separate need for a floor against double rounding: a
converted weight of `100000 * 1234.5678` is around `1.2e8`, where a double's ULP
is about `1e-8`, so a `qty_is_zero`-style `1e-9` would emit spurious residuals on
large trades.

The balance constraint in
[0041](../issues/0041-enable-balance-constraint.md) needs no tolerance of its own
either way. Once residuals are routed the group sums to zero by construction,
which is what lets that constraint be an exact `SUM(...) = 0`.
