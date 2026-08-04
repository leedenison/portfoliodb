# Exact decimals are bounded by closure under + - *

Quantities and prices are decimal, not binary floating point, and the schema has
been moving that way: every numeric column except the four on `txs` is already
`NUMERIC`. What was never settled is where exactness stops, because it has to
stop somewhere.

Decimals are closed under addition, subtraction and multiplication, and under
nothing else. Those three are exact: a product's scale is the sum of its
operands' scales, always finite. Division is not, and no storage type fixes it --
`1/3` has no finite decimal representation, so Postgres picks a scale for you
(`select_div_scale`, at least 16 significant digits) and rounds. The
transcendentals are worse: `ln`, `exp`, `pow` and `sqrt` on `numeric` are
software series expansions over digit arrays, so they are both slower than their
`float8` counterparts and still approximate.

So the boundary is algebraic, and it can be read off an expression rather than
argued about: **a value is decimal if and only if its expression tree, from
source-recorded values, contains only `+`, `-` and `*`. The first division or
transcendental ends exactness, and past it `double` is the honest and much faster
representation of an approximation.**

Transcribed values are decimal because a source wrote them down in decimal:
`quantity`, `unit_price`, `strike`, `contract_multiplier`, OHLC bars,
`split_from`/`split_to`, `declared_qty`, inflation index values. Everything
derived from them by the closed operations stays decimal, and that is where the
useful invariants live: a posting's weight is `quantity * unit_price *
contract_size`, a group's balance and a holding are sums, a realised gain is a
difference. Exactness there is free, which is why it is worth having. Valuation
divides by an FX rate, and performance metrics link returns geometrically and
solve for an internal rate, so both are past the boundary.

## Consequences

**A stored column is not automatically a fact.** `split_adjusted_quantity` and
`split_adjusted_unit_price` are a materialised cache of `quantity * factor`,
recomputable from the quantity, the `share_count_basis` and the split chain.
Being stored does not make them facts, and they are classified by their
expression like anything else: the factor's division puts them past the boundary,
so they carry a declared rounding scale (see
[0028](0028-cumulative-split-factor-is-an-exact-rational.md)). The rounding is
confined to the cache because the facts it derives from remain exact.

**Balance is checked on raw values, never on the split-adjusted pair.** See
[0024](0024-group-balance-is-checked-on-weight.md), which this is recorded
against.

**Queries cast operands, not results, at the boundary.** Valuation multiplies a
decimal quantity by a price and divides by an FX rate once per instrument per
calendar day. Casting the result would have Postgres compute sixteen significant
digits of `numeric` division for every one of those rows and then discard them;
casting the operands keeps the day grid on hardware arithmetic, which is what it
runs on today. The cast is also the only marker of where exactness ends in a
query, so it is worth keeping in one place rather than spreading the conversion
across the expression. See [spec/performance.md](../spec/performance.md).

**A tolerance survives at ingest.** Exactness is not the same as agreement. A
trade of 37 shares at 12.3456 costs 456.7872 against a broker cash row of
-456.79, and that 0.0028 is exact, real, and entirely an artefact of the source
having been written to 2dp. What exactness changes is that the tolerance can be
inferred from the scale of the amounts rather than fixed as a constant, and that
the floor against double rounding is no longer needed. Also in
[0024](0024-group-balance-is-checked-on-weight.md).

## Considered options

- **Exact decimals everywhere.** Rejected on two grounds. It is not achievable:
  the moment a value passes through an FX division or an average it is
  approximate whatever type carries it, so "everywhere" means "decimal-typed
  approximations", not exact ones. And it is misleading where it is achievable
  in form only -- a decimal string for a portfolio valuation asserts that its
  digits are meaningful when the FX division two layers down already destroyed
  them. Encoding an estimate as an exact decimal misrepresents its provenance,
  and that is the objection; the cost of numeric arithmetic on the valuation
  day-grid is secondary.

- **Floating point everywhere, with tolerances.** The status quo, and workable:
  the balance invariant is expressible against `double precision` using a
  relative epsilon. Rejected because every call site then has to choose and
  justify one, and an absolute epsilon is silently scale-dependent -- `1e-9` in
  the style of `qty_is_zero` is smaller than a double's ULP at the magnitude of
  a large converted weight, so it emits spurious residuals on exactly the trades
  that matter most. Exact decimals remove the question rather than answering it
  repeatedly.

- **Rational arithmetic throughout**, as GnuCash does with a numerator/
  denominator pair. Fully exact, including under division. Rejected as
  disproportionate: it is contagious through every layer and every client, and
  the only place the repository actually needs it is the split factor, where it
  is applied locally ([0028](0028-cumulative-split-factor-is-an-exact-rational.md)).

The wire encoding that follows from this is recorded separately in
[0027](0027-decimal-values-cross-the-wire-as-strings.md).
